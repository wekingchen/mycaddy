# NaiveProxy Caddy 定制构建

[![GitHub Release](https://img.shields.io/github/v/release/wekingchen/mycaddy?style=flat-square&logo=github)](https://github.com/wekingchen/mycaddy/releases)
[![Build Status](https://img.shields.io/github/actions/workflow/status/wekingchen/mycaddy/build-caddy.yml?style=flat-square&logo=github-actions)](https://github.com/wekingchen/mycaddy/actions)

本项目用于自动构建一套面向 NaiveProxy 的定制 Caddy。

当前方案以 **`klzgrad/forwardproxy:naive` 为长期主线**，跟随其 Naive padding、Caddy 版本选择和 sing-quic BBRv1 改动；本仓库只额外维护一层 **UDP-over-HTTP overlay**，并继续集成原有 Caddy 扩展插件。

## 当前架构

```text
klzgrad/forwardproxy:naive
        │
        ├─ CADDY_VERSION
        ├─ Naive padding
        └─ caddy-sing-quic-bbrv1.patch
        │
        ▼
对应版本 Caddy + SagerNet quic-go / sing-quic BBRv1
        │
        ├─ 应用本仓库 UDP-over-HTTP overlay
        ├─ 启用 Caddy HTTP/3 Datagram 与嵌套 QUIC MTU 适配
        ├─ 运行 forwardproxy 回归测试
        ├─ 加入全部自定义 Caddy 插件
        └─ 构建 linux/amd64 + linux/arm64
```

这里刻意**不长期 fork klzgrad 的完整 forwardproxy 源码**。每次构建都重新拉取 `naive` 最新提交，再把本仓库维护的 UDP 增量叠加上去。

这样做的目的很简单：尽量紧跟 klzgrad 上游，同时把 UDP-over-HTTP 的维护范围限制在少量明确的增量代码里。

## UDP-over-HTTP

UDP-over-HTTP overlay 当前基于：

```text
imgk/forwardproxy
commit 6d1c91becd7d530a7cca2c26d3fd8bc70035d8ec
"add UDP in HTTP"
```

为兼容 klzgrad 当前的 Caddy BBRv1 方案，overlay 已从原版 `quic-go` 适配到 `github.com/sagernet/quic-go`。

当前支持情况：

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

注意：当 `forward_proxy` 配置了 `upstream` 时，当前 UDP-over-HTTP overlay 不接管该请求。

### HTTP/2 Extended CONNECT 的运行环境

Caddy 当前使用的 `golang.org/x/net/http2` 默认没有开启 RFC 8441 Extended CONNECT。需要在**启动 Caddy 之前**设置 `GODEBUG=http2xconnect=1`，否则普通 HTTP/2 CONNECT-UDP 请求可能被 HTTP/2 协议层拒绝：

```bash
GODEBUG=http2xconnect=1 ./caddy_amd64 run --config Caddyfile
```

systemd 部署时可以设置 `Environment=GODEBUG=http2xconnect=1`。这只用于启用 HTTP/2 Extended CONNECT；HTTP/1.1 和 HTTP/3 不依赖该开关。测试工作流对 H2 也显式设置此环境，避免把受限配置误判成协议代码故障。

### QUIC 的 MTU 限制与双层代理配置

NaiveProxy 的双层 QUIC 需要让**外层 HTTP/3 Datagram**承载**内层 QUIC 数据包**。把所有 Caddy 的 QUIC 初始包一起调大不是正确办法：内层也会发出更大的包，外层仍装不下。

因此本项目默认保留 Caddy 原本的 **1200 字节**，只允许在**外层代理的 Caddy 进程**显式设置 `MYCADDY_QUIC_INITIAL_PACKET_SIZE=1452`（面向常见 1500 MTU 网络）：

```bash
# 外层代理进程（负责 CONNECT-UDP 封装）
MYCADDY_QUIC_INITIAL_PACKET_SIZE=1452 ./caddy_amd64 run --config outer.Caddyfile

# 内层代理进程（保持默认 1200）
./caddy_amd64 run --config inner.Caddyfile
```

两层可以使用**同一份 Caddy 二进制**，但在同一主机测试时采用独立进程；通常实际部署时它们位于不同服务器。只有外层需要调整，内层不需要。该参数只接受 1200–1452 的整数值，非法数值会使启动失败，避免无意改变系统默认行为。

**注意：**低 MTU 网络（例如某些 VPN、移动网络）不一定能承载 1452 字节的外层 QUIC 包。此方案针对典型 1500 MTU 的链路，实际网络仍需验证，不承诺所有路径均无分片。官方 NaiveProxy E2E 会检查是否发生 `DATAGRAM frame too large` 和内层首次握手超时。

## 内置插件

以下插件均参与正式构建：

| 插件 | 对应能力 / 模块 |
| --- | --- |
| `klzgrad/forwardproxy:naive` + UDP overlay | NaiveProxy；`http.handlers.forward_proxy` |
| `caddyserver/jsonc-adapter` | JSONC 配置；`caddy.adapters.jsonc` |
| `mholt/caddy-l4` | L4 TCP/UDP；`caddy.listeners.layer4` / `layer4.*` |
| `caddy-dns/cloudflare` | Cloudflare DNS；`dns.providers.cloudflare` |
| `caddy-dns/tencentcloud` | 腾讯云 DNS；`dns.providers.tencentcloud` |
| `caddy-dns/duckdns` | DuckDNS；`dns.providers.duckdns` |
| `mholt/caddy-dynamicdns` | Dynamic DNS；`dynamic_dns` |
| `mholt/caddy-events-exec` | Caddy 事件执行命令；`events.handlers.exec` |
| `WeidiDeng/caddy-cloudflare-ip` | Cloudflare 可信来源 IP；`http.ip_sources.cloudflare` |
| `xcaddyplugins/caddy-trusted-cloudfront` | CloudFront 可信来源 IP；`http.ip_sources.cloudfront` |
| `mholt/caddy-webdav` | WebDAV；`http.handlers.webdav` |
| `imgk/caddy-trojan` | Trojan；`http.handlers.trojan` / `trojan.*` |

## 构建与更新机制

工作流：`.github/workflows/build-caddy.yml`

自动流程如下：

1. 每天 UTC 16:00（UTC+8 次日 00:00）检查 `klzgrad/forwardproxy:naive`。
2. 读取最新上游 Commit 和其 `CADDY_VERSION`。
3. 如果上游 Commit 与最新 Release 已记录的 Commit 相同，则定时任务跳过构建。
4. 如果上游更新，重新拉取最新源码。
5. 应用本仓库 UDP-over-HTTP overlay。
6. 拉取 klzgrad 指定版本的 Caddy，应用上游 `caddy-sing-quic-bbrv1.patch`，再启用 HTTP/3 Datagram 与嵌套 QUIC MTU 适配。
7. 在 patched Caddy + SagerNet QUIC 环境下运行 `go test ./...`。
8. 使用 xcaddy 加入全部插件，分别编译 `linux/amd64` 和 `linux/arm64`。
9. 对最终二进制做功能校验。
10. 用最终 amd64 Caddy 二进制独立验证 HTTP/1.1、HTTP/2、HTTP/3 三种 UDP Echo 往返，逐字节比较收发内容。
11. 用官方 NaiveProxy 客户端验证单层 H2、单层 H3 和双层 QUIC-over-CONNECT-UDP，检查真实 UDP 收发与大包异常。
12. 两个架构和全部 E2E 通过后才允许发布 Release。

手动运行时可使用 `force_build=true` 强制重新构建当前上游版本。

测试分支统一使用 `test/**` 命名；测试分支会执行完整构建和校验，但不会发布 Release。

## 产物校验

工作流不是只判断“Go 编译成功”。

每个最终二进制都会检查：

- 所有自定义插件是否出现在 Go build info 中；
- UDP-over-HTTP 的关键协议/配置特征是否实际存在于最终二进制；
- amd64 额外直接执行 `caddy list-modules`，逐项检查所有关键 Caddy module；
- arm64 因 GitHub Ubuntu Runner 为 x86_64，不能直接执行 ARM64 ELF，因此使用静态 build info 做跨架构校验。

UDP-over-HTTP 检查的关键特征包括：

```text
udp_uri_template
connect-udp
Connect-Udp-Bind
.well-known/masque/udp/
connect-udp-bind over http3 is not supported yet
```

这意味着 UDP overlay 如果没有真正进入最终二进制，构建会直接失败，不会继续发布 Release。

此外，amd64 会运行 `tests/udp-over-http-e2e.py`、`tests/udp-e2e-go/` 以及官方 NaiveProxy 的端到端测试。这些测试均启动**最终构建出来的 Caddy 可执行文件**和真实网络 socket，不调用 forwardproxy 内部函数：

```text
测试客户端
  │ HTTP/1.1 Upgrade: connect-udp
  ▼
最终 caddy_amd64
  │ UDP
  ▼
127.0.0.1 UDP Echo Server
  │ 原样回包
  ▼
最终 caddy_amd64 → 测试客户端
```

HTTP/1.1 测试必须收到 `101 Switching Protocols`；HTTP/2 必须完成 Extended CONNECT，HTTP/3 必须协商 HTTP Datagram。每一种协议都要求 UDP Echo Server 实际收到 payload，回包后客户端收到完全相同的内容。

官方 NaiveProxy 客户端使用固定版本 `v154.0.8037.49-4` 进行实际 HTTP/2、HTTP/3 单层代理测试和两跳 QUIC 代理测试。两跳测试要求：**外层 QUIC → HTTP/3 CONNECT-UDP → 内层 QUIC → TCP 目标服务器**，并检查 Datagram 收发和握手是否出现尺寸限制。

## 下载与使用

到 [Releases](https://github.com/wekingchen/mycaddy/releases) 下载对应架构：

- `caddy_amd64.tar.gz`：Linux x86_64 / amd64
- `caddy_arm64.tar.gz`：Linux aarch64 / arm64

以 amd64 为例：

```bash
tar -xzf caddy_amd64.tar.gz
chmod +x caddy_amd64
./caddy_amd64 version
./caddy_amd64 list-modules
```

压缩包中还包含 `build-info_amd64.txt` 或 `build-info_arm64.txt`，用于记录本次构建的 klzgrad Commit、Caddy 版本、UDP overlay 来源和 QUIC 栈。

> ARM64 产物是 Linux ARM64 可执行文件。不要在普通 x86_64 Linux Runner 上直接执行，否则会得到 `Exec format error`。

## Caddyfile 示例

klzgrad 当前 forwardproxy 的认证指令为 `basic_auth`，而不是旧文档里的 `basicauth`。

```caddy
:443 {
    forward_proxy {
        basic_auth user your-password
        hide_ip
        hide_via
        probe_resistance secret.example.com
    }
}
```

`probe_resistance` 需要与 `basic_auth` 配合使用。

更完整的 forwardproxy 参数请以上游 `klzgrad/forwardproxy:naive` 当前文档为准，因为这部分会随上游演进。

## 本地复现构建

自动工作流是本项目的**构建事实来源**。如果要本地复现，应按照与 Action 相同的顺序，而不是直接使用旧的 “clone imgk/udpinhttp + xcaddy” 命令。

核心步骤：

```bash
# 1. 拉取本项目
git clone https://github.com/wekingchen/mycaddy.git
cd mycaddy

# 2. 拉取 klzgrad naive
git clone --branch naive --depth 1 https://github.com/klzgrad/forwardproxy.git forwardproxy

# 3. 应用 UDP overlay
cp forwardproxy-overlay/udp_over_http.go forwardproxy/
cp forwardproxy-overlay/forwardproxy_udp_test.go forwardproxy/
python3 scripts/apply-forwardproxy-udp.py forwardproxy/forwardproxy.go

# 4. 读取 klzgrad 指定 Caddy 版本
CADDY_VERSION="$(tr -d '\r\n' < forwardproxy/CADDY_VERSION)"

# 5. 拉取对应 Caddy，并应用 klzgrad BBRv1 补丁
git clone --depth 1 --branch "$CADDY_VERSION" https://github.com/caddyserver/caddy.git caddy-bbr
git -C caddy-bbr apply "$PWD/forwardproxy/caddy-sing-quic-bbrv1.patch"
python3 scripts/apply-caddy-http3-datagrams.py caddy-bbr

# 6. 用最终依赖环境测试 forwardproxy
cd forwardproxy
go mod edit -replace=github.com/caddyserver/caddy/v2=../caddy-bbr
go mod edit -require=github.com/dunglas/httpsfv@v1.1.0
go mod edit -require=github.com/sagernet/quic-go@v0.61.0-sing-box-mod.9
go mod tidy
go test ./...
cd ..

# 7. 安装 xcaddy
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
```

最终 xcaddy 插件参数请直接参考 `.github/workflows/build-caddy.yml`，避免 README 与实际构建参数长期出现两份不同的“真相”。

## 上游兼容策略

`scripts/apply-forwardproxy-udp.py` 只允许在几个明确的 forwardproxy 接入点修改上游代码。

如果 klzgrad 将来重构这些位置，脚本会因为找不到唯一锚点而主动失败。此时应重新审查 UDP overlay 与新上游的兼容性，而不是静默继续使用旧版 forwardproxy。

这种失败是有意设计的保护机制。

## 当前验证状态

截至 2026-10-09，已直接运行最终编译出来的 amd64 Caddy，可复查的证据包括：

- [候选构建 #37876720043](https://github.com/wekingchen/mycaddy/actions/runs/37876720043)：amd64、arm64 编译及现有模块/回归校验通过。
- [三协议及官方客户端验收 #37877756536](https://github.com/wekingchen/mycaddy/actions/runs/37877756536)：HTTP/1.1、HTTP/2、HTTP/3 的真实 UDP Echo 均通过；官方 NaiveProxy 单层 H2/H3 及双层 QUIC-over-CONNECT-UDP 验证通过。
- [双层 QUIC 诊断 #37876818236](https://github.com/wekingchen/mycaddy/actions/runs/37876818236)：外层较大 QUIC 包、内层默认 1200 字节的独立 Caddy 进程建立连接成功，检查到真实 UDP 双向转发，未见 `DATAGRAM frame too large` 或首次 QUIC 握手超时。

正式构建的 amd64 Job 现在也直接运行官方 NaiveProxy 的上述场景；只要失败，Release Job 就不得发布。arm64 通过实际交叉编译和静态插件校验，**没有宣称在 ARM64 真机上运行过 E2E**。

另有 [发布后二次验收工作流](https://github.com/wekingchen/mycaddy/actions/workflows/release-udp-e2e.yml) 检验 Release 附件本身，不是仅检查开发分支代码。

后续具体发布日期、版本与构建结果，请查看 [Releases](https://github.com/wekingchen/mycaddy/releases) 和对应工作流。