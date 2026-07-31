//go:build linux || darwin

package runner

import (
	"syscall"
	"unsafe"
)

// isTerminal asks the terminal driver rather than looking at the file type.
//
// The obvious test — os.ModeCharDevice on stdin — is wrong, and wrong in the
// direction that matters. /dev/null is a character device, so a command run as
// `anas ... < /dev/null` looks interactive under that test: it prints a prompt
// nobody will answer and then fails on EOF with a confusing message, instead of
// returning the contract's exit code 3 straight away. A terminal is the thing
// that answers the termios ioctl, so that is what gets asked. Anything else
// returns ENOTTY.
func isTerminal(fd uintptr) bool {
	var termios [64]byte
	_, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL, fd, ioctlReadTermios,
		uintptr(unsafe.Pointer(&termios[0])), 0, 0, 0)
	return errno == 0
}
