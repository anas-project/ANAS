// Package installer contains the fixed Cloudflare mail-source installation
// steps. It does not enable routing or imply that mail delivery is working.
package installer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"
)

var (
	ErrConflict = errors.New("cloud resource conflict")
	ErrUnknown  = errors.New("cloud operation outcome unknown; inspect before recovery")
	ErrBusy     = errors.New("installation is already running")
	ErrJournal  = errors.New("installation journal invalid or inaccessible")
)

var accountID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var fingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)
var resourceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

// Spec contains no credential. Names and account cannot change during a run.
type Spec struct {
	AccountID string `json:"account_id"`
	Worker    string `json:"worker"`
	Bucket    string `json:"bucket"`
}

func (s Spec) valid() bool {
	return accountID.MatchString(s.AccountID) && resourceName.MatchString(s.Worker) && resourceName.MatchString(s.Bucket)
}

// Client must use create-only provider operations. An ErrConflict is a
// definitive rejection; other errors after issuance leave the result unknown.
type Client interface {
	CreateWorker(context.Context, string, string) (string, error)
	WorkerMatches(context.Context, string, string, string) (bool, error)
	CreateBucket(context.Context, string, string) (string, error)
	BucketMatches(context.Context, string, string, string) (bool, error)
}

type record struct {
	Schema            string `json:"schema"`
	Spec              Spec   `json:"spec"`
	Stage             string `json:"stage"`
	WorkerID          string `json:"worker_id,omitempty"`
	BucketFingerprint string `json:"bucket_fingerprint,omitempty"`
	OperationID       string `json:"operation_id,omitempty"`
	VersionDigest     string `json:"version_digest,omitempty"`
	VersionID         string `json:"version_id,omitempty"`
	EndpointOrigin    string `json:"endpoint_origin,omitempty"`
}

type Journal struct {
	file   *os.File
	dirfd  int
	state  record
	failed bool
}

func safeFile(fd int, directory bool) bool {
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 {
		return false
	}
	if directory {
		return st.Mode&unix.S_IFMT == unix.S_IFDIR
	}
	return st.Mode&unix.S_IFMT == unix.S_IFREG && st.Nlink == 1
}

// OpenJournal takes an exclusive process lock, and rejects a changed spec,
// unsafe path or partial record. The containing directory must already exist
// and belong to the current user with no group/other access.
func OpenJournal(path string, spec Spec) (*Journal, error) {
	if !filepath.IsAbs(path) || !spec.valid() || filepath.Base(path) == "." || filepath.Base(path) == ".." {
		return nil, ErrJournal
	}
	dir, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrJournal
	}
	defer unix.Close(dir)
	if !safeFile(dir, true) {
		return nil, ErrJournal
	}
	flags := unix.O_RDWR | unix.O_APPEND | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Openat(dir, filepath.Base(path), flags|unix.O_CREAT|unix.O_EXCL, 0600)
	created := err == nil
	if errors.Is(err, unix.EEXIST) {
		fd, err = unix.Openat(dir, filepath.Base(path), flags, 0)
	}
	if err != nil {
		return nil, ErrJournal
	}
	f := os.NewFile(uintptr(fd), filepath.Base(path))
	if !safeFile(fd, false) {
		f.Close()
		return nil, ErrJournal
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrBusy
		}
		return nil, ErrJournal
	}
	ownedDir, err := unix.Dup(dir)
	if err != nil {
		_ = unix.Flock(fd, unix.LOCK_UN)
		f.Close()
		return nil, ErrJournal
	}
	if _, err := unix.FcntlInt(uintptr(ownedDir), unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
		unix.Close(ownedDir)
		_ = unix.Flock(fd, unix.LOCK_UN)
		f.Close()
		return nil, ErrJournal
	}
	j := &Journal{file: f, dirfd: ownedDir}
	if created {
		j.state = record{Schema: "anas.mail-cloud-reservation/v1", Spec: spec, Stage: "prepared"}
		if err := j.append(j.state); err != nil || unix.Fsync(dir) != nil {
			j.Close()
			return nil, ErrJournal
		}
		return j, nil
	}
	if err := j.load(spec); err != nil {
		j.Close()
		return nil, err
	}
	if j.file.Sync() != nil || unix.Fsync(dir) != nil {
		j.Close()
		return nil, ErrJournal
	}
	return j, nil
}

func (j *Journal) load(spec Spec) error {
	if _, err := j.file.Seek(0, io.SeekStart); err != nil {
		return ErrJournal
	}
	data, err := io.ReadAll(io.LimitReader(j.file, 1<<20+1))
	if err != nil || len(data) == 0 || len(data) > 1<<20 || data[len(data)-1] != '\n' {
		return ErrUnknown
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var last record
	for scanner.Scan() {
		var current record
		dec := json.NewDecoder(strings.NewReader(scanner.Text()))
		dec.DisallowUnknownFields()
		if dec.Decode(&current) != nil || current.Schema != "anas.mail-cloud-reservation/v1" || current.Spec != spec || !validRecord(current) {
			return ErrJournal
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			return ErrJournal
		}
		if !validTransition(last, current) {
			return ErrJournal
		}
		last = current
	}
	if scanner.Err() != nil || last.Schema == "" {
		return ErrJournal
	}
	j.state = last
	return nil
}

func validTransition(previous, next record) bool {
	if previous.Schema == "" {
		return next.Stage == "prepared"
	}
	if previous.Spec != next.Spec {
		return false
	}
	switch previous.Stage {
	case "prepared":
		return next.Stage == "worker-issued"
	case "worker-issued":
		return next.Stage == "worker-created" || next.Stage == "worker-conflict"
	case "worker-created":
		return next.Stage == "bucket-issued" && next.WorkerID == previous.WorkerID
	case "bucket-issued":
		return (next.Stage == "bucket-conflict" || next.Stage == "reserved") && next.WorkerID == previous.WorkerID
	case "reserved":
		return next.Stage == "version-issued" && next.WorkerID == previous.WorkerID && next.BucketFingerprint == previous.BucketFingerprint
	case "version-issued":
		return next.Stage == "version-created" && next.WorkerID == previous.WorkerID && next.BucketFingerprint == previous.BucketFingerprint &&
			next.OperationID == previous.OperationID && next.VersionDigest == previous.VersionDigest
	case "version-created":
		return next.Stage == "version-verified" && next.WorkerID == previous.WorkerID && next.BucketFingerprint == previous.BucketFingerprint &&
			next.OperationID == previous.OperationID && next.VersionDigest == previous.VersionDigest && next.VersionID == previous.VersionID
	case "version-verified":
		return next.Stage == "schedule-issued" && next.WorkerID == previous.WorkerID && next.BucketFingerprint == previous.BucketFingerprint &&
			next.OperationID == previous.OperationID && next.VersionDigest == previous.VersionDigest && next.VersionID == previous.VersionID
	case "schedule-issued":
		return next.Stage == "schedule-verified" && next.WorkerID == previous.WorkerID && next.BucketFingerprint == previous.BucketFingerprint &&
			next.OperationID == previous.OperationID && next.VersionDigest == previous.VersionDigest && next.VersionID == previous.VersionID
	case "schedule-verified":
		return next.Stage == "endpoint-issued" && next.WorkerID == previous.WorkerID && next.BucketFingerprint == previous.BucketFingerprint && next.OperationID == previous.OperationID && next.VersionDigest == previous.VersionDigest && next.VersionID == previous.VersionID
	case "endpoint-issued":
		return next.Stage == "endpoint-configured" && next.WorkerID == previous.WorkerID && next.BucketFingerprint == previous.BucketFingerprint && next.OperationID == previous.OperationID && next.VersionDigest == previous.VersionDigest && next.VersionID == previous.VersionID && next.EndpointOrigin == previous.EndpointOrigin
	case "endpoint-configured":
		return next.Stage == "endpoint-verified" && next.WorkerID == previous.WorkerID && next.BucketFingerprint == previous.BucketFingerprint && next.OperationID == previous.OperationID && next.VersionDigest == previous.VersionDigest && next.VersionID == previous.VersionID && next.EndpointOrigin == previous.EndpointOrigin
	default:
		return false
	}
}

func validRecord(r record) bool {
	if r.Stage == "endpoint-issued" || r.Stage == "endpoint-configured" || r.Stage == "endpoint-verified" {
		if !validWorkerOrigin(r.Spec.Worker, r.EndpointOrigin) {
			return false
		}
	} else if r.EndpointOrigin != "" {
		return false
	}
	switch r.Stage {
	case "prepared", "worker-issued", "worker-conflict":
		return r.WorkerID == "" && r.BucketFingerprint == "" && r.OperationID == "" && r.VersionDigest == "" && r.VersionID == ""
	case "worker-created", "bucket-issued", "bucket-conflict":
		return accountID.MatchString(r.WorkerID) && r.BucketFingerprint == "" && r.OperationID == "" && r.VersionDigest == "" && r.VersionID == ""
	case "reserved":
		return accountID.MatchString(r.WorkerID) && fingerprint.MatchString(r.BucketFingerprint) && r.OperationID == "" && r.VersionDigest == "" && r.VersionID == ""
	case "version-issued":
		return accountID.MatchString(r.WorkerID) && fingerprint.MatchString(r.BucketFingerprint) && uuid.MatchString(r.OperationID) && fingerprint.MatchString(r.VersionDigest) && r.VersionID == ""
	case "version-created", "version-verified", "schedule-issued", "schedule-verified", "endpoint-issued", "endpoint-configured", "endpoint-verified":
		return accountID.MatchString(r.WorkerID) && fingerprint.MatchString(r.BucketFingerprint) && uuid.MatchString(r.OperationID) && fingerprint.MatchString(r.VersionDigest) && uuid.MatchString(r.VersionID)
	default:
		return false
	}
}

func (j *Journal) append(r record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return ErrJournal
	}
	data = append(data, '\n')
	if n, err := j.file.Write(data); err != nil || n != len(data) {
		j.failed = true
		return ErrJournal
	}
	if err := j.file.Sync(); err != nil {
		j.failed = true
		return ErrJournal
	}
	j.state = r
	return nil
}

func (j *Journal) Close() error {
	if j.file == nil {
		return nil
	}
	_ = unix.Flock(int(j.file.Fd()), unix.LOCK_UN)
	err := j.file.Close()
	j.file = nil
	if closeErr := unix.Close(j.dirfd); err == nil {
		err = closeErr
	}
	j.dirfd = -1
	return err
}

// ReserveResources prepares a Worker and an R2 bucket, but does not upload,
// deploy, configure routes or establish the health of a mail source. A lost
// provider response is never retried or adopted automatically.
func (j *Journal) ReserveResources(ctx context.Context, client Client) error {
	if j.file == nil || j.failed || client == nil {
		return ErrJournal
	}
	r := j.state
	if r.Stage == "worker-issued" || r.Stage == "bucket-issued" || r.Stage == "version-issued" || r.Stage == "schedule-issued" || r.Stage == "endpoint-issued" {
		return ErrUnknown
	}
	if r.Stage == "worker-conflict" || r.Stage == "bucket-conflict" {
		return ErrConflict
	}
	if r.Stage == "reserved" || r.Stage == "version-created" || r.Stage == "version-verified" || r.Stage == "schedule-verified" || r.Stage == "endpoint-configured" || r.Stage == "endpoint-verified" {
		return verifyReserved(ctx, client, r)
	}
	if r.Stage == "prepared" {
		r.Stage = "worker-issued"
		if err := j.append(r); err != nil {
			return err
		}
		id, err := client.CreateWorker(ctx, r.Spec.AccountID, r.Spec.Worker)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				r.Stage = "worker-conflict"
				if e := j.append(r); e != nil {
					return e
				}
				return ErrConflict
			}
			return ErrUnknown
		}
		if !accountID.MatchString(id) {
			return ErrUnknown
		}
		r.WorkerID, r.Stage = id, "worker-created"
		if err := j.append(r); err != nil {
			return err
		}
	}
	match, err := client.WorkerMatches(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker)
	if err != nil {
		return fmt.Errorf("worker readback unavailable: %w", ErrUnknown)
	}
	if !match {
		return ErrConflict
	}
	r.Stage = "bucket-issued"
	if err := j.append(r); err != nil {
		return err
	}
	bucketFingerprint, err := client.CreateBucket(ctx, r.Spec.AccountID, r.Spec.Bucket)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			r.Stage = "bucket-conflict"
			if e := j.append(r); e != nil {
				return e
			}
			return ErrConflict
		}
		return ErrUnknown
	}
	if !fingerprint.MatchString(bucketFingerprint) {
		return ErrUnknown
	}
	r.Stage, r.BucketFingerprint = "reserved", bucketFingerprint
	if err := j.append(r); err != nil {
		return err
	}
	return verifyReserved(ctx, client, r)
}

func verifyReserved(ctx context.Context, client Client, r record) error {
	worker, err := client.WorkerMatches(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker)
	if err != nil {
		return ErrUnknown
	}
	if !worker {
		return ErrConflict
	}
	bucket, err := client.BucketMatches(ctx, r.Spec.AccountID, r.Spec.Bucket, r.BucketFingerprint)
	if err != nil {
		return ErrUnknown
	}
	if !bucket {
		return ErrConflict
	}
	return nil
}
