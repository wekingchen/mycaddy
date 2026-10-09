#!/usr/bin/env bash
# 使用真实官方 NaiveProxy 验证双层 QUIC CONNECT-UDP，必须能往返真实 UDP 数据。
set -euo pipefail
[[ $# == 2 ]] || { echo "usage: $0 <caddy> <naive>" >&2; exit 2; }
CADDY="$(realpath "$1")"
NAIVE="$(realpath "$2")"
TMP="$(mktemp -d -t mycaddy-naive-chain-XXXXXXXX)"
CADDY_PID='' INNER_PID='' NAIVE_PID='' ORIGIN_PID=''
cleanup() {
  local code=$?
  trap - EXIT
  for pid in "$NAIVE_PID" "$CADDY_PID" "$INNER_PID" "$ORIGIN_PID"; do
    [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
  done
  sleep 0.2
  for pid in "$NAIVE_PID" "$CADDY_PID" "$INNER_PID" "$ORIGIN_PID"; do
    if [[ -n "$pid" ]]; then
      kill -KILL "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  if [[ "$code" != 0 ]]; then
    echo '--- NaiveProxy ---' >&2
    tail -n 80 "$TMP/naive.log" >&2 || true
    echo '--- Caddy ---' >&2
    tail -n 120 "$TMP/caddy.log" >&2 || true
    echo '--- Inner Caddy ---' >&2
    tail -n 120 "$TMP/inner-caddy.log" >&2 || true
    echo '--- 外层访问日志 ---' >&2
    tail -n 20 "$TMP/outer-access.log" >&2 || true
  fi
  rm -rf "$TMP"
  exit "$code"
}
trap cleanup EXIT

echo '127.0.0.1 outer.test inner.test' | sudo tee -a /etc/hosts >/dev/null
openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 1 \
  -keyout "$TMP/tls.key" -out "$TMP/tls.crt" -subj '/CN=outer.test' \
  -addext 'subjectAltName=DNS:outer.test,DNS:inner.test,IP:127.0.0.1' >/dev/null 2>&1
mkdir -p "$TMP/origin" "$TMP/data" "$TMP/config"
printf 'official-naive-quic-chain-e2e-ok\n' > "$TMP/origin/marker.txt"
python3 -m http.server 18081 --bind 127.0.0.1 --directory "$TMP/origin" >"$TMP/origin.log" 2>&1 &
ORIGIN_PID=$!

cat > "$TMP/outer.Caddyfile" <<CADDYFILE
{
    debug
    admin off
    auto_https disable_redirects
    servers {
        protocols h1 h2 h3
    }
}
:19443, outer.test:19443 {
    tls $TMP/tls.crt $TMP/tls.key
    log {
        output file $TMP/outer-access.log
        format json
    }
    @naive_preamble {
        method GET
        path /
    }
    respond @naive_preamble 204
    forward_proxy {
        ports 19444
        acl {
            allow 127.0.0.1/32
        }
    }
}
CADDYFILE
cat > "$TMP/inner.Caddyfile" <<CADDYFILE
{
    debug
    admin off
    auto_https disable_redirects
    servers {
        protocols h1 h2 h3
    }
}
:19444, inner.test:19444 {
    tls $TMP/tls.crt $TMP/tls.key
    @naive_preamble {
        method GET
        path /
    }
    respond @naive_preamble 204
    forward_proxy {
        ports 18081
        acl {
            allow 127.0.0.1/32
        }
    }
}
CADDYFILE

GODEBUG=http2xconnect=1 XDG_DATA_HOME="$TMP/inner-data" XDG_CONFIG_HOME="$TMP/inner-config" \
  "$CADDY" run --config "$TMP/inner.Caddyfile" --adapter caddyfile >"$TMP/inner-caddy.log" 2>&1 &
INNER_PID=$!
MYCADDY_QUIC_INITIAL_PACKET_SIZE=1452 GODEBUG=http2xconnect=1 \
  XDG_DATA_HOME="$TMP/data" XDG_CONFIG_HOME="$TMP/config" \
  "$CADDY" run --config "$TMP/outer.Caddyfile" --adapter caddyfile >"$TMP/caddy.log" 2>&1 &
CADDY_PID=$!
python3 - <<'PY'
import socket, time
for port in [18081, 19443, 19444]:
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", port), .4):
                break
        except OSError:
            time.sleep(.1)
    else:
        raise SystemExit(f"Test service port {port} never became ready")
PY
cat > "$TMP/naive.json" <<'NAIVE'
{
  "listen": "socks://127.0.0.1:11080",
  "proxy": "quic://outer.test:19443,quic://inner.test:19444",
  "host-resolver-rules": "MAP outer.test 127.0.0.1, MAP inner.test 127.0.0.1",
  "log": ""
}
NAIVE
SSL_CERT_FILE="$TMP/tls.crt" "$NAIVE" "$TMP/naive.json" >"$TMP/naive.log" 2>&1 &
NAIVE_PID=$!
python3 - <<'PY'
import socket, time
for _ in range(100):
    try:
        with socket.create_connection(("127.0.0.1", 11080), .2):
            break
    except OSError:
        time.sleep(.1)
else:
    raise SystemExit("Official NaiveProxy SOCKS listener never became ready")
PY
body="$(curl --noproxy '' --socks5-hostname 127.0.0.1:11080 \
  --fail --silent --show-error --max-time 22 \
  http://127.0.0.1:18081/marker.txt)"
[[ "$body" == 'official-naive-quic-chain-e2e-ok' ]] || {
  echo "官方 NaiveProxy 双层链路回包不匹配：$body" >&2
  exit 1
}
for marker in \
  'H3 CONNECT-UDP datagram bridge started' \
  'H3 CONNECT-UDP UDP target packet sent' \
  'H3 CONNECT-UDP UDP target packet received' \
  'H3 CONNECT-UDP datagram returned'; do
  grep -Fq "$marker" "$TMP/caddy.log" || {
    echo "UDP 真实流量缺失：$marker" >&2
    exit 1
  }
done
if grep -Fq 'DATAGRAM frame too large' "$TMP/caddy.log"; then
  echo 'UDP Datagram 大包被拒绝，MTU 适配未通过' >&2
  exit 1
fi
if grep -Eq 'Preamble error: ERR_QUIC_PROTOCOL_ERROR|Preamble error: ERR_QUIC_HANDSHAKE_FAILED' "$TMP/naive.log"; then
  echo '官方 NaiveProxy 内层 QUIC 首次握手失败' >&2
  exit 1
fi
echo 'NAIVE_OFFICIAL_CLIENT_CONNECT_UDP_CHAIN=PASS'
echo "UDP_SENT_PACKETS=$(grep -Fc 'H3 CONNECT-UDP UDP target packet sent' "$TMP/caddy.log")"
echo "UDP_RETURNED_PACKETS=$(grep -Fc 'H3 CONNECT-UDP UDP target packet received' "$TMP/caddy.log")"
