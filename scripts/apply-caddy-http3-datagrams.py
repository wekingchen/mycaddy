#!/usr/bin/env python3
"""Enable HTTP/3 Datagrams in the Caddy source used by mycaddy.

Caddy creates the QUIC listener itself, so RFC 9297 / RFC 9298 needs both:
1. QUIC transport datagrams (quic.Config.EnableDatagrams)
2. HTTP/3 datagram SETTINGS (http3.Server.EnableDatagrams)

The replacements are strict so an upstream layout change fails the build rather
than silently producing a Caddy that advertises UDP-over-HTTP but cannot use H3.
"""
from __future__ import annotations

import pathlib
import sys


def replace_once(path: pathlib.Path, old: str, new: str) -> None:
    text = path.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected exactly one anchor, found {count}")
    path.write_text(text.replace(old, new, 1))


def main() -> int:
    if len(sys.argv) != 2:
        raise SystemExit("usage: apply-caddy-http3-datagrams.py <caddy-source-dir>")
    root = pathlib.Path(sys.argv[1])

    listeners = root / "listeners.go"
    replace_once(
        listeners,
        """			&quic.Config{
				InitialPacketSize: 1200,
				Allow0RTT:         allow0rtt,
				Tracer:            h3qlog.DefaultConnectionTracer,
			},""",
        """			&quic.Config{
				InitialPacketSize: 1200,
				Allow0RTT:         allow0rtt,
				EnableDatagrams:   true,
				Tracer:            h3qlog.DefaultConnectionTracer,
			},""",
    )

    server = root / "modules" / "caddyhttp" / "server.go"
    replace_once(
        server,
        """		s.h3server = &http3.Server{
			Handler:        s,
			TLSConfig:      tlsCfg,
			MaxHeaderBytes: s.MaxHeaderBytes,""",
        """		s.h3server = &http3.Server{
			Handler:         s,
			TLSConfig:       tlsCfg,
			MaxHeaderBytes:  s.MaxHeaderBytes,
			EnableDatagrams: true,""",
    )

    print("Enabled QUIC + HTTP/3 Datagrams for CONNECT-UDP")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
