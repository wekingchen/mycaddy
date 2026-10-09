# forwardproxy UDP-over-HTTP overlay

本目录只维护 **相对于 `klzgrad/forwardproxy:naive` 的 UDP-over-HTTP 增量能力**，不复制、不长期维护 klzgrad 的完整 forwardproxy 源码。

## 为什么采用 overlay

项目目标是同时满足两件事：

1. 尽量紧跟 `klzgrad/forwardproxy:naive`，保留其 Naive padding、Caddy 版本选择以及 sing-quic BBRv1 方案。
2. 在其基础上继续提供 `imgk/forwardproxy:udpinhttp` 的 UDP-over-HTTP 能力。

因此每次构建都重新拉取 klzgrad 最新 `naive`，然后再应用本目录的增量代码。

## 代码来源

UDP 实现当前固定基于：

```text
imgk/forwardproxy
commit: 6d1c91becd7d530a7cca2c26d3fd8bc70035d8ec
message: add UDP in HTTP
```

如以后需要同步 imgk 的新实现，应先单独 review 差异，再更新 overlay；不能直接覆盖 klzgrad 主线。

## 当前支持能力

| 能力 | 状态 |
| --- | --- |
| RFC 9298 CONNECT-UDP | ✅ |
| HTTP/1.1 Upgrade 流式 UDP | ✅ |
| HTTP/2 Extended CONNECT 流式 UDP | ✅ |
| HTTP/3 Datagram | ✅ |
| CONNECT-UDP-BIND over HTTP/1.1 | ✅ |
| CONNECT-UDP-BIND over HTTP/2 | ✅ |
| CONNECT-UDP-BIND over HTTP/3 | ❌ 暂未实现 |
| 自定义 `udp_uri_template` | ✅ |

代码中的 `HandlePacketBind` 当前会明确返回：

```text
connect-udp-bind over http3 is not supported yet
```

因此不能把 CONNECT-UDP-BIND 描述成“所有 HTTP 版本均支持”。

另外，当 forwardproxy 配置了 `upstream` 时，`tryUDPoverHTTP` 会放弃接管该请求；这是当前实现的既有行为。

## QUIC 适配

imgk 原实现使用：

```text
github.com/quic-go/quic-go
```

klzgrad 当前的 Caddy BBRv1 补丁则把 Caddy QUIC 栈切换为：

```text
github.com/sagernet/quic-go
github.com/sagernet/sing-quic
```

为了避免一个二进制里混用两套 HTTP/3 Stream 类型，本 overlay 已统一适配为 SagerNet QUIC。

SagerNet `http3` 当前没有导出原版 quic-go 的 `ParseCapsule`，所以 overlay 内保留了一个最小的 RFC 9297 Capsule framing 解析辅助函数 `parseUDPCapsule`。它只替代缺失的辅助 API，不改变 UDP-over-HTTP 协议逻辑。

### HTTP/2 双向流与 HTTP/3 Datagram

HTTP/2 Extended CONNECT **不能**像 HTTP/1.1 一样 Hijack 底层 TCP 连接；当前实现通过 HTTP/2 的请求 Body + ResponseWriter 传输双向 Capsule，并在需要时 Flush。Go 的 HTTP/2 Extended CONNECT 必须在服务端启用 RFC 8441（`GODEBUG=http2xconnect=1`）。

HTTP/3 的 CONNECT-UDP 依赖**两层**支持：QUIC 传输的 `EnableDatagrams` 以及 HTTP/3 Server 的 `EnableDatagrams`。缺一不可，所以 `scripts/apply-caddy-http3-datagrams.py` 必须在 klzgrad 的 BBRv1 补丁后执行。该脚本还将 QUIC 初始包大小设置为 **1452 字节**，供官方 NaiveProxy 客户端在典型 1500-MTU 链路上进行双层 QUIC 测试。对于路径 MTU 更低的网络不能保证兼容，必须另行验证。

不满足这些条件，代码即使编译成功、模块齐全，也会在真实流量测试中失败。本项目将这些测试加入了正式发布前的验收标准。

## 构建接入点

`scripts/apply-forwardproxy-udp.py` 只修改 klzgrad `forwardproxy.go` 的三个位置：

1. `Handler`：加入 `udpProxyServer` 与 `udp_uri_template`。
2. `Provision`：初始化 UDP proxy server。
3. `ServeHTTP`：在普通 CONNECT 逻辑前尝试处理 UDP-over-HTTP 请求。

脚本要求每个锚点**恰好匹配一次**。

如果 klzgrad 上游重构导致某个锚点消失或出现多次，构建会明确失败，提示人工检查，而不是静默使用旧源码或模糊地继续打补丁。

## 完整构建顺序

1. 拉取 `klzgrad/forwardproxy:naive` 指定 Commit。
2. 复制 `udp_over_http.go` 与 `forwardproxy_udp_test.go` 到临时 forwardproxy 源码目录。
3. 执行 `scripts/apply-forwardproxy-udp.py`。
4. 读取 klzgrad 的 `CADDY_VERSION`。
5. 拉取对应版本 Caddy。
6. 应用 klzgrad 自带的 `caddy-sing-quic-bbrv1.patch`。
7. 应用 `scripts/apply-caddy-http3-datagrams.py`，启用 H3 Datagram 和双层 QUIC 包容量适配。
8. 用 patched Caddy + SagerNet QUIC 更新临时 `go.mod` 并执行 `go test ./...`。
9. 用 xcaddy 将修改后的 forwardproxy 和其他插件一起编入 Caddy。
10. 对最终 amd64/arm64 二进制逐项检查插件依赖和 UDP-over-HTTP 特征。
11. 用真实 UDP Echo 验证 H1/H2/H3，再使用官方 NaiveProxy 验证单层及双层 QUIC 代理。
12. 全部通过后才允许主分支发布 Release。

## 最终产物验证

UDP-over-HTTP 本身不会注册成单独的 Caddy module，它是 `http.handlers.forward_proxy` 内部能力。

因此不能只通过：

```text
caddy list-modules
```

判断 UDP overlay 是否存在。

工作流现在同时检查最终二进制中的以下特征：

```text
udp_uri_template
connect-udp
Connect-Udp-Bind
.well-known/masque/udp/
connect-udp-bind over http3 is not supported yet
```

并且要求 `github.com/caddyserver/forwardproxy` 以及所有自定义插件都存在于最终 Go build info。

amd64 还会实际运行 `caddy list-modules`，对 Caddy module 做第二层校验；arm64 因 x86_64 GitHub Runner 无法直接执行 ARM64 ELF，使用静态 build info 校验。

## 维护原则

- **klzgrad 是主线，UDP 是增量。**
- 不为了让 overlay “永远能打上”而放宽锚点检查。
- 不把 klzgrad 的完整 forwardproxy 长期复制进本仓库。
- 上游冲突时优先人工 review，而不是自动退回旧 Commit。
- UDP 协议代码发生变化时必须同步更新回归测试和最终二进制特征校验。
