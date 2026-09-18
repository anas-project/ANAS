package actionabi

import "io"

// ReadModuleCancellation waits for the dedicated, inherited Module executor
// cancellation pipe to reach EOF. A successful return means a cancellation
// REQUEST, not that the action has rolled back or reached a safe point. Only
// the executor can acknowledge that with a cancelled terminal and clean exit.
//
// The Linux Module adapter supplies this pipe at ANAS_ACTION_CANCEL_FD=4.
// Never call this on request stdin, stdout, a browser connection or a host
// action socket: closing those streams does not request cancellation. This
// function does not inspect the environment, open descriptors, start a
// goroutine, or cancel an application context. The executor owns the reader
// and must close it when the action finishes so a waiting goroutine can exit.
// Any byte, read failure or non-progressing reader is a protocol failure, NOT
// a cancellation acknowledgement. The raw I/O error is intentionally hidden.
func ReadModuleCancellation(reader io.Reader) error {
	if reader == nil {
		return ErrProtocol
	}
	var buffer [1]byte
	for emptyReads := 0; emptyReads < 100; emptyReads++ {
		n, err := reader.Read(buffer[:])
		if n != 0 {
			return ErrProtocol
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return ErrProtocol
		}
	}
	return ErrProtocol
}
