#!/usr/bin/env python3
"""Enable HTTP/3 Datagrams in the Caddy source used by mycaddy.

Caddy creates the QUIC listener itself, so RFC 9297 / RFC 9298 needs both:
1. QUIC transport datagrams (quic.Config.EnableDatagrams)
2. HTTP/3 datagram SETTINGS (http3.Server.EnableDatagrams)\n3. Per-process QUIC packet sizing for nested QUIC: the default stays 1200
   bytes. An explicitly configured outer MASQUE proxy can set
   MYCADDY_QUIC_INITIAL_PACKET_SIZE=1452 (only on common 1500-MTU paths).
   Inner servers must keep smaller packets; raising both sides globally
   makes nesting impossible for large QUIC Initial messages.

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
        """\t\tearlyLn, err := tr.ListenEarly(
\t\t\thttp3.ConfigureTLSConfig(quicTlsConfig),
\t\t\t&quic.Config{
\t\t\t\tInitialPacketSize: 1200,
\t\t\t\tAllow0RTT:         allow0rtt,
\t\t\t\tTracer:            h3qlog.DefaultConnectionTracer,
\t\t\t},""",
        """\t\t// Preserve Caddy's conservative default for low-MTU networks and inner QUIC
\t\t// servers. Only an explicitly configured outer MASQUE proxy needs larger
\t\t// packets to transport complete inner QUIC Initial packets in H3 Datagrams.
\t\tquicInitialPacketSize := uint16(1200)
\t\tif raw := os.Getenv("MYCADDY_QUIC_INITIAL_PACKET_SIZE"); raw != "" {
\t\t\tparsed, err := strconv.ParseUint(raw, 10, 16)
\t\t\tif err != nil || parsed < 1200 || parsed > 1452 {
\t\t\t\treturn nil, fmt.Errorf("MYCADDY_QUIC_INITIAL_PACKET_SIZE must be in [1200,1452] (got %q)", raw)
\t\t\t}
\t\t\tquicInitialPacketSize = uint16(parsed)
\t\t}
\t\tearlyLn, err := tr.ListenEarly(
\t\t\thttp3.ConfigureTLSConfig(quicTlsConfig),
\t\t\t&quic.Config{
\t\t\t\tInitialPacketSize: quicInitialPacketSize,
\t\t\t\tAllow0RTT:         allow0rtt,
\t\t\t\tEnableDatagrams:   true,
\t\t\t\tTracer:            h3qlog.DefaultConnectionTracer,
\t\t\t},""",
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

    print("Enabled HTTP/3 Datagrams and optional per-process QUIC packet size (default 1200)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
