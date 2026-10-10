// lease-net-agent is a disposable fixture for the Incus lease network
// acceptance VM. It is never part of an ANAS release. The same static binary
// runs on the VM host, inside network namespaces that play the LAN and the
// internet, inside a Docker container that plays another Module, and inside
// lease guests, so every reachability answer comes from one implementation.
//
//	serve --tag T [--tcp P,..] [--udp P,..] [--http P,..] [--auth P,..] [--hold P,..] [--status DIR]
//	                           --auth answers Traefik forwardAuth: 200 only when
//	                           the request carries X-Lnet-Auth: allow, else 401
//	batch                      probes as a JSON array on stdin, results on stdout
//	hold-client HOST PORT      answer a hold server's heartbeats until it closes
//	idle                       sleep until terminated (a Compose service body)
//	occupy FIRST LAST FREE READY  listen on every TCP port FIRST..LAST except the
//	                           multiples of FREE (and ports already taken), write
//	                           the count to READY, hold them until terminated
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: lease-net-agent serve|batch|hold-client|idle|occupy")
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "batch":
		batch(os.Stdin, os.Stdout)
	case "hold-client":
		if len(os.Args) != 4 {
			fail("usage: lease-net-agent hold-client HOST PORT")
		}
		holdClient(os.Args[2], os.Args[3])
	case "idle":
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		<-ctx.Done()
	case "occupy":
		if len(os.Args) != 6 {
			fail("usage: lease-net-agent occupy FIRST LAST FREE READY")
		}
		occupy(os.Args[2], os.Args[3], os.Args[4], os.Args[5])
	default:
		fail("unknown subcommand")
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}

type reply struct {
	Tag  string `json:"tag"`
	Peer string `json:"peer"`
}

func peerOf(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return strings.TrimPrefix(host, "::ffff:")
}

func ports(value string) []int {
	var out []int
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		port, err := strconv.Atoi(part)
		if err != nil || port < 1 || port > 65535 {
			fail("invalid port " + part)
		}
		out = append(out, port)
	}
	return out
}

func serve(args []string) {
	var tag, status string
	var tcp, udp, httpPorts, authPorts, hold []int
	for i := 0; i+1 < len(args); i += 2 {
		switch args[i] {
		case "--tag":
			tag = args[i+1]
		case "--tcp":
			tcp = ports(args[i+1])
		case "--udp":
			udp = ports(args[i+1])
		case "--http":
			httpPorts = ports(args[i+1])
		case "--auth":
			authPorts = ports(args[i+1])
		case "--hold":
			hold = ports(args[i+1])
		case "--status":
			status = args[i+1]
		default:
			fail("unknown serve flag " + args[i])
		}
	}
	if tag == "" || len(args)%2 != 0 || len(hold) > 0 && status == "" {
		fail("serve needs --tag, and --status with --hold")
	}
	// Listen on the dual-stack wildcard so one socket answers both families;
	// where the namespace has no IPv6 at all, fall back to IPv4.
	listen := func(network string, port int) net.Listener {
		l, err := net.Listen(network, "[::]:"+strconv.Itoa(port))
		if err != nil {
			l, err = net.Listen("tcp4", "0.0.0.0:"+strconv.Itoa(port))
		}
		if err != nil {
			fail(err.Error())
		}
		return l
	}
	for _, port := range tcp {
		l := listen("tcp", port)
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				go func() {
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
					buffer := make([]byte, 64)
					_, _ = conn.Read(buffer)
					body, _ := json.Marshal(reply{Tag: tag, Peer: peerOf(conn.RemoteAddr())})
					_, _ = conn.Write(append(body, '\n'))
				}()
			}
		}()
	}
	for _, port := range udp {
		serveUDP(tag, port)
	}
	for _, port := range httpPorts {
		l := listen("tcp", port)
		mux := http.NewServeMux()
		port := port
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"tag": tag, "port": strconv.Itoa(port), "remote": peerOf(remoteAddr(r.RemoteAddr)),
				"xff": r.Header.Get("X-Forwarded-For"), "host": r.Host,
			})
		})
		server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = server.Serve(l) }()
	}
	for _, port := range authPorts {
		l := listen("tcp", port)
		server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Lnet-Auth") == "allow" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"tag": tag, "auth": "denied"})
		})}
		go func() { _ = server.Serve(l) }()
	}
	for _, port := range hold {
		l := listen("tcp", port)
		path := filepath.Join(status, "hold-"+strconv.Itoa(port)+".json")
		go holdServer(l, path)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	<-ctx.Done()
}

type stringAddr string

func (a stringAddr) Network() string { return "tcp" }
func (a stringAddr) String() string  { return string(a) }

func remoteAddr(value string) net.Addr { return stringAddr(value) }

// holdStatus is what a hold server knows about its latest connection: when
// the peer last answered a heartbeat. A connection whose peer stopped
// answering is over, whatever the kernel still keeps for it.
type holdStatus struct {
	Peer          string `json:"peer"`
	Connected     bool   `json:"connected"`
	LastPongMilli int64  `json:"last_pong_unix_ms"`
	Pongs         int    `json:"pongs"`
	Closed        bool   `json:"closed"`
}

func holdServer(l net.Listener, path string) {
	var mu sync.Mutex
	write := func(status holdStatus) {
		mu.Lock()
		defer mu.Unlock()
		body, _ := json.Marshal(status)
		tmp := path + ".tmp"
		if os.WriteFile(tmp, body, 0644) == nil {
			_ = os.Rename(tmp, path)
		}
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			status := holdStatus{Peer: peerOf(conn.RemoteAddr()), Connected: true}
			write(status)
			reader := bufio.NewReader(conn)
			for {
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				if _, err := conn.Write([]byte("ping\n")); err != nil {
					break
				}
				line, err := reader.ReadString('\n')
				if err == nil && line == "pong\n" {
					status.LastPongMilli, status.Pongs = time.Now().UnixMilli(), status.Pongs+1
					write(status)
					time.Sleep(300 * time.Millisecond)
					continue
				}
				var timeout net.Error
				if errors.As(err, &timeout) && timeout.Timeout() {
					// Keep the socket: a silent peer is the observation, not
					// a reason to tear the connection down from this side.
					continue
				}
				break
			}
			status.Closed = true
			write(status)
		}()
	}
}

func holdClient(host, port string) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 5*time.Second)
	if err != nil {
		fail(err.Error())
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if line == "ping\n" {
			if _, err := conn.Write([]byte("pong\n")); err != nil {
				return
			}
		}
	}
}

// Probe is one reachability question. Kind is tcp, udp, connect, https, http,
// dns or bind. Source optionally pins the local address.
type Probe struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Source    string `json:"source,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
	// HostHeader names the virtual host for http/https; Name is the DNS
	// query, or the protocol (tcp, udp) of a bind probe.
	HostHeader string `json:"host_header,omitempty"`
	Name       string `json:"name,omitempty"`
	// Auth is sent as X-Lnet-Auth by an http/https probe.
	Auth string `json:"auth,omitempty"`
}

// Result is the answer; OK means the peer answered (or, for bind, the port
// was free). Error is a coarse class so a timeout (dropped) and a refusal
// (reached, nothing listening) stay distinguishable in the evidence.
type Result struct {
	OK     bool              `json:"ok"`
	Error  string            `json:"error,omitempty"`
	Tag    string            `json:"tag,omitempty"`
	Peer   string            `json:"peer,omitempty"`
	Status int               `json:"status,omitempty"`
	Body   map[string]string `json:"body,omitempty"`
	Answer []string          `json:"answer,omitempty"`
}

func batch(in io.Reader, out io.Writer) {
	var probes []Probe
	decoder := json.NewDecoder(io.LimitReader(in, 1<<20))
	if err := decoder.Decode(&probes); err != nil {
		fail("batch input is not a JSON array of probes")
	}
	results := make(map[string]Result, len(probes))
	var mu sync.Mutex
	var wg sync.WaitGroup
	gate := make(chan struct{}, 64)
	for _, probe := range probes {
		wg.Add(1)
		gate <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-gate }()
			result := run(probe)
			mu.Lock()
			results[probe.ID] = result
			mu.Unlock()
		}()
	}
	wg.Wait()
	_ = json.NewEncoder(out).Encode(results)
}

func classify(err error) string {
	var netErr net.Error
	switch {
	case err == nil:
		return ""
	case errors.As(err, &netErr) && netErr.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "unreachable"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, io.EOF):
		return "reset"
	case errors.Is(err, syscall.EADDRINUSE):
		return "in_use"
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return "denied"
	default:
		return "error"
	}
}

func dialer(probe Probe, timeout time.Duration, network string) *net.Dialer {
	d := &net.Dialer{Timeout: timeout}
	if probe.Source != "" {
		if network == "udp" {
			d.LocalAddr = &net.UDPAddr{IP: net.ParseIP(probe.Source)}
		} else {
			d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(probe.Source)}
		}
	}
	return d
}

func run(probe Probe) Result {
	timeout := time.Duration(probe.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	address := net.JoinHostPort(probe.Host, strconv.Itoa(probe.Port))
	switch probe.Kind {
	case "connect":
		// Only the handshake: for a peer that is not this agent (Traefik).
		conn, err := dialer(probe, timeout, "tcp").Dial("tcp", address)
		if err != nil {
			return Result{Error: classify(err)}
		}
		_ = conn.Close()
		return Result{OK: true}
	case "tcp", "udp":
		conn, err := dialer(probe, timeout, probe.Kind).Dial(probe.Kind, address)
		if err != nil {
			return Result{Error: classify(err)}
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(timeout))
		if _, err := conn.Write([]byte("probe\n")); err != nil {
			return Result{Error: classify(err)}
		}
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return Result{Error: classify(err)}
		}
		var answer reply
		if json.Unmarshal([]byte(line), &answer) != nil {
			return Result{Error: "malformed"}
		}
		return Result{OK: true, Tag: answer.Tag, Peer: answer.Peer}
	case "http", "https":
		transport := &http.Transport{
			DialContext:     dialer(probe, timeout, "tcp").DialContext,
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: probe.HostHeader},
		}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		request, err := http.NewRequest(http.MethodGet, probe.Kind+"://"+address+"/", nil)
		if err != nil {
			return Result{Error: "error"}
		}
		if probe.HostHeader != "" {
			request.Host = probe.HostHeader
		}
		if probe.Auth != "" {
			request.Header.Set("X-Lnet-Auth", probe.Auth)
		}
		response, err := client.Do(request)
		if err != nil {
			return Result{Error: classify(err)}
		}
		defer response.Body.Close()
		body := map[string]string{}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&body)
		return Result{OK: true, Status: response.StatusCode, Body: body}
	case "dns":
		return dnsQuery(probe, address, timeout)
	case "bind":
		var closer io.Closer
		var err error
		// Name is the protocol; Host is ignored: a hold binds every address.
		if probe.Name == "udp" {
			closer, err = net.ListenPacket("udp", ":"+strconv.Itoa(probe.Port))
		} else {
			closer, err = net.Listen("tcp", ":"+strconv.Itoa(probe.Port))
		}
		if err != nil {
			return Result{Error: classify(err)}
		}
		_ = closer.Close()
		return Result{OK: true}
	}
	return Result{Error: "unknown_kind"}
}

// dnsQuery sends one recursive A query over UDP and returns the addresses.
func dnsQuery(probe Probe, address string, timeout time.Duration) Result {
	conn, err := dialer(probe, timeout, "udp").Dial("udp", address)
	if err != nil {
		return Result{Error: classify(err)}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	query := []byte{0x4e, 0x41, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, label := range strings.Split(strings.TrimSuffix(probe.Name, "."), ".") {
		if label == "" || len(label) > 63 {
			return Result{Error: "error"}
		}
		query = append(query, byte(len(label)))
		query = append(query, label...)
	}
	query = append(query, 0, 0, 1, 0, 1)
	if _, err := conn.Write(query); err != nil {
		return Result{Error: classify(err)}
	}
	buffer := make([]byte, 1500)
	n, err := conn.Read(buffer)
	if err != nil {
		return Result{Error: classify(err)}
	}
	answers, rcode, ok := parseDNS(buffer[:n])
	if !ok {
		return Result{Error: "malformed"}
	}
	return Result{OK: true, Status: rcode, Answer: answers}
}

func parseDNS(message []byte) ([]string, int, bool) {
	if len(message) < 12 || message[0] != 0x4e || message[1] != 0x41 {
		return nil, 0, false
	}
	rcode := int(message[3] & 0x0f)
	questions, answers := binary.BigEndian.Uint16(message[4:]), binary.BigEndian.Uint16(message[6:])
	offset := 12
	skipName := func() bool {
		for offset < len(message) {
			length := int(message[offset])
			switch {
			case length == 0:
				offset++
				return true
			case length&0xc0 == 0xc0:
				offset += 2
				return offset <= len(message)
			default:
				offset += 1 + length
			}
		}
		return false
	}
	for i := 0; i < int(questions); i++ {
		if !skipName() || offset+4 > len(message) {
			return nil, 0, false
		}
		offset += 4
	}
	var out []string
	for i := 0; i < int(answers); i++ {
		if !skipName() || offset+10 > len(message) {
			return nil, 0, false
		}
		kind, length := binary.BigEndian.Uint16(message[offset:]), int(binary.BigEndian.Uint16(message[offset+8:]))
		offset += 10
		if offset+length > len(message) {
			return nil, 0, false
		}
		if kind == 1 && length == 4 {
			out = append(out, net.IP(message[offset:offset+4]).String())
		}
		offset += length
	}
	return out, rcode, true
}

// occupy takes most of a host port range, so an allocator that picks ports
// at random must skip taken ones to find a free one. Go raises the soft
// descriptor limit to the hard limit at start.
func occupy(firstArg, lastArg, freeArg, ready string) {
	first, err1 := strconv.Atoi(firstArg)
	last, err2 := strconv.Atoi(lastArg)
	free, err3 := strconv.Atoi(freeArg)
	if err1 != nil || err2 != nil || err3 != nil || first < 1 || last > 65535 || first > last || free < 2 {
		fail("occupy: invalid range")
	}
	var listeners []net.Listener
	for port := first; port <= last; port++ {
		if port%free == 0 {
			continue
		}
		listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		if err != nil {
			continue // already taken, by a hold or anything else
		}
		listeners = append(listeners, listener)
	}
	if err := os.WriteFile(ready, []byte(strconv.Itoa(len(listeners))+"\n"), 0644); err != nil {
		fail("occupy: ready file")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	<-ctx.Done()
	for _, listener := range listeners {
		_ = listener.Close()
	}
}
