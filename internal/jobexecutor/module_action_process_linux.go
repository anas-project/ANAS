//go:build linux

package jobexecutor

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"golang.org/x/sys/unix"
)

const (
	moduleActionDrainTimeout = 2 * time.Second
	moduleActionKillTimeout  = 5 * time.Second
	moduleActionStderrLimit  = 64 << 10
)

// runModuleActionProcess never inherits the caller's environment or exposes
// stderr. FD 3 is the sealed, digest-checked ELF; FD 4 is a cancellation pipe.
// EOF on FD 4 requests cooperative cancellation. The executor must explicitly
// acknowledge it in its terminal frame; EOF on stdin only ends the request.
func runModuleActionProcess(daemonContext context.Context, definition ModuleActionDefinition, program *moduleActionProgram, request actionabi.Request, options ActionRecorderOptions, cancelRequest <-chan struct{}) (consolejobs.Job, error) {
	// Pdeathsig refers to the creating OS thread, not the Go goroutine. Keep
	// that thread alive until supervision has finished. This is not a sandbox:
	// trusted executors must not daemonize or escape the owned process group.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	unknown := func(cause error) (consolejobs.Job, error) {
		ctx, cancel := context.WithTimeout(context.Background(), terminalWriteTimeout)
		defer cancel()
		code := "execution_unconfirmed"
		if errors.Is(cause, ErrModuleActionContainment) {
			code = consolejobs.ActionContainmentCode
		}
		job, err := options.Store.CompleteActionObserved(ctx, options.Lease, actionabi.Event{
			ABI: actionabi.Version, JobID: request.JobID, InvocationID: request.InvocationID, Type: "error",
			Error: &actionabi.Failure{Outcome: actionabi.Unknown, Code: code, Message: "Action completion could not be confirmed"},
		}, options.Observer)
		return job, errors.Join(cause, err)
	}
	if !unprivilegedModuleActionProcess() {
		return unknown(ErrModuleActionUnavailable)
	}
	frame, err := actionabi.EncodeRequest(request)
	if err != nil {
		return unknown(ErrModuleActionParameters)
	}
	// Refuse unsupported kernels before launching anything. PidFD from clone
	// is checked again below; do not replace it with a reusable numeric PID.
	probeFD, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return unknown(ErrModuleActionUnavailable)
	}
	_ = unix.Close(probeFD)
	proc, err := openModuleActionProc()
	if err != nil {
		return unknown(ErrModuleActionUnavailable)
	}
	defer proc.Close()

	var files []*os.File
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	pipe := func() (*os.File, *os.File, error) {
		r, w, err := os.Pipe()
		if err == nil {
			files = append(files, r, w)
		}
		return r, w, err
	}
	input, inputWriter, err := pipe()
	if err != nil {
		return unknown(ErrModuleActionUnavailable)
	}
	output, outputWriter, err := pipe()
	if err != nil {
		return unknown(ErrModuleActionUnavailable)
	}
	stderr, stderrWriter, err := pipe()
	if err != nil {
		return unknown(ErrModuleActionUnavailable)
	}
	cancellation, cancellationWriter, err := pipe()
	if err != nil {
		return unknown(ErrModuleActionUnavailable)
	}
	options.Output = output
	recorder, err := NewActionRecorder(options)
	if err != nil {
		return unknown(ErrModuleActionUnavailable)
	}

	pidFD := -1
	ctx, cancel := context.WithTimeout(daemonContext, definition.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3")
	// CommandContext guards Start with the daemon-owned action deadline.
	// The pidfd/group supervisor below owns cancellation and reaping; the
	// default Cancel would kill only the leader and race that supervision.
	cmd.Cancel = nil
	cmd.Args = []string{"anas-module-action"}
	// Go performs chdir BEFORE remapping ExtraFiles. At this point the child
	// still has the parent's descriptor numbers; using child FD 4 here would
	// select the wrong file. The opened directory stays pinned through Start.
	cmd.Dir = "/proc/self/fd/" + strconv.FormatUint(uint64(program.directory.Fd()), 10)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "ANAS_ACTION_ABI=" + actionabi.Version,
		"ANAS_ACTION_CANCEL_FD=4", "ANAS_ACTION_CANCELLABLE=" + definition.Cancellable}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, outputWriter, stderrWriter
	cmd.ExtraFiles = []*os.File{program.image, cancellation}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL, PidFD: &pidFD}
	if ctx.Err() != nil {
		return unknown(ErrModuleActionExecution)
	}
	if err := cmd.Start(); err != nil {
		return unknown(ErrModuleActionUnavailable)
	}
	_ = input.Close()
	_ = outputWriter.Close()
	_ = stderrWriter.Close()
	_ = cancellation.Close()
	if pidFD >= 0 {
		defer unix.Close(pidFD)
	}

	inputDone := make(chan error, 1)
	outputDone := make(chan error, 1)
	stderrDone := make(chan error, 1)
	go func() {
		n, err := inputWriter.Write(frame)
		if err == nil && n != len(frame) {
			err = io.ErrShortWrite
		}
		closeErr := inputWriter.Close()
		inputDone <- errors.Join(err, closeErr)
	}()
	go func() {
		for {
			if err := recorder.Next(ctx); err != nil {
				outputDone <- err
				return
			}
		}
	}()
	go func() {
		// No stderr bytes are persisted, including on process start/exit errors.
		n, err := io.Copy(io.Discard, io.LimitReader(stderr, moduleActionStderrLimit+1))
		if n > moduleActionStderrLimit {
			err = ErrModuleActionExecution
		}
		stderrDone <- err
	}()

	var drain, grace, kill *time.Timer
	var drainC, graceC, killC <-chan time.Time
	defer func() {
		for _, timer := range []*time.Timer{drain, grace, kill} {
			if timer != nil {
				timer.Stop()
			}
		}
	}()
	forced, cancelled, exited, groupEmpty := false, false, false, false
	var lastGroupCheck time.Time
	inputFinished, outputFinished, stderrFinished := false, false, false
	force := func() {
		if forced {
			return
		}
		forced = true
		// No Wait has run yet: even an exited leader is still waitable, so its
		// process-group ID cannot be reused while we signal that group.
		_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
		_ = cmd.Process.Kill()
		cancel()
		_ = inputWriter.Close()
		_ = cancellationWriter.Close()
		_ = output.Close()
		_ = stderr.Close()
		kill = time.NewTimer(moduleActionKillTimeout)
		killC = kill.C
	}
	if pidFD < 0 {
		force()
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	contextDone := ctx.Done()
	for !(exited && groupEmpty && inputFinished && outputFinished && stderrFinished) {
		select {
		case err := <-inputDone:
			inputFinished, inputDone = true, nil
			if err != nil {
				force()
			}
		case err := <-outputDone:
			outputFinished, outputDone = true, nil
			if err != io.EOF {
				force()
			}
		case err := <-stderrDone:
			stderrFinished, stderrDone = true, nil
			if err != nil {
				force()
			}
		case <-cancelRequest:
			cancelRequest = nil
			if forced {
				continue
			}
			cancelled = true
			if err := cancellationWriter.Close(); err != nil {
				force()
			}
			grace = time.NewTimer(definition.CancelGrace)
			graceC = grace.C
		case <-contextDone:
			contextDone = nil
			force()
		case <-graceC:
			graceC = nil
			force()
		case <-drainC:
			drainC = nil
			force()
		case <-ticker.C:
			if exited {
				if !groupEmpty && time.Since(lastGroupCheck) >= 200*time.Millisecond {
					checkContext, checkCancel := context.WithTimeout(context.Background(), time.Second)
					alive, checkErr := moduleActionGroupAlive(checkContext, proc, cmd.Process.Pid)
					checkCancel()
					lastGroupCheck = time.Now()
					if checkErr != nil {
						force()
					} else {
						groupEmpty = !alive
					}
				}
				continue
			}
			if pidFD < 0 {
				continue
			}
			fds := []unix.PollFd{{Fd: int32(pidFD), Events: unix.POLLIN}}
			_, err := unix.Poll(fds, 0)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err != nil || fds[0].Revents&(unix.POLLERR|unix.POLLNVAL|unix.POLLHUP) != 0 {
				force()
				continue
			}
			if fds[0].Revents&unix.POLLIN != 0 {
				exited = true
				drain = time.NewTimer(moduleActionDrainTimeout)
				drainC = drain.C
			}
		case <-killC:
			// Do not race a stuck recorder/store writer with another terminal
			// write: it may still own the store's execution-lease mutex. Leave
			// the durable job running when the writer has not stopped; only
			// verified recovery may append unknown. In both cases admission
			// stops, and no later signal is sent to a possibly reused group ID.
			go func() { _ = cmd.Wait() }()
			if !outputFinished {
				return options.Job, ErrModuleActionContainment
			}
			return unknown(ErrModuleActionContainment)
		}
	}
	// With file descriptors (not io.Writer adapters) on all standard streams,
	// Wait has no os/exec copy goroutines to await. Poll established exit while
	// leaving the leader unreaped for all preceding group signals.
	waitErr := cmd.Wait()
	// Closed stdout/stderr and an exited leader do not imply its descendants
	// exited. After reaping, only QUERY the group: sending a signal here could
	// hit an unrelated process after ID reuse. Any surviving/unknown group
	// prevents a successful outcome and requires execution-owner recovery.
	if !moduleActionGroupGone(cmd.Process.Pid, moduleActionKillTimeout) {
		return unknown(ErrModuleActionContainment)
	}
	exit := actionabi.ExitState{Forced: forced, CancelRequested: cancelled, ExitCode: -1}
	if cmd.ProcessState != nil {
		exit.ProcessExited = true
		exit.ExitCode = cmd.ProcessState.ExitCode()
	}
	if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			exit.Forced = true
		}
	}
	finishContext, finishCancel := context.WithTimeout(context.Background(), terminalWriteTimeout)
	defer finishCancel()
	job, err := recorder.Finish(finishContext, exit)
	if err != nil {
		return job, errors.Join(ErrModuleActionExecution, err)
	}
	return job, nil
}

func moduleActionGroupGone(groupID int, timeout time.Duration) bool {
	if groupID <= 1 || timeout <= 0 {
		return false
	}
	deadline := time.Now().Add(timeout)
	for {
		err := unix.Kill(-groupID, 0)
		if errors.Is(err, unix.ESRCH) {
			return true
		}
		if err != nil && !errors.Is(err, unix.EINTR) {
			return false
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}
