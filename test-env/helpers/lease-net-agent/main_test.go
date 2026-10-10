package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestBatchClassifiesAnswersAndRefusals(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			body, _ := json.Marshal(reply{Tag: "t1", Peer: peerOf(conn.RemoteAddr())})
			_, _ = conn.Write(append(body, '\n'))
			_ = conn.Close()
		}
	}()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	go func() {
		buffer := make([]byte, 64)
		for {
			_, peer, err := packet.ReadFrom(buffer)
			if err != nil {
				return
			}
			body, _ := json.Marshal(reply{Tag: "u1", Peer: peerOf(peer)})
			_, _ = packet.WriteTo(append(body, '\n'), peer)
		}
	}()
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"host": r.Host, "auth": r.Header.Get("X-Lnet-Auth")})
	}))
	defer web.Close()
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	closedPort := closed.Addr().(*net.TCPAddr).Port
	_ = closed.Close()
	webPort, _ := strconv.Atoi(web.URL[strings.LastIndex(web.URL, ":")+1:])
	held, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	probes := []Probe{
		{ID: "tcp", Kind: "tcp", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port},
		{ID: "udp", Kind: "udp", Host: "127.0.0.1", Port: packet.LocalAddr().(*net.UDPAddr).Port},
		{ID: "http", Kind: "http", Host: "127.0.0.1", Port: webPort, HostHeader: "x.example.test", Auth: "allow"},
		{ID: "refused", Kind: "tcp", Host: "127.0.0.1", Port: closedPort},
		{ID: "bind", Kind: "bind", Name: "tcp", Port: held.Addr().(*net.TCPAddr).Port},
	}
	input, _ := json.Marshal(probes)
	var output bytes.Buffer
	batch(bytes.NewReader(input), &output)
	var results map[string]Result
	if err := json.Unmarshal(output.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	if r := results["tcp"]; !r.OK || r.Tag != "t1" || r.Peer != "127.0.0.1" {
		t.Fatalf("tcp: %+v", r)
	}
	if r := results["udp"]; !r.OK || r.Tag != "u1" {
		t.Fatalf("udp: %+v", r)
	}
	if r := results["http"]; !r.OK || r.Status != 200 || r.Body["host"] != "x.example.test" || r.Body["auth"] != "allow" {
		t.Fatalf("http: %+v", r)
	}
	if r := results["refused"]; r.OK || r.Error != "refused" {
		t.Fatalf("refused: %+v", r)
	}
	if r := results["bind"]; r.OK || r.Error != "in_use" {
		t.Fatalf("bind: %+v", r)
	}
}

func TestParseDNSReadsARecords(t *testing.T) {
	message := []byte{0x4e, 0x41, 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0,
		1, 'a', 4, 't', 'e', 's', 't', 0, 0, 1, 0, 1,
		0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 192, 0, 2, 7}
	answers, rcode, ok := parseDNS(message)
	if !ok || rcode != 0 || len(answers) != 1 || answers[0] != "192.0.2.7" {
		t.Fatalf("answers=%v rcode=%d ok=%v", answers, rcode, ok)
	}
}
