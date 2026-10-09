#!/usr/bin/env python3
"""Real UDP-over-HTTP end-to-end test against a built Caddy binary.

Path under test:
client -> HTTP/1.1 Upgrade(connect-udp) -> built Caddy -> UDP echo server -> Caddy -> client

This intentionally uses the final Caddy executable, not httptest or direct
forwardproxy function calls.
"""

from __future__ import annotations

import argparse
import socket
import subprocess
import tempfile
import threading
import time
from pathlib import Path

HTTP_HOST = "127.0.0.1"
HTTP_PORT = 18080
UDP_HOST = "127.0.0.1"
UDP_PORT = 19090
PAYLOAD = b"mycaddy-real-udp-e2e"


def udp_echo_server(result: dict[str, bytes | str], ready: threading.Event) -> None:
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.settimeout(15)
    try:
        sock.bind((UDP_HOST, UDP_PORT))
        ready.set()
        data, addr = sock.recvfrom(65535)
        result["rx"] = data
        sock.sendto(data, addr)
        result["tx"] = data
    except Exception as exc:  # pragma: no cover - surfaced by main
        result["error"] = repr(exc)
        ready.set()
    finally:
        sock.close()


def wait_for_tcp(proc: subprocess.Popen[str]) -> None:
    deadline = time.time() + 15
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f"Caddy exited early with code {proc.returncode}")
        try:
            with socket.create_connection((HTTP_HOST, HTTP_PORT), timeout=0.5):
                return
        except OSError:
            time.sleep(0.2)
    raise TimeoutError("Caddy did not start listening in time")


def run_client() -> tuple[str, bytes]:
    sock = socket.create_connection((HTTP_HOST, HTTP_PORT), timeout=10)
    sock.settimeout(10)
    try:
        path = f"/.well-known/masque/udp/{UDP_HOST}/{UDP_PORT}/"
        request = (
            f"GET {path} HTTP/1.1\r\n"
            f"Host: localhost:{HTTP_PORT}\r\n"
            "Connection: Upgrade\r\n"
            "Upgrade: connect-udp\r\n"
            "Capsule-Protocol: ?1\r\n"
            "\r\n"
        ).encode()
        sock.sendall(request)

        buffered = b""
        while b"\r\n\r\n" not in buffered:
            chunk = sock.recv(4096)
            if not chunk:
                raise RuntimeError("connection closed before HTTP response headers")
            buffered += chunk

        headers, buffered = buffered.split(b"\r\n\r\n", 1)
        status = headers.split(b"\r\n", 1)[0].decode("ascii", "replace")
        if " 101 " not in status:
            raise RuntimeError(f"expected HTTP 101, got {status}\n{headers.decode('latin1')}")

        # All varints below are < 64, so RFC 9000 QUIC varint encoding is 1 byte:
        # capsule type=0, capsule length=context-id byte + payload, context-id=0.
        frame = bytes([0, 1 + len(PAYLOAD), 0]) + PAYLOAD
        sock.sendall(frame)

        while len(buffered) < 3:
            buffered += sock.recv(4096)

        frame_type = buffered[0]
        frame_length = buffered[1]
        needed = 2 + frame_length
        while len(buffered) < needed:
            buffered += sock.recv(4096)

        body = buffered[2:needed]
        context_id = body[0]
        reply = body[1:]

        if frame_type != 0:
            raise RuntimeError(f"unexpected frame type: {frame_type}")
        if context_id != 0:
            raise RuntimeError(f"unexpected context id: {context_id}")
        if reply != PAYLOAD:
            raise RuntimeError(f"payload mismatch: sent={PAYLOAD!r}, got={reply!r}")

        return status, reply
    finally:
        sock.close()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("caddy", help="path to the built amd64 Caddy executable")
    args = parser.parse_args()

    caddy = Path(args.caddy).resolve()
    if not caddy.is_file():
        raise SystemExit(f"Caddy binary not found: {caddy}")

    echo_result: dict[str, bytes | str] = {}
    ready = threading.Event()
    echo_thread = threading.Thread(target=udp_echo_server, args=(echo_result, ready), daemon=True)
    echo_thread.start()
    if not ready.wait(5):
        raise SystemExit("UDP echo server did not start")
    if "error" in echo_result:
        raise SystemExit(f"UDP echo server startup failed: {echo_result['error']}")

    with tempfile.TemporaryDirectory(prefix="mycaddy-udp-e2e-") as tmp:
        tmpdir = Path(tmp)
        caddyfile = tmpdir / "Caddyfile"
        caddy_log = tmpdir / "caddy.log"
        caddyfile.write_text(
            f"""http://localhost:{HTTP_PORT} {{
    forward_proxy {{
        ports {UDP_PORT}
        acl {{
            allow 127.0.0.1/32
        }}
    }}
}}
""",
            encoding="utf-8",
        )

        with caddy_log.open("w+", encoding="utf-8") as log:
            proc = subprocess.Popen(
                [str(caddy), "run", "--config", str(caddyfile), "--adapter", "caddyfile"],
                stdout=log,
                stderr=subprocess.STDOUT,
                text=True,
            )
            try:
                wait_for_tcp(proc)
                status, reply = run_client()
            except Exception:
                log.flush()
                log.seek(0)
                print("--- Caddy log ---")
                print(log.read())
                raise
            finally:
                proc.terminate()
                try:
                    proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait(timeout=5)

    echo_thread.join(timeout=10)
    if echo_thread.is_alive():
        raise SystemExit("UDP echo server did not complete")
    if "error" in echo_result:
        raise SystemExit(f"UDP echo server failed: {echo_result['error']}")
    if echo_result.get("rx") != PAYLOAD or echo_result.get("tx") != PAYLOAD:
        raise SystemExit(f"UDP echo payload mismatch: {echo_result}")

    print(f"HTTP_STATUS={status}")
    print(f"UDP_ECHO_RX={echo_result['rx'].decode()}")
    print(f"UDP_ECHO_TX={echo_result['tx'].decode()}")
    print(f"UDP_REPLY={reply.decode()}")
    print("E2E_RESULT=PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
