//go:build !linux && !darwin

package runner

// Without a way to name the PID space, no writer can be looked up directly and
// every judgement falls back to the heartbeat.
func currentPIDSpace() (bootID, namespace string) { return "", "" }

func inspectProcess(int) processProbe { return processProbe{} }
