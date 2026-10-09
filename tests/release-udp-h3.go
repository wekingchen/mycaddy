package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/quic-go/masque-go"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/yosida95/uritemplate/v3"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatalf("usage: %s <proxy-host:port> <udp-target-host:port>", os.Args[0])
	}
	proxy := os.Args[1]
	target := os.Args[2]

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tpl := uritemplate.MustNew(fmt.Sprintf("https://%s/.well-known/masque/udp/{target_host}/{target_port}/", proxy))
	req, err := masque.NewRequest(ctx, tpl, target)
	if err != nil {
		log.Fatal(err)
	}

	tr := masque.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // local E2E certificate only
			NextProtos:         []string{http3.NextProtoH3},
		},
		QUICConfig: &quic.Config{
			EnableDatagrams:   true,
			InitialPacketSize: 1350,
		},
	}
	conn, rsp, err := tr.Dial(req)
	if err != nil {
		if rsp != nil {
			log.Fatalf("CONNECT-UDP failed: status=%s err=%v", rsp.Status, err)
		}
		log.Fatalf("CONNECT-UDP failed: %v", err)
	}
	defer conn.Close()
	if rsp.StatusCode != 200 {
		log.Fatalf("expected 200, got %s", rsp.Status)
	}

	payload := []byte("mycaddy-release-e2e-h3")
	if _, err := conn.WriteTo(payload, nil); err != nil {
		log.Fatalf("writing UDP payload: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		log.Fatal(err)
	}
	buf := make([]byte, 2048)
	n, addr, err := conn.ReadFrom(buf)
	if err != nil {
		log.Fatalf("reading UDP echo: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		log.Fatalf("UDP payload mismatch: sent=%q got=%q", payload, buf[:n])
	}
	if _, _, err := net.SplitHostPort(addr.String()); err != nil {
		log.Printf("remote addr=%s", addr)
	}
	fmt.Println("H3_UDP_E2E=PASS")
}
