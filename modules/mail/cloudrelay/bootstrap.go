package cloudrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type bootstrapRequest struct {
	Schema string          `json:"schema"`
	Action string          `json:"action"`
	Config json.RawMessage `json:"config"`
}

func (bootstrapRequest) String() string   { return "[private bootstrap request]" }
func (bootstrapRequest) GoString() string { return "[private bootstrap request]" }

type bootstrapResponse struct {
	State string `json:"state"`
}
type bootstrapActions struct {
	publish  func(string) error
	activate func(context.Context, string) error
	inspect  func(context.Context, string) (string, error)
}

// Bootstrap executes only fixed operations against a fixed systemd unit. The
// administrator must install this binary and grant the SSH identity permission
// to invoke exactly `cloudrelay bootstrap`; the command accepts no paths or
// executable payloads. The private configuration travels only over stdin.
func Bootstrap(ctx context.Context, input io.Reader, output io.Writer) error {
	if os.Geteuid() != 0 {
		return ErrPublishConflict
	}
	return runBootstrap(ctx, input, output, "/run", bootstrapActions{publish: PublishSystemdFiles, activate: ActivateSystemdInstallation, inspect: InspectSystemdInstallation})
}

func runBootstrap(ctx context.Context, input io.Reader, output io.Writer, parent string, actions bootstrapActions) error {
	raw, err := io.ReadAll(io.LimitReader(input, 96<<10+1))
	if err != nil || len(raw) > 96<<10 || !exactObject(raw, "schema", "action", "config") {
		return ErrPublishConflict
	}
	defer clear(raw)
	var request bootstrapRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var extra any
	if dec.Decode(&request) != nil || dec.Decode(&extra) != io.EOF || request.Schema != "anas.mail-systemd-bootstrap/v1" || (request.Action != "publish" && request.Action != "activate" && request.Action != "inspect") {
		return ErrPublishConflict
	}
	defer clear(request.Config)
	cfg, err := decodeRuntimeConfig(request.Config)
	if err != nil {
		return ErrPublishConflict
	}
	// Canonical bytes ensure a retry with the same semantic configuration binds
	// exactly the same source even if the request's whitespace differs.
	config, err := json.Marshal(cfg)
	if err != nil {
		return ErrPublishConflict
	}
	defer clear(config)
	if ctx.Err() != nil {
		return ErrPublishUnknown
	}
	dir, err := os.MkdirTemp(parent, "anas-mail-bootstrap-")
	if err != nil {
		return ErrPublishUnknown
	}
	path := filepath.Join(dir, "runtime.json")
	cleanup := func() error {
		e := os.Remove(path)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return ErrPublishUnknown
		}
		if os.Remove(dir) != nil {
			return ErrPublishUnknown
		}
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = os.Remove(dir)
		return ErrPublishUnknown
	}
	if n, e := f.Write(config); e != nil || n != len(config) || f.Sync() != nil {
		f.Close()
		_ = cleanup()
		return ErrPublishUnknown
	}
	if f.Close() != nil {
		_ = cleanup()
		return ErrPublishUnknown
	}
	state := "unknown"
	switch request.Action {
	case "publish":
		if actions.publish == nil {
			err = ErrPublishUnknown
		} else {
			err = actions.publish(path)
			if err == nil {
				state = "published"
			}
		}
	case "activate":
		if actions.activate == nil {
			err = ErrPublishUnknown
		} else {
			err = actions.activate(ctx, path)
			if err == nil {
				state = "active-local-verified"
			}
		}
	case "inspect":
		if actions.inspect == nil {
			err = ErrPublishUnknown
		} else {
			state, err = actions.inspect(ctx, path)
			if err == nil && state != "inactive" && state != "active-local-verified" {
				err = ErrPublishUnknown
				state = "unknown"
			}
		}
	}
	if cleanup() != nil {
		return ErrPublishUnknown
	}
	if err != nil {
		if errors.Is(err, ErrPublishConflict) {
			state = "conflict"
		} else {
			state = "unknown"
		}
	}
	if e := json.NewEncoder(output).Encode(bootstrapResponse{State: state}); e != nil {
		return ErrPublishUnknown
	}
	return err
}
