#!/usr/bin/env python3
from pathlib import Path
import sys

if len(sys.argv) != 2:
    raise SystemExit("用法: apply-forwardproxy-udp.py <forwardproxy.go>")

path = Path(sys.argv[1])
text = path.read_text(encoding="utf-8")

def replace_once(old: str, new: str, label: str) -> None:
    global text
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"无法应用 UDP-over-HTTP 适配：{label} 锚点数量为 {count}，预期为 1。klzgrad 上游可能已变更，请人工检查。")
    text = text.replace(old, new, 1)

struct_anchor = '\tAuthCredentials [][]byte `json:"auth_credentials,omitempty"` // slice with base64-encoded credentials\n}'
struct_replacement = '''\tAuthCredentials [][]byte `json:"auth_credentials,omitempty"` // slice with base64-encoded credentials

\t// UDP-over-HTTP / CONNECT-UDP overlay.
\tudpProxyServer udpProxyServer
\t// Optional RFC 9298 URI template.
\tURITemplate string `json:"udp_uri_template,omitempty"`
}'''
replace_once(struct_anchor, struct_replacement, "Handler 结构")

provision_anchor = '''\treturn nil
}

func (h *Handler) ServeHTTP'''
provision_replacement = '''\tvar udpErr error
\th.udpProxyServer, udpErr = newUDPProxyServer(h.URITemplate, h.logger)
\tif udpErr != nil {
\t\treturn fmt.Errorf("create UDP proxy error: %w", udpErr)
\t}

\treturn nil
}

func (h *Handler) ServeHTTP'''
replace_once(provision_anchor, provision_replacement, "Provision 初始化")

serve_anchor = '''\tif r.Method == http.MethodConnect {'''
serve_replacement = '''\tisUDPoverHTTP, udpErr := h.tryUDPoverHTTP(w, r)
\tif isUDPoverHTTP {
\t\tif udpErr != nil {
\t\t\treturn fmt.Errorf("handle UDP over HTTP error: %w", udpErr)
\t\t}
\t\treturn nil
\t}

\tif r.Method == http.MethodConnect {'''
replace_once(serve_anchor, serve_replacement, "ServeHTTP 入口")

path.write_text(text, encoding="utf-8")
print("UDP-over-HTTP 适配已成功应用到", path)
