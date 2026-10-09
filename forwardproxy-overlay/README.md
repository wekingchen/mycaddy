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
7. 用 patched Caddy + SagerNet QUIC 更新临时 `go.mod`。
8. 执行 `go test ./...`。
9. 用 xcaddy 将修改后的本地 forwardproxy 和其他插件一起编入最终 Caddy。
10. 对最终 amd64/arm64 二进制检查插件依赖和 UDP-over-HTTP 特征。
11. 全部通过后才允许主分支发布 Release。

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
