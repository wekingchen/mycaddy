# forwardproxy UDP-over-HTTP overlay

本目录只维护 **相对于 `klzgrad/forwardproxy:naive` 的增量能力**，不复制或长期维护 klzgrad 的完整源码。

构建流程：

1. 拉取 `klzgrad/forwardproxy:naive` 最新提交。
2. 复制 `udp_over_http.go` 与 `forwardproxy_udp_test.go` 到上游源码目录。
3. 运行 `scripts/apply-forwardproxy-udp.py`，只修改 `Handler`、`Provision`、`ServeHTTP` 三个接入点。
4. 将 UDP-over-HTTP 所需依赖加入临时 `go.mod`。
5. 读取上游 `CADDY_VERSION`，按 klzgrad 自己的方式给 Caddy 应用 `caddy-sing-quic-bbrv1.patch`。
6. 运行 UDP 回归测试并用 xcaddy 编译。

## 设计目标

- klzgrad 是主线；每天直接跟进其 `naive` 分支。
- UDP-over-HTTP 是本仓库的 overlay，不反向覆盖上游的 Naive padding、Caddy 版本选择或 sing-quic BBRv1。
- 上游结构变化导致三个接入锚点失效时，构建必须明确失败，禁止静默退回旧源码。
- UDP 实现来源固定记录为 `imgk/forwardproxy` commit `6d1c91becd7d530a7cca2c26d3fd8bc70035d8ec`，后续如同步 imgk 新实现，应单独审查后更新 overlay。

## QUIC 适配

imgk 原实现使用 `github.com/quic-go/quic-go`。klzgrad 当前 Caddy BBRv1 补丁使用 `github.com/sagernet/quic-go`，因此 overlay 已统一改为 SagerNet QUIC 类型，避免同一二进制内混用两套 HTTP/3 Stream 类型。
