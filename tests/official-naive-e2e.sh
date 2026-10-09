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
TMP="$(mktemp -d -t mycaddy-naive-e2e-XXXXXX)"
CADDY_PID=""
NAIVE_PID=""
HTTP_PID=""

cleanup() {
  set +e
  [[ -n "$NAIVE_PID" ]] && kill "$NAIVE_PID" 2>/dev/null
  [[ -n "$CADDY_PID" ]] && kill "$CADDY_PID" 2>/dev/null
  [[ -n "$HTTP_PID" ]] && kill "$HTTP_PID" 2>/dev/null
  [[ -n "$NAIVE_PID" ]] && wait "$NAIVE_PID" 2>/dev/null
  [[ -n "$CADDY_PID" ]] && wait "$CADDY_PID" 2>/dev/null
  [[ -n "$HTTP_PID" ]] && wait "$HTTP_PID" 2>/dev/null
  if [[ -f "$TMP/caddy.log" ]]; then
    echo "--- Caddy $MODE log ---"
    cat "$TMP/caddy.log"
  fi
  if [[ -f "$TMP/naive.log" ]]; then
    echo "--- Official naive $MODE log ---"
    cat "$TMP/naive.log"
  fi
  rm -rf "$TMP"
}
trap cleanup EXIT

mkdir -p "$TMP/www" "$TMP/data" "$TMP/config"
printf 'official-naive-%s-e2e\n' "$MODE" > "$TMP/www/index.html"
python3 -m http.server "$TARGET_PORT" --bind 127.0.0.1 --directory "$TMP/www" >"$TMP/http.log" 2>&1 &
HTTP_PID=$!

cat >"$TMP/Caddyfile" <<EOF
{
    auto_https disable_redirects
}

https://localhost:$CADDY_PORT {
    tls internal
    forward_proxy {
        ports $TARGET_PORT
        acl {
            allow 127.0.0.1/32
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

python3 - "$SOCKS_PORT" "$NAIVE_PID" <<'PY'
import os, socket, sys, time
port = int(sys.argv[1])
pid = int(sys.argv[2])
deadline = time.time() + 15
while time.time() < deadline:
    try:
        os.kill(pid, 0)
    except OSError:
        raise SystemExit("naive exited before SOCKS listener became ready")
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=.3):
            break
    except OSError:
        time.sleep(.15)
else:
    raise SystemExit("naive SOCKS listener did not become ready")
PY

RESULT="$(curl --fail --silent --show-error --max-time 10 \
  --socks5-hostname "127.0.0.1:$SOCKS_PORT" \
  "http://127.0.0.1:$TARGET_PORT/")"

EXPECTED="official-naive-$MODE-e2e"
if [[ "$RESULT" != "$EXPECTED" ]]; then
  echo "official naive payload mismatch: expected=$EXPECTED got=$RESULT" >&2
  exit 1
fi

MODE_UPPER="$(printf '%s' "$MODE" | tr '[:lower:]' '[:upper:]')"
echo "OFFICIAL_NAIVE_"$MODE_UPPER"_TUNNEL=PASS"
echo "OFFICIAL_NAIVE_"$MODE_UPPER"_PAYLOAD=$RESULT"
