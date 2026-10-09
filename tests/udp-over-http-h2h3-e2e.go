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
	"sync"
	"time"

	masque "github.com/quic-go/masque-go"
	"github.com/yosida95/uritemplate/v3"
	"golang.org/x/net/http2"
)

const (
	httpsPort = 18443
	udpPort   = 19090
)

var payload = []byte("mycaddy-real-udp-e2e-h2h3")

type echoServer struct {
	conn *net.UDPConn
	wg   sync.WaitGroup
	stop chan struct{}
}

func startEcho() (*echoServer, error) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("127.0.0.1:%d", udpPort))
	if err != nil { return nil, err }
	c, err := net.ListenUDP("udp", addr)
	if err != nil { return nil, err }
	s := &echoServer{conn:c, stop:make(chan struct{})}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		b := make([]byte, 65535)
		for {
			_ = c.SetReadDeadline(time.Now().Add(500*time.Millisecond))
			n, peer, err := c.ReadFromUDP(b)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					select { case <-s.stop: return; default: continue }
				}
				return
			}
			_, _ = c.WriteToUDP(b[:n], peer)
		}
	}()
	return s, nil
}

func (s *echoServer) close() {
	close(s.stop)
	_ = s.conn.Close()
	s.wg.Wait()
}

func frame(p []byte) []byte {
	if len(p)+1 >= 64 { panic("test payload too large for one-byte QUIC varint") }
	out := []byte{0, byte(len(p)+1), 0}
	return append(out, p...)
}

func readFrame(r io.Reader) ([]byte, error) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(r, h); err != nil { return nil, err }
	if h[0] != 0 { return nil, fmt.Errorf("unexpected capsule type %d", h[0]) }
	if h[1] < 1 || h[1] >= 64 { return nil, fmt.Errorf("unexpected capsule length %d", h[1]) }
	b := make([]byte, int(h[1]))
	if _, err := io.ReadFull(r, b); err != nil { return nil, err }
	if b[0] != 0 { return nil, fmt.Errorf("unexpected context id %d", b[0]) }
	return b[1:], nil
}

func waitTCP(proc *exec.Cmd) error {
	deadline := time.Now().Add(15*time.Second)
	for time.Now().Before(deadline) {
		if proc.ProcessState != nil && proc.ProcessState.Exited() {
			return fmt.Errorf("Caddy exited early")
		}
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", httpsPort), 300*time.Millisecond)
		if err == nil { c.Close(); time.Sleep(500*time.Millisecond); return nil }
		time.Sleep(200*time.Millisecond)
	}
	return fmt.Errorf("Caddy HTTPS listener did not become ready")
}

func testH2() error {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	url := fmt.Sprintf("https://localhost:%d/.well-known/masque/udp/127.0.0.1/%d/", httpsPort, udpPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodConnect, url, pr)
	if err != nil { return err }
	req.ContentLength = -1
	req.Header[":protocol"] = []string{"connect-udp"}
	req.Header.Set("Capsule-Protocol", "?1")

	tr := &http2.Transport{TLSClientConfig:&tls.Config{
		InsecureSkipVerify:true,
		NextProtos:[]string{"h2"},
	}}
	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := tr.RoundTrip(req)
		if err != nil { errCh <- err; return }
		respCh <- resp
	}()

	var resp *http.Response
	select {
	case err := <-errCh:
		return fmt.Errorf("HTTP/2 CONNECT-UDP handshake: %w", err)
	case resp = <-respCh:
	case <-ctx.Done():
		return fmt.Errorf("HTTP/2 CONNECT-UDP handshake timeout: %w", ctx.Err())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP/2 expected 200, got %s body=%q", resp.Status, b)
	}
	if _, err := pw.Write(frame(payload)); err != nil {
		return fmt.Errorf("HTTP/2 send UDP frame: %w", err)
	}
	got, err := readFrame(resp.Body)
	if err != nil { return fmt.Errorf("HTTP/2 receive UDP frame: %w", err) }
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("HTTP/2 payload mismatch: got %q want %q", got, payload)
	}
	fmt.Printf("HTTP2_UDP_REPLY=%s\nHTTP2_E2E_RESULT=PASS\n", got)
	return nil
}

func testH3() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	template := uritemplate.MustNew(fmt.Sprintf("https://localhost:%d/.well-known/masque/udp/{target_host}/{target_port}/", httpsPort))
	req, err := masque.NewRequest(ctx, template, fmt.Sprintf("127.0.0.1:%d", udpPort))
	if err != nil { return fmt.Errorf("HTTP/3 create CONNECT-UDP request: %w", err) }
	tr := masque.Transport{TLSClientConfig:&tls.Config{
		InsecureSkipVerify:true,
		NextProtos:[]string{"h3"},
	}}
	conn, resp, err := tr.Dial(req)
	if err != nil { return fmt.Errorf("HTTP/3 CONNECT-UDP dial: %w", err) }
	defer conn.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP/3 expected 200, got %s", resp.Status)
	}
	_ = conn.SetDeadline(time.Now().Add(10*time.Second))
	if _, err := conn.WriteTo(payload, nil); err != nil {
		return fmt.Errorf("HTTP/3 send UDP datagram: %w", err)
	}
	b := make([]byte, 2048)
	n, _, err := conn.ReadFrom(b)
	if err != nil { return fmt.Errorf("HTTP/3 receive UDP datagram: %w", err) }
	if !bytes.Equal(b[:n], payload) {
		return fmt.Errorf("HTTP/3 payload mismatch: got %q want %q", b[:n], payload)
	}
	fmt.Printf("HTTP3_UDP_REPLY=%s\nHTTP3_E2E_RESULT=PASS\n", b[:n])
	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: udp-h2h3-e2e /path/to/caddy")
		os.Exit(2)
	}
	caddy, err := filepath.Abs(os.Args[1])
	if err != nil { panic(err) }
	echo, err := startEcho()
	if err != nil { panic(err) }
	defer echo.close()

	tmp, err := os.MkdirTemp("", "mycaddy-h2h3-e2e-")
	if err != nil { panic(err) }
	defer os.RemoveAll(tmp)
	cfg := filepath.Join(tmp, "Caddyfile")
	logPath := filepath.Join(tmp, "caddy.log")
	config := fmt.Sprintf(`{
	admin off
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
	if err := os.WriteFile(cfg, []byte(config), 0600); err != nil { panic(err) }
	logf, err := os.Create(logPath)
	if err != nil { panic(err) }
	defer logf.Close()
	cmd := exec.Command(caddy, "run", "--config", cfg, "--adapter", "caddyfile")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+filepath.Join(tmp,"data"), "XDG_CONFIG_HOME="+filepath.Join(tmp,"config"))
	if err := cmd.Start(); err != nil { panic(err) }
	defer func(){
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func(){ _ = cmd.Wait(); close(done) }()
		select { case <-done: case <-time.After(3*time.Second): _ = cmd.Process.Kill() }
	}()
	if err := waitTCP(cmd); err != nil {
		logf.Sync(); b,_ := os.ReadFile(logPath); panic(fmt.Sprintf("%v\n--- Caddy log ---\n%s",err,b))
	}
	var failures []string
	if err := testH2(); err != nil {
		fmt.Printf("HTTP2_E2E_RESULT=FAIL: %v\n", err)
		failures = append(failures, "HTTP/2: "+err.Error())
	}
	if err := testH3(); err != nil {
		fmt.Printf("HTTP3_E2E_RESULT=FAIL: %v\n", err)
		failures = append(failures, "HTTP/3: "+err.Error())
	}
	if len(failures) > 0 {
		_ = logf.Sync()
		b, _ := os.ReadFile(logPath)
		fmt.Printf("--- Caddy log ---\n%s\n", b)
		for _, failure := range failures { fmt.Fprintln(os.Stderr, failure) }
		os.Exit(1)
	}
	fmt.Println("H2_H3_E2E_RESULT=PASS")
}
