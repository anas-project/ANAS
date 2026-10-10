// console-page-probe is a disposable fixture for the Incus lease network
// acceptance VM (INCUS-R-129). It is never part of an ANAS release. It starts
// a headless Chromium, gives it the console session the orchestrator already
// holds, opens the lease network page and reports what the page renders:
// every lease's outbound and inbound edges, the controls in the section and
// every request the page sent. It also saves a screenshot.
//
// The request comes as JSON on stdin, so session cookies never reach argv:
//
//	{"chromium": "/usr/bin/chromium", "origin": "https://127.0.0.1:18445",
//	 "path": "/#/network", "cookies": [{"name": "...", "value": "..."}],
//	 "leases": 4, "screenshot": "/path/page.png", "timeout_ms": 90000}
//
// It speaks the Chrome DevTools Protocol over a minimal WebSocket client with
// the standard library only.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type request struct {
	Chromium   string `json:"chromium"`
	Origin     string `json:"origin"`
	Path       string `json:"path"`
	Cookies    []struct{ Name, Value string }
	Leases     int    `json:"leases"`
	Screenshot string `json:"screenshot"`
	TimeoutMS  int    `json:"timeout_ms"`
}

type sentRequest struct {
	Method string `json:"method"`
	URL    string `json:"url"`
}

type report struct {
	Page            json.RawMessage `json:"page"`
	Requests        []sentRequest   `json:"requests"`
	ScreenshotBytes int             `json:"screenshot_bytes"`
}

func main() {
	var in request
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&in); err != nil || in.Chromium == "" ||
		!strings.HasPrefix(in.Origin, "https://") || in.Leases < 1 || in.Screenshot == "" {
		fail("invalid request")
	}
	timeout := time.Duration(in.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := probe(ctx, in)
	if err != nil {
		fail(err.Error())
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fail("write report")
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "console-page-probe:", message)
	os.Exit(1)
}

func probe(ctx context.Context, in request) (report, error) {
	profile, err := os.MkdirTemp("", "console-page-probe-")
	if err != nil {
		return report{}, err
	}
	defer os.RemoveAll(profile)
	// Root in a disposable VM: Chromium's sandbox needs a non-root user, and
	// the console's certificate is the fixture's own.
	browser := exec.CommandContext(ctx, in.Chromium, "--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage",
		"--no-first-run", "--no-default-browser-check", "--ignore-certificate-errors", "--window-size=1400,1800",
		"--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "--user-data-dir="+profile, "about:blank")
	browser.Stdout, browser.Stderr = io.Discard, io.Discard
	if err := browser.Start(); err != nil {
		return report{}, fmt.Errorf("start chromium: %w", err)
	}
	defer func() {
		_ = browser.Process.Kill()
		_ = browser.Wait()
	}()
	port, err := devToolsPort(ctx, filepath.Join(profile, "DevToolsActivePort"))
	if err != nil {
		return report{}, err
	}
	target, err := pageTarget(ctx, port)
	if err != nil {
		return report{}, err
	}
	conn, err := dialWebSocket(ctx, target)
	if err != nil {
		return report{}, err
	}
	defer conn.close()
	session := newCDP(conn)
	go session.read()

	for _, method := range []string{"Network.enable", "Page.enable", "Runtime.enable"} {
		if _, err := session.call(ctx, method, map[string]any{}); err != nil {
			return report{}, err
		}
	}
	for _, cookie := range in.Cookies {
		result, err := session.call(ctx, "Network.setCookie", map[string]any{"name": cookie.Name, "value": cookie.Value,
			"url": in.Origin + "/", "path": "/", "secure": true, "httpOnly": true, "sameSite": "Strict"})
		var set struct{ Success bool }
		if err != nil || json.Unmarshal(result, &set) != nil || !set.Success {
			return report{}, fmt.Errorf("session cookie %s was not accepted", cookie.Name)
		}
	}
	if _, err := session.call(ctx, "Page.navigate", map[string]any{"url": in.Origin + in.Path}); err != nil {
		return report{}, err
	}
	ready := fmt.Sprintf(`(() => { const s = document.querySelector('section[aria-labelledby="network-title"]');
		return !!s && s.querySelectorAll('article.network-lease svg.network-diagram').length >= %d })()`, in.Leases)
	for {
		value, err := session.evaluate(ctx, ready)
		if err == nil && string(value) == "true" {
			break
		}
		if ctx.Err() != nil {
			return report{}, fmt.Errorf("the lease network page did not render %d leases", in.Leases)
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Reload through the page's own control: a read-only page sends only
	// reads, however it is used.
	if _, err := session.evaluate(ctx, `(() => { const b = document.querySelector('section[aria-labelledby="network-title"] button');
		if (b) b.click(); return !!b })()`); err != nil {
		return report{}, err
	}
	time.Sleep(3 * time.Second)
	page, err := session.evaluate(ctx, extractPage)
	if err != nil {
		return report{}, err
	}
	shot, err := session.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": true})
	if err != nil {
		return report{}, err
	}
	var image struct{ Data string }
	if json.Unmarshal(shot, &image) != nil {
		return report{}, errors.New("screenshot response")
	}
	png, err := base64.StdEncoding.DecodeString(image.Data)
	if err != nil || len(png) == 0 {
		return report{}, errors.New("screenshot data")
	}
	if err := os.WriteFile(in.Screenshot, png, 0644); err != nil {
		return report{}, err
	}
	return report{Page: page, Requests: session.requests(), ScreenshotBytes: len(png)}, nil
}

// extractPage reads the rendered section: each lease's edges from the SVG
// (direction from the column, allowed or denied from the class and marker,
// the computed stroke so the two are told apart by colour), every control
// and form inside the section.
const extractPage = `(() => {
  const section = document.querySelector('section[aria-labelledby="network-title"]');
  if (!section) return {error: 'no network section'};
  const leases = [...section.querySelectorAll('article.network-lease')].map(article => {
    const svg = article.querySelector('svg.network-diagram');
    const edges = (svg ? [...svg.querySelectorAll('g > g')] : []).map(g => {
      const rect = g.querySelector('rect.network-peer');
      const line = g.querySelector('line.network-edge');
      const label = g.querySelector('text.network-peer-label');
      const title = line ? line.querySelector('title') : null;
      const style = line ? getComputedStyle(line) : null;
      return {
        flow: rect && Number(rect.getAttribute('x')) === 0 ? 'egress' : 'ingress',
        label: label ? label.textContent.trim() : '',
        allowed: !!line && line.classList.contains('allowed'),
        denied: !!line && line.classList.contains('denied'),
        marker: line ? line.getAttribute('marker-end') : '',
        title: title ? title.textContent.trim() : '',
        stroke: style ? style.stroke : '',
        dash: style ? style.strokeDasharray : '',
      };
    });
    return {key: article.getAttribute('data-lease'),
      egress: edges.filter(e => e.flow === 'egress'), ingress: edges.filter(e => e.flow === 'ingress')};
  });
  const controls = [...section.querySelectorAll('button, input, select, textarea, a[href], [contenteditable="true"]')]
    .map(e => ({tag: e.tagName.toLowerCase(), type: e.getAttribute('type') || '', text: (e.textContent || '').trim().slice(0, 60)}));
  return {leases, controls, forms: section.querySelectorAll('form').length};
})()`

func devToolsPort(ctx context.Context, path string) (string, error) {
	for {
		body, err := os.ReadFile(path)
		if err == nil {
			if line, _, _ := strings.Cut(string(body), "\n"); line != "" {
				if _, err := strconv.Atoi(line); err == nil {
					return line, nil
				}
			}
		}
		if ctx.Err() != nil {
			return "", errors.New("chromium did not open its DevTools port")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func pageTarget(ctx context.Context, port string) (string, error) {
	for {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/json/list", nil)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			var targets []struct {
				Type                 string `json:"type"`
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&targets)
			response.Body.Close()
			for _, target := range targets {
				if decodeErr == nil && target.Type == "page" && strings.HasPrefix(target.WebSocketDebuggerURL, "ws://127.0.0.1:"+port+"/") {
					return target.WebSocketDebuggerURL, nil
				}
			}
		}
		if ctx.Err() != nil {
			return "", errors.New("chromium has no page target")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// --- WebSocket (RFC 6455), client side, text frames only -----------------------------

type wsConn struct {
	conn   net.Conn
	reader *bufio.Reader
	write  sync.Mutex
}

func dialWebSocket(ctx context.Context, url string) (*wsConn, error) {
	rest := strings.TrimPrefix(url, "ws://")
	host, path, ok := strings.Cut(rest, "/")
	if !ok {
		return nil, errors.New("websocket url")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	fmt.Fprintf(conn, "GET /%s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, host, base64.StdEncoding.EncodeToString(key))
	reader := bufio.NewReaderSize(conn, 1<<16)
	response, err := http.ReadResponse(reader, nil)
	if err != nil || response.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, errors.New("websocket upgrade refused")
	}
	return &wsConn{conn: conn, reader: reader}, nil
}

func (c *wsConn) send(payload []byte) error {
	c.write.Lock()
	defer c.write.Unlock()
	header := []byte{0x81}
	switch n := len(payload); {
	case n < 126:
		header = append(header, 0x80|byte(n))
	case n <= 0xffff:
		header = append(header, 0x80|126, byte(n>>8), byte(n))
	default:
		header = append(header, 0x80|127)
		header = binary.BigEndian.AppendUint64(header, uint64(n))
	}
	mask := make([]byte, 4)
	_, _ = rand.Read(mask)
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	_, err := c.conn.Write(append(append(header, mask...), masked...))
	return err
}

func (c *wsConn) receive() ([]byte, error) {
	var message []byte
	for {
		var head [2]byte
		if _, err := io.ReadFull(c.reader, head[:]); err != nil {
			return nil, err
		}
		fin, opcode := head[0]&0x80 != 0, head[0]&0x0f
		length := uint64(head[1] & 0x7f)
		switch length {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
				return nil, err
			}
			length = uint64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
				return nil, err
			}
			length = binary.BigEndian.Uint64(ext[:])
		}
		if head[1]&0x80 != 0 || length > 64<<20 {
			return nil, errors.New("unexpected websocket frame")
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.reader, payload); err != nil {
			return nil, err
		}
		switch opcode {
		case 0x8:
			return nil, io.EOF
		case 0x9, 0xa:
			continue // the browser does not ping; ignore either way
		}
		message = append(message, payload...)
		if fin {
			return message, nil
		}
	}
}

func (c *wsConn) close() { _ = c.conn.Close() }

// --- Chrome DevTools Protocol ----------------------------------------------------------

type cdp struct {
	conn    *wsConn
	mu      sync.Mutex
	next    int
	waiting map[int]chan cdpReply
	sent    []sentRequest
	done    chan struct{}
}

type cdpReply struct {
	Result json.RawMessage
	Err    error
}

func newCDP(conn *wsConn) *cdp {
	return &cdp{conn: conn, waiting: map[int]chan cdpReply{}, done: make(chan struct{})}
}

func (c *cdp) read() {
	defer close(c.done)
	for {
		body, err := c.conn.receive()
		if err != nil {
			return
		}
		var message struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &message) != nil {
			continue
		}
		c.mu.Lock()
		if message.ID != 0 {
			if reply, ok := c.waiting[message.ID]; ok {
				delete(c.waiting, message.ID)
				var err error
				if message.Error != nil {
					err = errors.New(message.Error.Message)
				}
				reply <- cdpReply{Result: message.Result, Err: err}
			}
		} else if message.Method == "Network.requestWillBeSent" {
			var event struct {
				Request sentRequest `json:"request"`
			}
			if json.Unmarshal(message.Params, &event) == nil {
				c.sent = append(c.sent, event.Request)
			}
		}
		c.mu.Unlock()
	}
}

func (c *cdp) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.next++
	id := c.next
	reply := make(chan cdpReply, 1)
	c.waiting[id] = reply
	c.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := c.conn.send(body); err != nil {
		return nil, err
	}
	select {
	case r := <-reply:
		if r.Err != nil {
			return nil, fmt.Errorf("%s: %w", method, r.Err)
		}
		return r.Result, nil
	case <-c.done:
		return nil, fmt.Errorf("%s: browser connection closed", method)
	case <-ctx.Done():
		return nil, fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

func (c *cdp) evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	result, err := c.call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true})
	if err != nil {
		return nil, err
	}
	var value struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if json.Unmarshal(result, &value) != nil || len(value.ExceptionDetails) > 0 {
		return nil, errors.New("page script failed")
	}
	return value.Result.Value, nil
}

func (c *cdp) requests() []sentRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sentRequest(nil), c.sent...)
}
