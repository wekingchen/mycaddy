#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <caddy> <naive> <h2|h3>" >&2
  exit 2
fi

CADDY="$(realpath "$1")"
NAIVE="$(realpath "$2")"
MODE="$3"

case "$MODE" in
  h2)
    CADDY_PORT=18542
    SOCKS_PORT=11082
    PROXY_SCHEME=https
    ;;
  h3)
    CADDY_PORT=18543
    SOCKS_PORT=11083
    PROXY_SCHEME=quic
    ;;
  *)
    echo "unknown mode: $MODE" >&2
    exit 2
    ;;
esac

TARGET_PORT=19110
TARGET_HOST="$(hostname -I | awk '{print $1}')"
test -n "$TARGET_HOST"

TMP="$(mktemp -d -t mycaddy-naive-e2e-XXXXXX)"
CADDY_PID=""
NAIVE_PID=""
ECHO_PID=""

cleanup() {
  set +e
  [[ -n "$NAIVE_PID" ]] && kill "$NAIVE_PID" 2>/dev/null
  [[ -n "$CADDY_PID" ]] && kill "$CADDY_PID" 2>/dev/null
  [[ -n "$ECHO_PID" ]] && kill "$ECHO_PID" 2>/dev/null
  [[ -n "$NAIVE_PID" ]] && wait "$NAIVE_PID" 2>/dev/null
  [[ -n "$CADDY_PID" ]] && wait "$CADDY_PID" 2>/dev/null
  [[ -n "$ECHO_PID" ]] && wait "$ECHO_PID" 2>/dev/null
  echo "--- Caddy $MODE log ---"
  cat "$TMP/caddy.log" 2>/dev/null || true
  echo "--- Official naive $MODE log ---"
  cat "$TMP/naive.log" 2>/dev/null || true
  echo "--- TCP echo $MODE log ---"
  cat "$TMP/echo.log" 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT

start_echo() {
  python3 - "$TARGET_PORT" >"$TMP/echo.log" 2>&1 <<'PY' &
import socket, sys
port = int(sys.argv[1])
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as srv:
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("0.0.0.0", port))
    srv.listen(1)
    print("ECHO_READY", flush=True)
    conn, addr = srv.accept()
    with conn:
        conn.settimeout(15)
        data = conn.recv(65535)
        print("ECHO_RX=" + data.decode("utf-8", "replace"), flush=True)
        conn.sendall(data)
        print("ECHO_TX=" + data.decode("utf-8", "replace"), flush=True)
PY
  ECHO_PID=$!

  for _ in $(seq 1 100); do
    grep -q '^ECHO_READY$' "$TMP/echo.log" 2>/dev/null && return 0
    kill -0 "$ECHO_PID" 2>/dev/null || {
      cat "$TMP/echo.log"
      return 1
    }
    sleep 0.1
  done
  echo "TCP echo server did not become ready" >&2
  return 1
}

mkdir -p "$TMP/data" "$TMP/config"

# First prove the runner can reach its own non-loopback target address.
start_echo
python3 - "$TARGET_HOST" "$TARGET_PORT" <<'PY'
import socket, sys
host = sys.argv[1]
port = int(sys.argv[2])
with socket.create_connection((host, port), timeout=3) as s:
    s.sendall(b"direct-target-smoke")
    got = s.recv(64)
    if got != b"direct-target-smoke":
        raise SystemExit(f"direct target echo mismatch: {got!r}")
print("DIRECT_TARGET_TCP=PASS")
PY
wait "$ECHO_PID"
ECHO_PID=""

# Start a fresh one-shot echo server for the actual Naive tunnel.
start_echo

cat >"$TMP/Caddyfile" <<EOF
{
    debug
    auto_https disable_redirects
}

:$CADDY_PORT, localhost:$CADDY_PORT {
    tls internal
    log

    @naive_preamble {
        method GET
        path /
    }
    respond @naive_preamble 204

    forward_proxy {
        ports $TARGET_PORT
        acl {
            allow $TARGET_HOST/32
        }
    }
}
EOF

GODEBUG=http2xconnect=1 \
XDG_DATA_HOME="$TMP/data" \
XDG_CONFIG_HOME="$TMP/config" \
"$CADDY" run --config "$TMP/Caddyfile" --adapter caddyfile >"$TMP/caddy.log" 2>&1 &
CADDY_PID=$!

python3 - "$CADDY_PORT" "$CADDY_PID" <<'PY'
import os, socket, ssl, sys, time
port = int(sys.argv[1])
pid = int(sys.argv[2])
ctx = ssl._create_unverified_context()
deadline = time.time() + 20
while time.time() < deadline:
    try:
        os.kill(pid, 0)
    except OSError:
        raise SystemExit("Caddy exited before TLS became ready")
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=.5) as raw:
            with ctx.wrap_socket(raw, server_hostname="localhost"):
                pass
        break
    except OSError:
        time.sleep(.2)
else:
    raise SystemExit("Caddy TLS did not become ready")
PY

ROOT_CERT="$TMP/data/caddy/pki/authorities/local/root.crt"
for _ in $(seq 1 100); do
  [[ -s "$ROOT_CERT" ]] && break
  sleep 0.1
done
test -s "$ROOT_CERT"

SSL_CERT_FILE="$ROOT_CERT" \
"$NAIVE" \
  --listen="socks://127.0.0.1:$SOCKS_PORT" \
  --proxy="$PROXY_SCHEME://localhost:$CADDY_PORT" \
  --log="$TMP/naive.log" &
NAIVE_PID=$!

python3 - "$SOCKS_PORT" "$TARGET_HOST" "$TARGET_PORT" "$MODE" "$NAIVE_PID" <<'PY'
import os, socket, struct, sys, time

socks_port = int(sys.argv[1])
target_host = sys.argv[2]
target_port = int(sys.argv[3])
mode = sys.argv[4]
pid = int(sys.argv[5])
payload = f"official-naive-{mode}-tcp-e2e".encode()

deadline = time.time() + 15
sock = None
while time.time() < deadline:
    try:
        os.kill(pid, 0)
    except OSError:
        raise SystemExit("naive exited before SOCKS listener became ready")
    try:
        sock = socket.create_connection(("127.0.0.1", socks_port), timeout=.5)
        break
    except OSError:
        time.sleep(.15)
if sock is None:
    raise SystemExit("naive SOCKS listener did not become ready")

with sock:
    sock.settimeout(15)
    sock.sendall(b"\x05\x01\x00")
    reply = sock.recv(2)
    if reply != b"\x05\x00":
        raise SystemExit(f"SOCKS greeting failed: {reply.hex()}")

    req = b"\x05\x01\x00\x01" + socket.inet_aton(target_host) + struct.pack("!H", target_port)
    sock.sendall(req)

    head = sock.recv(4)
    if len(head) != 4 or head[0] != 5 or head[1] != 0:
        raise SystemExit(f"SOCKS CONNECT failed: {head.hex()}")
    atyp = head[3]
    if atyp == 1:
        need = 6
    elif atyp == 4:
        need = 18
    elif atyp == 3:
        n = sock.recv(1)
        if len(n) != 1:
            raise SystemExit("SOCKS CONNECT truncated domain length")
        need = n[0] + 2
    else:
        raise SystemExit(f"SOCKS CONNECT bad ATYP: {atyp}")
    rest = b""
    while len(rest) < need:
        part = sock.recv(need - len(rest))
        if not part:
            raise SystemExit("SOCKS CONNECT truncated reply")
        rest += part

    sock.sendall(payload)
    got = b""
    while len(got) < len(payload):
        part = sock.recv(len(payload) - len(got))
        if not part:
            raise SystemExit("official naive tunnel closed before echo")
        got += part
    if got != payload:
        raise SystemExit(f"official naive payload mismatch: sent={payload!r} got={got!r}")

print(f"OFFICIAL_NAIVE_{mode.upper()}_TUNNEL=PASS")
print("OFFICIAL_NAIVE_PAYLOAD=" + payload.decode())
PY

wait "$ECHO_PID"
ECHO_PID=""
grep -q "ECHO_RX=official-naive-$MODE-tcp-e2e" "$TMP/echo.log"
grep -q "ECHO_TX=official-naive-$MODE-tcp-e2e" "$TMP/echo.log"
