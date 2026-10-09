package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatalf("usage: %s <proxy-host:port> <udp-target-host:port>", os.Args[0])
	}
	addr, target := os.Args[1], os.Args[2]
	host, port, err := net.SplitHostPort(target)
	if err != nil { log.Fatal(err) }
	path := fmt.Sprintf("/.well-known/masque/udp/%s/%s/", host, port)
	payload := []byte("mycaddy-release-e2e-h2")

	d := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{
		ServerName: "localhost", InsecureSkipVerify: true, NextProtos: []string{"h2"},
	})
	if err != nil { log.Fatal(err) }
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10*time.Second))
	if conn.ConnectionState().NegotiatedProtocol != "h2" {
		log.Fatalf("ALPN=%q, want h2", conn.ConnectionState().NegotiatedProtocol)
	}
	fr := http2.NewFramer(conn, conn)
	if _, err := conn.Write([]byte(http2.ClientPreface)); err != nil { log.Fatal(err) }
	if err := fr.WriteSettings(http2.Setting{ID:http2.SettingEnableConnectProtocol, Val:1}); err != nil { log.Fatal(err) }

	serverSupports := false
	gotAck := false
	for !(serverSupports && gotAck) {
		f, err := fr.ReadFrame()
		if err != nil { log.Fatalf("reading server settings: %v", err) }
		sf, ok := f.(*http2.SettingsFrame)
		if !ok { continue }
		if sf.IsAck() { gotAck = true; continue }
		if err := sf.ForeachSetting(func(s http2.Setting) error {
			if s.ID == http2.SettingEnableConnectProtocol && s.Val == 1 { serverSupports = true }
			return nil
		}); err != nil { log.Fatal(err) }
		if err := fr.WriteSettingsAck(); err != nil { log.Fatal(err) }
		if !serverSupports {
			log.Fatal("H2_EXTENDED_CONNECT_SERVER_SETTING=UNSUPPORTED")
		}
	}
	fmt.Println("H2_EXTENDED_CONNECT_SERVER_SETTING=SUPPORTED")

	var hb bytes.Buffer
	enc := hpack.NewEncoder(&hb)
	for _, hf := range []hpack.HeaderField{
		{Name:":method", Value:"CONNECT"},
		{Name:":scheme", Value:"https"},
		{Name:":authority", Value:addr},
		{Name:":path", Value:path},
		{Name:":protocol", Value:"connect-udp"},
		{Name:"capsule-protocol", Value:"?1"},
	} {
		if err := enc.WriteField(hf); err != nil { log.Fatal(err) }
	}
	if err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID:1, BlockFragment:hb.Bytes(), EndHeaders:true, EndStream:false}); err != nil { log.Fatal(err) }

	status := ""
	for status == "" {
		f, err := fr.ReadFrame()
		if err != nil { log.Fatalf("reading response headers: %v", err) }
		if rst, ok := f.(*http2.RSTStreamFrame); ok && rst.StreamID == 1 {
			log.Fatalf("H2_CONNECT_RESET_BEFORE_RESPONSE=%s", rst.ErrCode)
		}
		h, ok := f.(*http2.HeadersFrame)
		if !ok || h.StreamID != 1 { continue }
		var block bytes.Buffer
		block.Write(h.HeaderBlockFragment())
		for !h.HeadersEnded() {
			f2, err := fr.ReadFrame(); if err != nil { log.Fatal(err) }
			c, ok := f2.(*http2.ContinuationFrame); if !ok || c.StreamID != 1 { log.Fatalf("unexpected continuation frame %T", f2) }
			block.Write(c.HeaderBlockFragment())
			if c.HeadersEnded() { break }
		}
		dec := hpack.NewDecoder(4096, func(hf hpack.HeaderField) { if hf.Name == ":status" { status = hf.Value } })
		if _, err := io.Copy(dec, &block); err != nil { log.Fatal(err) }
	}
	fmt.Println("H2_CONNECT_STATUS="+status)
	if status != "200" { log.Fatalf("want 200, got %s",status) }

	if len(payload)+1 >= 64 { log.Fatal("payload too large") }
	capsule := append([]byte{0, byte(1+len(payload)), 0}, payload...)
	if err := fr.WriteData(1,false,capsule); err != nil { log.Fatal(err) }

	var data []byte
	for {
		f, err := fr.ReadFrame()
		if err != nil { log.Fatalf("reading UDP reply: %v",err) }
		if rst, ok := f.(*http2.RSTStreamFrame); ok && rst.StreamID == 1 {
			log.Fatalf("H2_CONNECT_RESET_AFTER_200=%s",rst.ErrCode)
		}
		if df, ok := f.(*http2.DataFrame); ok && df.StreamID == 1 {
			data=append(data,df.Data()...)
			if len(data)>=2 {
				need:=2+int(data[1])
				if len(data)>=need {
					body:=data[2:need]
					if len(body)<1 || body[0]!=0 { log.Fatalf("bad context id: %x",body) }
					if string(body[1:])!=string(payload) { log.Fatalf("payload mismatch sent=%q got=%q",payload,body[1:]) }
					fmt.Println("H2_UDP_E2E=PASS")
					return
				}
			}
		}
	}
}
