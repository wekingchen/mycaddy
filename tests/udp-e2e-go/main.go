package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

var payload = []byte("mycaddy-release-real-udp-e2e")
var activeLogPath string

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "E2E_FAIL: "+format+"\n", args...)
	if activeLogPath != "" {
		if b, err := os.ReadFile(activeLogPath); err == nil {
			fmt.Fprintf(os.Stderr, "--- Caddy log ---\n%s\n", b)
		}
	}
	os.Exit(1)
}

func startUDPEcho(port int) (<-chan error, <-chan []byte) {
	errc := make(chan error, 1)
	rxc := make(chan []byte, 1)
	go func() {
		pc, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			errc <- err
			return
		}
		defer pc.Close()
		_ = pc.SetDeadline(time.Now().Add(20 * time.Second))
		buf := make([]byte, 65535)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			errc <- err
			return
		}
		got := append([]byte(nil), buf[:n]...)
		rxc <- got
		if _, err := pc.WriteTo(got, addr); err != nil {
			errc <- err
			return
		}
		errc <- nil
	}()
	return errc, rxc
}

func startCaddy(caddy, dir string, httpsPort, udpPort int) (*exec.Cmd, *os.File) {
	caddyfile := filepath.Join(dir, "Caddyfile")
	cfg := fmt.Sprintf(`{
    auto_https disable_redirects
}

https://localhost:%d {
    tls internal
    forward_proxy {
        ports %d
        acl {
            allow 127.0.0.1/32
        }
    }
}
`, httpsPort, udpPort)
	if err := os.WriteFile(caddyfile, []byte(cfg), 0600); err != nil {
		fail("write Caddyfile: %v", err)
	}
	logf, err := os.Create(filepath.Join(dir, "caddy.log"))
	if err != nil {
		fail("create Caddy log: %v", err)
	}
	cmd := exec.Command(caddy, "run", "--config", caddyfile, "--adapter", "caddyfile")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Env = append(os.Environ(), "GODEBUG=http2xconnect=1", "XDG_DATA_HOME="+filepath.Join(dir, "data"), "XDG_CONFIG_HOME="+filepath.Join(dir, "config"))
	if err := cmd.Start(); err != nil {
		fail("start Caddy: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			break
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", httpsPort), 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return cmd, logf
		}
		time.Sleep(150 * time.Millisecond)
	}
	stopCaddy(cmd, logf)
	dump, _ := os.ReadFile(filepath.Join(dir, "caddy.log"))
	fail("Caddy did not become ready:\n%s", dump)
	return nil, nil
}

func stopCaddy(cmd *exec.Cmd, logf *os.File) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	if logf != nil {
		_ = logf.Close()
	}
}

func capsule(p []byte) []byte {
	if len(p)+1 >= 64 {
		panic("payload too large for one-byte QUIC varint test frame")
	}
	return append([]byte{0, byte(len(p) + 1), 0}, p...)
}

func verifyEcho(errc <-chan error, rxc <-chan []byte) {
	select {
	case got := <-rxc:
		if !bytes.Equal(got, payload) {
			fail("UDP echo server received wrong payload: %q", got)
		}
	case <-time.After(10 * time.Second):
		fail("UDP echo server never received payload")
	}
	if err := <-errc; err != nil {
		fail("UDP echo server error: %v", err)
	}
}

func runH2(port int) {
	addr := fmt.Sprintf("localhost:%d", port)
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2"},
		ServerName:         "localhost",
	})
	if err != nil {
		fail("HTTP/2 TLS dial: %v", err)
	}
	defer conn.Close()
	if conn.ConnectionState().NegotiatedProtocol != "h2" {
		fail("HTTP/2 ALPN mismatch: %q", conn.ConnectionState().NegotiatedProtocol)
	}

	fr := http2.NewFramer(conn, conn)
	fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
		fail("write HTTP/2 client preface: %v", err)
	}
	if err := fr.WriteSettings(); err != nil {
		fail("write HTTP/2 settings: %v", err)
	}

	serverExtendedConnect := false
	for {
		f, err := fr.ReadFrame()
		if err != nil {
			fail("read HTTP/2 server settings: %v", err)
		}
		sf, ok := f.(*http2.SettingsFrame)
		if !ok {
			continue
		}
		if sf.IsAck() {
			continue
		}
		_ = sf.ForeachSetting(func(s http2.Setting) error {
			if s.ID == http2.SettingEnableConnectProtocol && s.Val == 1 {
				serverExtendedConnect = true
			}
			return nil
		})
		if err := fr.WriteSettingsAck(); err != nil {
			fail("ack HTTP/2 settings: %v", err)
		}
		break
	}
	if !serverExtendedConnect {
		fail("server did not advertise SETTINGS_ENABLE_CONNECT_PROTOCOL=1")
	}

	path := "/.well-known/masque/udp/127.0.0.1/19092/"
	var hb bytes.Buffer
	enc := hpack.NewEncoder(&hb)
	headers := []hpack.HeaderField{
		{Name: ":method", Value: "CONNECT"},
		{Name: ":scheme", Value: "https"},
		{Name: ":authority", Value: addr},
		{Name: ":path", Value: path},
		{Name: ":protocol", Value: "connect-udp"},
		{Name: "capsule-protocol", Value: "?1"},
	}
	for _, hf := range headers {
		if err := enc.WriteField(hf); err != nil {
			fail("encode HTTP/2 header %s: %v", hf.Name, err)
		}
	}
	if err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: hb.Bytes(), EndHeaders: true, EndStream: false}); err != nil {
		fail("write HTTP/2 CONNECT headers: %v", err)
	}

	status := ""
	for status == "" {
		f, err := fr.ReadFrame()
		if err != nil {
			fail("read HTTP/2 response headers: %v", err)
		}
		switch v := f.(type) {
		case *http2.MetaHeadersFrame:
			if v.StreamID == 1 {
				status = v.PseudoValue("status")
			}
		case *http2.SettingsFrame:
			if !v.IsAck() {
				_ = fr.WriteSettingsAck()
			}
		case *http2.GoAwayFrame:
			fail("HTTP/2 GOAWAY before response: code=%v debug=%q", v.ErrCode, v.DebugData())
		case *http2.RSTStreamFrame:
			if v.StreamID == 1 {
				fail("HTTP/2 stream reset before response: %v", v.ErrCode)
			}
		}
	}
	if status != "200" {
		fail("HTTP/2 CONNECT status=%s, want 200", status)
	}
	if err := fr.WriteData(1, false, capsule(payload)); err != nil {
		fail("write HTTP/2 UDP capsule: %v", err)
	}

	var raw []byte
	deadline := time.Now().Add(10 * time.Second)
	_ = conn.SetReadDeadline(deadline)
	for len(raw) < len(payload)+3 {
		f, err := fr.ReadFrame()
		if err != nil {
			fail("read HTTP/2 UDP capsule: %v", err)
		}
		switch v := f.(type) {
		case *http2.DataFrame:
			if v.StreamID == 1 {
				raw = append(raw, v.Data()...)
			}
		case *http2.RSTStreamFrame:
			if v.StreamID == 1 {
				fail("HTTP/2 stream reset during UDP exchange: %v", v.ErrCode)
			}
		case *http2.GoAwayFrame:
			fail("HTTP/2 GOAWAY during UDP exchange: code=%v debug=%q", v.ErrCode, v.DebugData())
		}
	}
	if raw[0] != 0 || int(raw[1]) != len(payload)+1 || raw[2] != 0 || !bytes.Equal(raw[3:3+len(payload)], payload) {
		fail("HTTP/2 UDP reply framing/payload mismatch: %x", raw)
	}
	fmt.Printf("HTTP2_STATUS=%s\nHTTP2_UDP_REPLY=%s\nHTTP2_E2E=PASS\n", status, payload)
}

func runH3(port int) {
	tr := &http3.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"},
		QUICConfig:      &quic.Config{EnableDatagrams: true},
		EnableDatagrams: true,
	}
	defer tr.Close()

	url := fmt.Sprintf("https://localhost:%d/.well-known/masque/udp/127.0.0.1/19093/", port)
	req, err := http.NewRequest(http.MethodConnect, url, nil)
	if err != nil {
		fail("create HTTP/3 CONNECT request: %v", err)
	}
	req.Proto = "connect-udp"
	req.Header.Set("Capsule-Protocol", "?1")

	resp, err := tr.RoundTrip(req)
	if err != nil {
		fail("HTTP/3 CONNECT round trip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		fail("HTTP/3 CONNECT status=%d body=%q", resp.StatusCode, body)
	}
	hs, ok := resp.Body.(http3.HTTPStreamer)
	if !ok {
		fail("HTTP/3 response body doesn't implement HTTPStreamer")
	}
	stream := hs.HTTPStream()
	if err := stream.SendDatagram(append([]byte{0}, payload...)); err != nil {
		fail("HTTP/3 SendDatagram: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := stream.ReceiveDatagram(ctx)
	if err != nil {
		fail("HTTP/3 ReceiveDatagram: %v", err)
	}
	if len(got) < 1 || got[0] != 0 || !bytes.Equal(got[1:], payload) {
		fail("HTTP/3 UDP reply mismatch: %x", got)
	}
	fmt.Printf("HTTP3_STATUS=%d\nHTTP3_UDP_REPLY=%s\nHTTP3_E2E=PASS\n", resp.StatusCode, payload)
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <caddy> <h2|h3>\n", os.Args[0])
		os.Exit(2)
	}
	caddy, err := filepath.Abs(os.Args[1])
	if err != nil {
		fail("resolve Caddy path: %v", err)
	}
	proto := strings.ToLower(os.Args[2])
	var httpsPort, udpPort int
	switch proto {
	case "h2":
		httpsPort, udpPort = 18443, 19092
	case "h3":
		httpsPort, udpPort = 18444, 19093
	default:
		fail("unknown protocol %q", proto)
	}
	if _, err := os.Stat(caddy); err != nil {
		fail("Caddy not found: %v", err)
	}
	errc, rxc := startUDPEcho(udpPort)
	dir, err := os.MkdirTemp("", "mycaddy-"+proto+"-e2e-")
	if err != nil {
		fail("temp dir: %v", err)
	}
	defer os.RemoveAll(dir)
	cmd, logf := startCaddy(caddy, dir, httpsPort, udpPort)
	activeLogPath = filepath.Join(dir, "caddy.log")
	defer func() {
		stopCaddy(cmd, logf)
		if b, err := os.ReadFile(filepath.Join(dir, "caddy.log")); err == nil {
			fmt.Printf("--- Caddy %s log ---\n%s\n", proto, b)
		}
	}()

	if proto == "h2" {
		runH2(httpsPort)
	} else {
		runH3(httpsPort)
	}
	verifyEcho(errc, rxc)
	fmt.Printf("UDP_ECHO_%s=PASS target=127.0.0.1:%s\n", strings.ToUpper(proto), strconv.Itoa(udpPort))
}
