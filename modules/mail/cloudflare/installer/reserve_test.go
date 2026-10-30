// TEST_CASES: MAILMOD-T-026
package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCloud struct {
	workerCreates, bucketCreates, workerReads, bucketReads int
	workerErr, bucketErr, readErr                          error
	workerOK, bucketOK                                     bool
}

func (f *fakeCloud) CreateWorker(context.Context, string, string) (string, error) {
	f.workerCreates++
	return strings.Repeat("b", 32), f.workerErr
}
func (f *fakeCloud) WorkerMatches(context.Context, string, string, string) (bool, error) {
	f.workerReads++
	return f.workerOK, f.readErr
}
func (f *fakeCloud) CreateBucket(context.Context, string, string) (string, error) {
	f.bucketCreates++
	return strings.Repeat("c", 64), f.bucketErr
}
func (f *fakeCloud) BucketMatches(context.Context, string, string, string) (bool, error) {
	f.bucketReads++
	return f.bucketOK, f.readErr
}

func fixture(t *testing.T) (string, Spec) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "journal.jsonl"), Spec{AccountID: strings.Repeat("a", 32), Worker: "anas-worker", Bucket: "anas-bucket"}
}

func TestReservationIsDurableAndRechecksResources(t *testing.T) {
	path, spec := fixture(t)
	cloud := &fakeCloud{workerOK: true, bucketOK: true}
	j, err := OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.ReserveResources(t.Context(), cloud); err != nil {
		t.Fatal(err)
	}
	if cloud.workerCreates != 1 || cloud.bucketCreates != 1 {
		t.Fatal("unexpected resource creation counts", cloud)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if err = j.ReserveResources(t.Context(), cloud); err != nil {
		t.Fatal(err)
	}
	if cloud.workerCreates != 1 || cloud.bucketCreates != 1 || cloud.bucketReads != 2 {
		t.Fatal("recovery recreated resources or skipped readback", cloud)
	}
	cloud.bucketOK = false
	if !errors.Is(j.ReserveResources(t.Context(), cloud), ErrConflict) {
		t.Fatal("changed bucket accepted")
	}
}

func TestLostCreateReplyNeverRetriesOrAdopts(t *testing.T) {
	for _, stage := range []string{"worker", "bucket"} {
		t.Run(stage, func(t *testing.T) {
			path, spec := fixture(t)
			cloud := &fakeCloud{workerOK: true, bucketOK: true}
			if stage == "worker" {
				cloud.workerErr = errors.New("simulated lost response")
			} else {
				cloud.bucketErr = errors.New("simulated lost response")
			}
			j, err := OpenJournal(path, spec)
			if err != nil {
				t.Fatal(err)
			}
			if !errors.Is(j.ReserveResources(t.Context(), cloud), ErrUnknown) {
				t.Fatal("missing reply was accepted")
			}
			if err = j.Close(); err != nil {
				t.Fatal(err)
			}
			j, err = OpenJournal(path, spec)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			cloud.workerErr, cloud.bucketErr = nil, nil
			beforeWorker, beforeBucket := cloud.workerCreates, cloud.bucketCreates
			if !errors.Is(j.ReserveResources(t.Context(), cloud), ErrUnknown) || cloud.workerCreates != beforeWorker || cloud.bucketCreates != beforeBucket {
				t.Fatal("unknown create was retried")
			}
		})
	}
}

func TestReservationRejectsConflictsAndConcurrentAccess(t *testing.T) {
	path, spec := fixture(t)
	cloud := &fakeCloud{workerOK: true, bucketOK: true, bucketErr: ErrConflict}
	j, err := OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenJournal(path, spec); !errors.Is(err, ErrBusy) {
		t.Fatal("concurrent installer accepted", err)
	}
	if !errors.Is(j.ReserveResources(t.Context(), cloud), ErrConflict) {
		t.Fatal("bucket conflict not retained")
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	cloud.bucketErr = nil
	if !errors.Is(j.ReserveResources(t.Context(), cloud), ErrConflict) || cloud.workerCreates != 1 || cloud.bucketCreates != 1 {
		t.Fatal("conflicting resource was reused")
	}
}

func TestReadbackOutageResumesFromConfirmedWorker(t *testing.T) {
	path, spec := fixture(t)
	cloud := &fakeCloud{workerOK: true, bucketOK: true, readErr: errors.New("provider temporarily unavailable")}
	j, err := OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(j.ReserveResources(t.Context(), cloud), ErrUnknown) || cloud.workerCreates != 1 || cloud.bucketCreates != 0 {
		t.Fatal("unavailable readback allowed bucket creation", cloud)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	cloud.readErr = nil
	j, err = OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if err = j.ReserveResources(t.Context(), cloud); err != nil || cloud.workerCreates != 1 || cloud.bucketCreates != 1 {
		t.Fatal("confirmed worker was not reused safely", err, cloud)
	}
}

func TestReservationFailsClosedOnPartialJournalOrUnsafePath(t *testing.T) {
	path, spec := fixture(t)
	j, err := OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(`{"stage":"worker-issued"`); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenJournal(path, spec); !errors.Is(err, ErrUnknown) {
		t.Fatal("partial journal accepted", err)
	}
	other := filepath.Join(filepath.Dir(path), "link")
	if err = os.Symlink(path, other); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenJournal(other, spec); !errors.Is(err, ErrJournal) {
		t.Fatal("symlink accepted", err)
	}
}
