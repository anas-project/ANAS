// TEST_CASES: MAILMOD-T-006 MAILMOD-T-017
package dms

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/textproto"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type nativeOutboundQueue struct {
	Sender     string `json:"sender"`
	Recipients []struct {
		Address     string `json:"address"`
		DelayReason string `json:"delay_reason"`
	} `json:"recipients"`
}

// This native test proves actual Postfix routing, verified TLS and relay
// authentication. Its relays run only in the fixture's internal Docker network,
// without published ports or an external route.
// It does not substitute for Roundcube/AD or real-provider acceptance.
func TestDMSNativeOutbound(t *testing.T) {
	path := os.Getenv("MAIL_DMS_NATIVE_CONFIG")
	if path == "" {
		t.Skip("explicit native DMS fixture required; no real-host pass")
	}
	var cfg struct{ Outbound bool }
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &cfg) != nil || !cfg.Outbound {
		t.Skip("native outbound fixture not enabled; no outbound pass")
	}
	certificate, err := tls.LoadX509KeyPair("/input/cert.pem", "/input/key.pem")
	if err != nil {
		t.Fatal("fixture TLS identity unavailable")
	}
	type delivery struct{ mode, sender, subject string }
	got := make(chan delivery, 8)
	type bounce struct{ recipient, body string }
	bounces := make(chan bounce, 8)
	rejected := make(chan string, 8)
	var rejectAuthentication atomic.Bool
	var sessions sync.WaitGroup
	for _, mode := range []string{"starttls", "tls"} {
		port := "2587"
		if mode == "tls" {
			port = "2465"
		}
		// Postfix uses DNS rather than /etc/hosts for relay lookup, so its own
		// container name resolves to eth0. Accept that private address as well.
		listener, err := net.Listen("tcp4", "0.0.0.0:"+port)
		if err != nil {
			t.Fatal("fixture relay listener unavailable")
		}
		t.Cleanup(func() { listener.Close() })
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				sessions.Add(1)
				go func() {
					defer sessions.Done()
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
					secure, authenticated := false, false
					tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
					if mode == "tls" {
						wrapped := tls.Server(conn, tlsConfig)
						if wrapped.Handshake() != nil {
							return
						}
						conn, secure = wrapped, true
					}
					reader := textproto.NewReader(bufio.NewReader(conn))
					reply := func(s string) bool { _, err := fmt.Fprint(conn, s+"\r\n"); return err == nil }
					if !reply("220 isolated native relay") {
						return
					}
					var sender, recipient string
					for {
						line, err := reader.ReadLine()
						if err != nil {
							return
						}
						switch {
						case strings.HasPrefix(line, "EHLO "):
							if !secure {
								reply("250-isolated\r\n250 STARTTLS")
							} else {
								reply("250-isolated\r\n250 AUTH PLAIN")
							}
						case line == "STARTTLS" && !secure:
							reply("220 TLS ready")
							wrapped := tls.Server(conn, tlsConfig)
							if wrapped.Handshake() != nil {
								return
							}
							conn, secure = wrapped, true
							reader = textproto.NewReader(bufio.NewReader(conn))
						case strings.HasPrefix(line, "AUTH PLAIN ") && secure:
							if mode == "tls" && rejectAuthentication.Load() {
								reply("535 5.7.8 isolated credentials rejected")
								rejected <- "authentication"
								return
							}
							decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
							parts := strings.Split(string(decoded), "\x00")
							authenticated = err == nil && len(parts) == 3 && parts[1] == "native-"+mode && parts[2] == "synthetic-native-"+mode
							if !authenticated {
								reply("535 fixture authentication failed")
								return
							}
							reply("235 authenticated")
						case strings.HasPrefix(line, "MAIL FROM:") && authenticated:
							sender = strings.TrimPrefix(line, "MAIL FROM:")
							reply("250 sender accepted")
						case strings.HasPrefix(line, "RCPT TO:") && authenticated:
							recipient = strings.TrimPrefix(line, "RCPT TO:")
							switch {
							case strings.HasPrefix(line, "RCPT TO:<temporary@"):
								reply("451 4.7.0 isolated account quota temporarily unavailable")
								rejected <- "temporary"
							case strings.HasPrefix(line, "RCPT TO:<permanent@"):
								reply("550 5.1.1 isolated recipient permanently rejected")
								rejected <- "permanent"
							default:
								reply("250 recipient accepted")
							}
						case line == "DATA" && authenticated:
							reply("354 data")
							body, err := reader.ReadDotBytes()
							if err != nil {
								return
							}
							message, e := mail.ReadMessage(strings.NewReader(string(body)))
							if e == nil && strings.HasPrefix(message.Header.Get("Subject"), "ANAS native ") {
								got <- delivery{mode: mode, sender: sender, subject: message.Header.Get("Subject")}
							}
							if strings.HasPrefix(sender, "<>") && strings.HasPrefix(recipient, "<native-permanent@starttls.example.test>") {
								bounces <- bounce{recipient: recipient, body: string(body)}
							}
							reply("250 persisted by isolated relay")
							return
						case line == "RSET":
							reply("250 reset")
						case line == "QUIT":
							reply("221 bye")
							return
						default:
							reply("530 authentication required")
						}
					}
				}()
			}
		}()
	}
	for _, mode := range []string{"starttls", "tls"} {
		sender := "native@" + mode + ".example.test"
		cmd := exec.CommandContext(t.Context(), "/usr/sbin/sendmail", "-i", "-f", sender, "controlled@external.example.test")
		cmd.Stdin = strings.NewReader("From: " + sender + "\nTo: controlled@external.example.test\nSubject: ANAS native " + mode + "\n\nControlled native Postfix route test.\n")
		if cmd.Run() != nil {
			t.Fatal("native queue submission failed")
		}
	}
	seen := map[string]bool{}
	deadline := time.NewTimer(100 * time.Second)
	defer deadline.Stop()
	for len(seen) < 2 {
		select {
		case received := <-got:
			if seen[received.mode] || received.subject != "ANAS native "+received.mode || !strings.HasPrefix(received.sender, "<native@"+received.mode+".example.test>") {
				t.Fatal("wrong relay, message, sender or duplicate delivery")
			}
			seen[received.mode] = true
		case <-deadline.C:
			t.Fatal("native Postfix did not complete both verified TLS relay routes")
		}
	}
	// In this private network the recipient domain resolves directly to this
	// container. Override only the test transport's port so the fake recipient
	// can coexist with DMS on port 25. Production retains the standard port.
	listener, err := net.Listen("tcp4", "0.0.0.0:2588")
	if err != nil {
		t.Fatal("direct recipient listener unavailable")
	}
	t.Cleanup(func() { listener.Close() })
	if out, err := exec.CommandContext(t.Context(), "/usr/sbin/postconf", "-P", "anas_direct/unix/smtp_tcp_port=2588").CombinedOutput(); err != nil {
		t.Fatalf("test port override failed: %s", out)
	}
	if out, err := exec.CommandContext(t.Context(), "/usr/sbin/postfix", "reload").CombinedOutput(); err != nil {
		t.Fatalf("test port reload failed: %s", out)
	}
	type directDelivery struct {
		secure          bool
		sender, subject string
	}
	directGot := make(chan directDelivery, 2)
	var advertiseTLS atomic.Bool
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			sessions.Add(1)
			go func() {
				defer sessions.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
				reader := textproto.NewReader(bufio.NewReader(conn))
				reply := func(s string) { _, _ = fmt.Fprint(conn, s+"\r\n") }
				reply("220 isolated direct recipient")
				secure := false
				var sender string
				for {
					line, err := reader.ReadLine()
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO "):
						if advertiseTLS.Load() && !secure {
							reply("250-isolated\r\n250 STARTTLS")
						} else {
							reply("250 isolated")
						}
					case line == "STARTTLS" && advertiseTLS.Load() && !secure:
						reply("220 TLS ready")
						wrapped := tls.Server(conn, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}})
						if wrapped.Handshake() != nil {
							return
						}
						conn, secure = wrapped, true
						reader = textproto.NewReader(bufio.NewReader(conn))
					case strings.HasPrefix(line, "MAIL FROM:"):
						sender = line
						reply("250 sender accepted")
					case strings.HasPrefix(line, "RCPT TO:"):
						reply("250 recipient accepted")
					case line == "DATA":
						reply("354 data")
						body, err := reader.ReadDotBytes()
						if err != nil {
							return
						}
						message, err := mail.ReadMessage(strings.NewReader(string(body)))
						if err != nil {
							return
						}
						directGot <- directDelivery{secure: secure, sender: sender, subject: message.Header.Get("Subject")}
						reply("250 persisted by isolated recipient")
						return
					case line == "QUIT":
						reply("221 bye")
						return
					default:
						reply("500 unsupported")
					}
				}
			}()
		}
	}()
	for _, secure := range []bool{true, false} {
		advertiseTLS.Store(secure)
		subject := fmt.Sprintf("ANAS native direct tls=%t", secure)
		sender, recipient := "native@direct.example.test", "controlled@direct.mailtest.invalid"
		cmd := exec.CommandContext(t.Context(), "/usr/sbin/sendmail", "-i", "-f", sender, recipient)
		cmd.Stdin = strings.NewReader("From: " + sender + "\nTo: " + recipient + "\nSubject: " + subject + "\n\nControlled direct route test.\n")
		if cmd.Run() != nil {
			t.Fatal("native direct submission failed")
		}
		select {
		case received := <-directGot:
			if received.secure != secure || received.subject != subject || !strings.HasPrefix(received.sender, "MAIL FROM:<"+sender+">") {
				t.Fatal("direct route, sender or opportunistic TLS mismatch")
			}
		case <-time.After(45 * time.Second):
			t.Fatal("native Postfix did not complete the direct route")
		}
	}
	// Both failures must be visible in the real native queue. A 550 must
	// terminate the original message and generate a delivery-status report,
	// rather than silently dropping it or retrying it forever.
	queue := func() []nativeOutboundQueue {
		t.Helper()
		raw, err := exec.CommandContext(t.Context(), "/usr/sbin/postqueue", "-j").Output()
		if err != nil {
			t.Fatal("native queue cannot be inspected")
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		var result []nativeOutboundQueue
		for {
			var item nativeOutboundQueue
			if err := decoder.Decode(&item); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal("native queue response invalid")
			}
			result = append(result, item)
		}
		return result
	}
	for _, failure := range []string{"temporary", "permanent", "authentication"} {
		t.Run(failure+" preserves actual outcome", func(t *testing.T) {
			mode := "starttls"
			if failure == "authentication" {
				mode = "tls"
				rejectAuthentication.Store(true)
			}
			sender := "native-" + failure + "@" + mode + ".example.test"
			recipient := failure + "@external.example.test"
			cmd := exec.CommandContext(t.Context(), "/usr/sbin/sendmail", "-i", "-f", sender, recipient)
			cmd.Stdin = strings.NewReader("From: " + sender + "\nTo: " + recipient + "\nSubject: ANAS native rejection " + failure + "\n\nControlled rejection test.\n")
			if cmd.Run() != nil {
				t.Fatal("native rejection case not queued")
			}
			select {
			case actual := <-rejected:
				if actual != failure {
					t.Fatal("unexpected relay rejection")
				}
			case <-time.After(45 * time.Second):
				t.Fatal("native relay rejection was not observed")
			}
			deadline := time.Now().Add(25 * time.Second)
			for time.Now().Before(deadline) {
				found, reason := false, ""
				for _, entry := range queue() {
					if entry.Sender == sender {
						found = true
						for _, r := range entry.Recipients {
							reason += r.DelayReason
						}
					}
				}
				if failure == "permanent" && !found {
					select {
					case report := <-bounces:
						if !strings.HasPrefix(report.recipient, "<"+sender+">") || !strings.Contains(report.body, "permanent@external.example.test") || !strings.Contains(report.body, "550 5.1.1") {
							t.Fatal("permanent failure did not return a matching delivery-status report")
						}
					case <-time.After(25 * time.Second):
						t.Fatal("permanent rejection disappeared without an actual delivery-status report")
					}
					return
				}
				if failure != "permanent" && found && (failure == "temporary" && strings.Contains(reason, "451") || failure == "authentication" && strings.Contains(reason, "535")) {
					if status := outboundQueueObservation(t.Context()); status != "queued-observed" {
						t.Fatal("Postfix queue observation missed controlled pending mail", status)
					}
					pending, err := queuePending([]string{"/var/mail-state/spool-postfix", "/var/spool/postfix"})
					if err != nil || !pending {
						t.Fatal("native queue guard missed controlled pending mail", err)
					}
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatal("native queue did not preserve the provider outcome")
		})
	}
}
