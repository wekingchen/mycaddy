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
6. 拉取 klzgrad 指定版本的 Caddy，并应用上游 `caddy-sing-quic-bbrv1.patch`。
7. 在 patched Caddy + SagerNet QUIC 环境下运行 `go test ./...`。
8. 使用 xcaddy 加入全部插件，分别编译 `linux/amd64` 和 `linux/arm64`。
9. 对最终二进制做功能校验。
10. 两个架构全部成功后才发布 Release。

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

截至 2026-10-09，正式工作流 #704 已验证：

- klzgrad `naive` Commit：`c096d6a00cb28e019cc1995b04bdc6a9311d8024`
- Caddy：`v2.11.7`
- amd64：回归测试、编译、模块校验、Artifact、Release 均成功
- arm64：回归测试、编译、静态模块校验、Artifact、Release 均成功
- 正式 Release：`v2.11.7-20261009-002817`

后续版本以 Releases 和对应构建记录为准。
