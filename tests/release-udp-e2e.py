#!/usr/bin/env python3
from __future__ import annotations

import argparse
import socket
import ssl
import time

PAYLOADS = {
    "h1": b"mycaddy-release-e2e-h1",
    "h2": b"mycaddy-release-e2e-h2",
}

def capsule(payload: bytes) -> bytes:
    if 1 + len(payload) >= 64:
        raise ValueError("payload too large for one-byte QUIC varint test framing")
    return bytes([0, 1 + len(payload), 0]) + payload

def parse_capsule(buf: bytes, payload: bytes) -> None:
    if len(buf) < 3:
        raise RuntimeError(f"capsule too short: {buf!r}")
    if buf[0] != 0:
        raise RuntimeError(f"unexpected capsule type: {buf[0]}")
    ln = buf[1]
    if len(buf) < 2 + ln:
        raise RuntimeError(f"truncated capsule: want {2+ln}, got {len(buf)}")
    body = buf[2:2+ln]
    if not body or body[0] != 0:
        raise RuntimeError(f"unexpected context id: {body[:1]!r}")
    reply = body[1:]
    if reply != payload:
        raise RuntimeError(f"UDP payload mismatch: sent={payload!r} got={reply!r}")

def h1(host: str, port: int, udp_host: str, udp_port: int) -> None:
    payload = PAYLOADS["h1"]
    s = socket.create_connection((host, port), timeout=10)
    s.settimeout(10)
    try:
        path = f"/.well-known/masque/udp/{udp_host}/{udp_port}/"
        req = (
            f"GET {path} HTTP/1.1\r\n"
            f"Host: {host}:{port}\r\n"
            "Connection: Upgrade\r\n"
            "Upgrade: connect-udp\r\n"
            "Capsule-Protocol: ?1\r\n"
            "\r\n"
        ).encode()
        s.sendall(req)
        buf = b""
        while b"\r\n\r\n" not in buf:
            part = s.recv(4096)
            if not part:
                raise RuntimeError("connection closed before HTTP/1.1 response headers")
            buf += part
        headers, buf = buf.split(b"\r\n\r\n", 1)
        status = headers.split(b"\r\n", 1)[0].decode("ascii", "replace")
        if " 101 " not in status:
            raise RuntimeError(f"expected HTTP 101, got {status}\n{headers.decode('latin1')}")
        s.sendall(capsule(payload))
        while len(buf) < 2:
            buf += s.recv(4096)
        total = 2 + buf[1]
        while len(buf) < total:
            buf += s.recv(4096)
        parse_capsule(buf[:total], payload)
        print("H1_UDP_E2E=PASS")
    finally:
        s.close()

def h2(host: str, port: int, udp_host: str, udp_port: int) -> None:
    from h2.config import H2Configuration
    from h2.connection import H2Connection
    from h2.events import DataReceived, ResponseReceived, StreamEnded, StreamReset

    payload = PAYLOADS["h2"]
    raw = socket.create_connection((host, port), timeout=10)
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    ctx.set_alpn_protocols(["h2"])
    s = ctx.wrap_socket(raw, server_hostname=host)
    s.settimeout(10)
    if s.selected_alpn_protocol() != "h2":
        raise RuntimeError(f"ALPN did not negotiate h2: {s.selected_alpn_protocol()!r}")

    conn = H2Connection(config=H2Configuration(client_side=True, header_encoding="utf-8"))
    conn.initiate_connection()
    s.sendall(conn.data_to_send())
    stream_id = conn.get_next_available_stream_id()
    path = f"/.well-known/masque/udp/{udp_host}/{udp_port}/"
    headers = [
        (":method", "CONNECT"),
        (":scheme", "https"),
        (":authority", f"{host}:{port}"),
        (":path", path),
        (":protocol", "connect-udp"),
        ("capsule-protocol", "?1"),
    ]
    conn.send_headers(stream_id, headers, end_stream=False)
    s.sendall(conn.data_to_send())

    got_200 = False
    response_data = bytearray()
    deadline = time.time() + 10
    sent = False
    try:
        while time.time() < deadline:
            data = s.recv(65535)
            if not data:
                raise RuntimeError("HTTP/2 connection closed")
            for ev in conn.receive_data(data):
                if isinstance(ev, ResponseReceived) and ev.stream_id == stream_id:
                    status = dict(ev.headers).get(":status")
                    if status != "200":
                        raise RuntimeError(f"expected HTTP/2 200, got {status}, headers={ev.headers}")
                    got_200 = True
                    if not sent:
                        conn.send_data(stream_id, capsule(payload), end_stream=False)
                        sent = True
                elif isinstance(ev, DataReceived) and ev.stream_id == stream_id:
                    response_data.extend(ev.data)
                    conn.acknowledge_received_data(ev.flow_controlled_length, stream_id)
                    if len(response_data) >= 2:
                        total = 2 + response_data[1]
                        if len(response_data) >= total:
                            parse_capsule(bytes(response_data[:total]), payload)
                            print("H2_UDP_E2E=PASS")
                            return
                elif isinstance(ev, StreamReset) and ev.stream_id == stream_id:
                    raise RuntimeError(f"HTTP/2 stream reset: {ev.error_code}")
                elif isinstance(ev, StreamEnded) and ev.stream_id == stream_id:
                    if not response_data:
                        raise RuntimeError("HTTP/2 stream ended before UDP echo")
            out = conn.data_to_send()
            if out:
                s.sendall(out)
        raise TimeoutError(f"timed out waiting for HTTP/2 UDP echo; got_200={got_200}, data={bytes(response_data)!r}")
    finally:
        s.close()

def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--protocol", choices=["h1", "h2"], required=True)
    p.add_argument("--host", default="127.0.0.1")
    p.add_argument("--port", type=int, required=True)
    p.add_argument("--udp-host", default="127.0.0.1")
    p.add_argument("--udp-port", type=int, default=19090)
    a = p.parse_args()
    if a.protocol == "h1":
        h1(a.host, a.port, a.udp_host, a.udp_port)
    else:
        h2(a.host, a.port, a.udp_host, a.udp_port)
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
