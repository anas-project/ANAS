package main

import (
	"bytes"
	"sync"

	"github.com/anas-project/ANAS/internal/computeimage"
)

// Observe complete, bounded log records without retaining raw output. This
// does not authenticate logs or assert success; it only annotates failures.
type buildObservation struct {
	mu               sync.Mutex
	line             []byte
	dropping         bool
	stage            computeimage.BuildStage
	unverifiedSource bool
}

const maxBuildObservationLine = 4096

func (w *buildObservation) Write(body []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range body {
		if c == '\n' {
			if !w.dropping {
				w.observe()
			}
			clear(w.line)
			w.line = w.line[:0]
			w.dropping = false
			continue
		}
		if w.dropping {
			continue
		}
		if len(w.line) == maxBuildObservationLine {
			clear(w.line)
			w.line = w.line[:0]
			w.dropping = true
			continue
		}
		w.line = append(w.line, c)
	}
	return len(body), nil
}

func (w *buildObservation) observe() {
	// debootstrap can continue after this warning. Later hooks/packing and a
	// zero process exit must not turn that base system into a verified release.
	// The default recipe also has an earlier filesystem trust precondition.
	if bytes.HasPrefix(w.line, []byte("W: Cannot check Release signature; keyring file not available ")) {
		w.unverifiedSource = true
		return
	}
	for _, candidate := range []struct {
		text  string
		stage computeimage.BuildStage
	}{
		{`msg="Downloading source"`, computeimage.BuildStageDownload},
		{`msg="Managing repositories"`, computeimage.BuildStageRepositories},
		{`msg="Managing packages"`, computeimage.BuildStagePackages},
		{`msg="Running generator"`, computeimage.BuildStageFiles},
		{`msg="Running hooks"`, computeimage.BuildStageHooks},
		{`msg="Creating image"`, computeimage.BuildStagePacking},
		{`msg="Packing image"`, computeimage.BuildStagePacking},
	} {
		if bytes.Contains(w.line, []byte(candidate.text)) {
			w.stage = candidate.stage
			return
		}
	}
}

func (w *buildObservation) sourceWasUnverified() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.unverifiedSource
}

func (w *buildObservation) failure() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Ignore unterminated or overlong records; never expose their fragments.
	clear(w.line)
	w.line = nil
	if w.unverifiedSource {
		return computeimage.BuildUnverifiedSourceFailure()
	}
	return computeimage.BuildFailureAt(w.stage)
}
