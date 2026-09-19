package hostaction

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/incusprovision"
)

func TestBrokerInstalledRootIdentityReachesTheSameBoundInvocation(t *testing.T) {
	remote, session, process := brokerServerFixture(t)
	remote.peer.UID, remote.peer.GID = 0, 0
	session.self = remote.peer
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- session.Serve(ctx, installedRelease(), brokerRunBinding(), &memoryAudit{}) }()
	calls := 0
	err := remote.WithHostInvocation(ctx, testRequest(), installedRelease(), remote.peer, func(context.Context) error { calls++; return nil })
	_ = remote.Close()
	serverErr := <-done
	if err != nil || serverErr != nil || calls != 1 {
		t.Fatalf("installed root/root protocol failed: client=%v server=%v callbacks=%d", err, serverErr, calls)
	}
	if !errors.Is(session.Close(), ErrBrokerExecutorRunning) || process.closed.Load() {
		t.Fatal("completed handshake discarded the live root executor")
	}
}

func TestBrokerFinishedWaitUsesAuthenticatedActionBudgetNotHandshakeTimeout(t *testing.T) {
	remote, session, _ := brokerServerFixture(t)
	body, err := json.Marshal(IncusPlanParameters{Schema: parameterSchema, Request: incusprovision.Request{}})
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest()
	request.Action = ActionInstallPlan
	request.Parameters = body
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- session.Serve(ctx, installedRelease(), brokerRunBinding(), &memoryAudit{}) }()
	err = remote.WithHostInvocation(ctx, request, installedRelease(), remote.peer, func(owner context.Context) error {
		timer := time.NewTimer(brokerIOTimeout + 200*time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-owner.Done():
			return owner.Err()
		}
	})
	_ = remote.Close()
	if serverErr := <-done; err != nil || serverErr != nil {
		t.Fatalf("legitimate bounded action was cut off by the handshake timer: client=%v server=%v", err, serverErr)
	}
}
