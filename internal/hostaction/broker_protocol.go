package hostaction

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/securefs"
)

// This private transport carries a binding handshake, NOT job events, exit
// status, credentials or arbitrary RPCs. The action request remains action/v1.
// Only an execution owner with a live HostJobBinding can approve it. It never
// opens the console store from the root process or creates a second journal.
const brokerSchema = "anas.host-job-binding/v1"
const maxBrokerFrame = 8 << 10
const brokerIOTimeout = 3 * time.Second

func brokerExecutionTimeout(action string) time.Duration {
	spec, ok := LookupAction(action)
	if !ok || spec.Timeout <= 0 {
		return brokerIOTimeout
	}
	// The fixed handler budget is followed by its bounded final audit and
	// the two confirmation messages. None is request-configurable.
	return time.Duration(spec.Timeout)*time.Second + 5*time.Second + 2*brokerIOTimeout
}

type brokerIdentity struct {
	PID int32  `json:"pid"`
	UID uint32 `json:"uid"`
	GID uint32 `json:"gid"`
}

func brokerIdentityOf(p PeerIdentity) brokerIdentity { return brokerIdentity{p.PID, p.UID, p.GID} }
func (p brokerIdentity) peer() PeerIdentity          { return PeerIdentity{p.PID, p.UID, p.GID} }

type brokerClaim struct {
	Schema  string            `json:"schema"`
	Nonce   string            `json:"nonce"`
	Release ReleaseIdentity   `json:"release"`
	Peer    brokerIdentity    `json:"peer"`
	Request actionabi.Request `json:"request"`
}

type brokerStep struct {
	Schema string `json:"schema"`
	Nonce  string `json:"nonce"`
	Stage  string `json:"stage"`
}

func validBrokerClaim(c brokerClaim) bool {
	nonce, err := hex.DecodeString(c.Nonce)
	if err != nil || len(nonce) != 16 || hex.EncodeToString(nonce) != c.Nonce || c.Schema != brokerSchema || c.Release.Validate() != nil ||
		c.Peer.PID <= 1 || (c.Peer.UID == 0) != (c.Peer.GID == 0) || c.Peer.UID == ^uint32(0) || c.Peer.GID == ^uint32(0) {
		return false
	}
	// Root/root is the installed service mode. These numbers do NOT grant
	// authority: both ends match them to pinned SO_PEERCRED/SO_PEERPIDFD
	// identities and the installed systemd unit before binding the real job.
	_, err = prepare(c.Request, Peer{pid: c.Peer.PID, uid: c.Peer.UID, gid: c.Peer.GID, verified: true})
	return err == nil
}

// No bufio read-ahead: an extra frame must remain visible to the next state
// check, including the final EOF check. One small LF frame is bounded before
// JSON decoding; canonical bytes reject duplicates, aliases, nulls and unknowns.
func readBrokerFrame(r io.Reader, value any) error {
	var body []byte
	var one [1]byte
	for len(body) < maxBrokerFrame {
		if _, err := io.ReadFull(r, one[:]); err != nil {
			return ErrRequest
		}
		body = append(body, one[0])
		if one[0] != '\n' {
			continue
		}
		if len(body) < 3 || bytes.ContainsAny(body[:len(body)-1], "\r\n") || json.Unmarshal(body[:len(body)-1], value) != nil {
			return ErrRequest
		}
		canonical, err := json.Marshal(value)
		if err != nil || !bytes.Equal(canonical, body[:len(body)-1]) {
			return ErrRequest
		}
		return nil
	}
	return ErrRequest
}

func writeBrokerFrame(w io.Writer, value any) error {
	body, err := json.Marshal(value)
	if err != nil || len(body)+1 > maxBrokerFrame {
		return ErrRequest
	}
	if securefs.WriteAll(w, append(body, '\n')) != nil {
		return ErrUnavailable
	}
	return nil
}

// One exchange exclusively owns its socket, including deadlines. Wait for the
// cancellation callback before returning, so it cannot affect a later user of
// a descriptor. Caller disconnect is not the execution owner's cancellation.
func brokerDeadline(ctx context.Context, c *net.UnixConn, timeout time.Duration) (func(), error) {
	if ctx == nil || c == nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	deadline := time.Now().Add(timeout)
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	if c.SetDeadline(deadline) != nil {
		return nil, ErrUnavailable
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Now()); close(done) })
	return func() {
		if !stop() {
			<-done
		}
	}, nil
}

func readBrokerStep(ctx context.Context, c *net.UnixConn, nonce, stage string) error {
	stop, err := brokerDeadline(ctx, c, brokerIOTimeout)
	if err != nil {
		return err
	}
	defer stop()
	var step brokerStep
	if readBrokerFrame(c, &step) != nil || step != (brokerStep{brokerSchema, nonce, stage}) {
		return ErrRequest
	}
	return nil
}

// Only the authenticated execution owner calls this, after binding the real
// job and granting execution. Waiting for work to finish is not a three-second
// input handshake. A missing deadline is rejected, never an infinite wait.
func readBrokerFinished(ctx context.Context, c *net.UnixConn, nonce string) error {
	if ctx == nil {
		return ErrUnavailable
	}
	deadline, bounded := ctx.Deadline()
	if !bounded {
		return ErrUnavailable
	}
	stop, err := brokerDeadline(ctx, c, time.Until(deadline))
	if err != nil {
		return err
	}
	defer stop()
	var step brokerStep
	if readBrokerFrame(c, &step) != nil || step != (brokerStep{brokerSchema, nonce, "finished"}) {
		return ErrRequest
	}
	return nil
}

func writeBrokerStep(ctx context.Context, c *net.UnixConn, nonce, stage string) error {
	stop, err := brokerDeadline(ctx, c, brokerIOTimeout)
	if err != nil {
		return err
	}
	defer stop()
	return writeBrokerFrame(c, brokerStep{brokerSchema, nonce, stage})
}

func brokerEOF(ctx context.Context, c *net.UnixConn) error {
	stop, err := brokerDeadline(ctx, c, brokerIOTimeout)
	if err != nil {
		return err
	}
	defer stop()
	var one [1]byte
	if n, err := c.Read(one[:]); n != 0 || err != io.EOF {
		return ErrRequest
	}
	return nil
}

// remoteJobBinding is on the activated executor side. Its connection must be
// authenticated against the ORIGINAL requesting process, not just its UID.
// Construction is private; Activation supplies fixed installation identities.
type remoteJobBinding struct {
	mu    sync.Mutex
	conn  *net.UnixConn
	peer  PeerIdentity
	watch brokerProcess
	used  bool
}

func (b *remoteJobBinding) WithHostInvocation(ctx context.Context, request actionabi.Request, release ReleaseIdentity, peer PeerIdentity, run func(context.Context) error) error {
	if b == nil || ctx == nil || run == nil {
		return ErrUnavailable
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used || b.conn == nil || b.watch == nil || peer != b.peer || b.watch.alive() != nil {
		return ErrDenied
	}
	b.used = true
	ctx, cancel := context.WithTimeout(ctx, brokerExecutionTimeout(request.Action))
	defer cancel()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ErrUnavailable
	}
	claim := brokerClaim{brokerSchema, hex.EncodeToString(nonce[:]), release, brokerIdentityOf(peer), request}
	if !validBrokerClaim(claim) {
		return ErrDenied
	}
	stop, err := brokerDeadline(ctx, b.conn, brokerIOTimeout)
	if err != nil {
		return err
	}
	err = writeBrokerFrame(b.conn, claim)
	stop()
	if err != nil || readBrokerStep(ctx, b.conn, claim.Nonce, "bound") != nil || b.watch.alive() != nil || ctx.Err() != nil {
		return ErrDenied
	}
	// This callback is synchronous and always the compiled executor. Nothing
	// received from the broker can choose code, paths or action parameters.
	if err := run(ctx); err != nil {
		return ErrUnavailable // No finished acknowledgement on uncertain work.
	}
	if b.watch.alive() != nil || writeBrokerStep(ctx, b.conn, claim.Nonce, "finished") != nil || b.conn.CloseWrite() != nil ||
		readBrokerStep(ctx, b.conn, claim.Nonce, "validated") != nil || brokerEOF(ctx, b.conn) != nil || b.watch.alive() != nil || ctx.Err() != nil {
		return ErrUnavailable
	}
	return nil
}

func (b *remoteJobBinding) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	failed := false
	if b.conn != nil {
		failed = b.conn.Close() != nil
		b.conn = nil
	}
	if b.watch != nil {
		failed = b.watch.close() != nil || failed
		b.watch = nil
	}
	if failed {
		return ErrUnavailable
	}
	return nil
}
