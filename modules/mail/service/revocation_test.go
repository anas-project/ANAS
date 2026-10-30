package main

import (
	"context"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type revocationFixture struct {
	task                store.RevocationTask
	claimErr, finishErr error
	finished, kicked    int
	success             bool
	code, session, box  string
	kickErr             error
}

func (f *revocationFixture) ExpireSessions(context.Context) error { return nil }
func (f *revocationFixture) ClaimRevocation(context.Context) (store.RevocationTask, error) {
	return f.task, f.claimErr
}
func (f *revocationFixture) FinishRevocation(_ context.Context, _ store.RevocationTask, success bool, code string) error {
	f.finished++
	f.success = success
	f.code = code
	return f.finishErr
}
func (f *revocationFixture) Kick(_ context.Context, session, box string) error {
	f.kicked++
	f.session = session
	f.box = box
	return f.kickErr
}

func TestRevocationWorkerPreservesFailureAndExactScope(t *testing.T) {
	for _, reason := range []string{"session", "mailbox", "dms-down", "database-before", "database-after", "newer-task"} {
		t.Run(reason, func(t *testing.T) {
			f := &revocationFixture{task: store.RevocationTask{ID: 1, Revision: 1, SessionID: fixtureAnchor}}
			switch reason {
			case "mailbox":
				f.task.MailboxID = webmailTestBox
			case "dms-down":
				f.kickErr = errors.New("not reachable")
			case "database-before":
				f.claimErr = errors.New("not available")
			case "database-after":
				f.finishErr = errors.New("not committed")
			case "newer-task":
				f.finishErr = store.ErrTaskChanged
			}
			found, err := revokeOne(t.Context(), f, f)
			if reason == "database-before" {
				if err == nil || found || f.kicked != 0 || f.finished != 0 {
					t.Fatal("database failure sent untracked withdrawal")
				}
				return
			}
			if !found || f.kicked != 1 || f.finished != 1 || f.session != fixtureAnchor || f.box != f.task.MailboxID {
				t.Fatal("withdrawal scope changed")
			}
			if reason == "dms-down" {
				if err != nil || f.success || f.code != "dms-unavailable" {
					t.Fatal("failed DMS withdrawal marked complete", err)
				}
			} else if reason == "database-after" || reason == "newer-task" {
				if err == nil {
					t.Fatal("uncommitted/newer task reported completed")
				}
			} else if err != nil || !f.success || f.code != "" {
				t.Fatal("valid withdrawal failed", err)
			}
		})
	}
}
