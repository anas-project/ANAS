//go:build linux

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// applyBridge runs the plan, having first made sure it can.
//
// The order matters: everything that can be decided or read unprivileged is
// done before any capability is raised, so the privileged window contains only
// the ip(8) invocations themselves.
func applyBridge(request bridgeRequest) error {
	state, err := readHostState(request.Name)
	if err != nil {
		return err
	}
	if request.Down && !state.InterfaceExists {
		return nil
	}
	commands := bridgeCommands(request, state)
	if len(commands) == 0 {
		return nil
	}
	ip, err := findIP()
	if err != nil {
		return err
	}
	if err := raiseNetAdmin(); err != nil {
		return err
	}
	for _, args := range commands {
		if err := runIP(ip, args); err != nil {
			return err
		}
	}
	return nil
}

func readHostState(name string) (hostState, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		// Any lookup failure is "not there": the operations that follow either
		// create it or are skipped, and neither needs to know why.
		return hostState{}, nil
	}
	state := hostState{InterfaceExists: true}
	addrs, err := iface.Addrs()
	if err != nil {
		return state, fmt.Errorf("read addresses of %s: %w", name, err)
	}
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.To4() == nil {
			continue
		}
		ones, _ := ipnet.Mask.Size()
		state.Addresses = append(state.Addresses, fmt.Sprintf("%s/%d", ipnet.IP.String(), ones))
	}
	return state, nil
}

// ipCandidates is where iproute2 is installed, in the order the distributions
// that ship it use.
//
// The program is looked up here rather than on PATH because it is about to run
// with CAP_NET_ADMIN. PATH comes from whoever invoked this binary, which is
// precisely the party whose privileges this design is trying not to widen.
var ipCandidates = []string{"/sbin/ip", "/usr/sbin/ip", "/bin/ip", "/usr/bin/ip"}

func findIP() (string, error) {
	for _, candidate := range ipCandidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("iproute2 is not installed: none of %s exists", strings.Join(ipCandidates, ", "))
}

// runIP executes one ip(8) invocation with an environment of this program's
// choosing rather than the caller's.
func runIP(ip string, args []string) error {
	cmd := exec.Command(ip, args...)
	cmd.Env = []string{"PATH=/sbin:/usr/sbin:/bin:/usr/bin", "LC_ALL=C"}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ip %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

const (
	capNetAdmin             = 12
	prCapAmbient            = 47
	prCapAmbientRaise       = 2
	linuxCapabilityVersion3 = 0x20080522
)

// capHeader and capData are the kernel's capget/capset arguments, declared here
// because the standard library does not expose them and the one package that
// does -- golang.org/x/sys -- is not otherwise a dependency of this module. The
// layout is kernel UAPI (<linux/capability.h>) and so is fixed; version 3 takes
// two data words.
type capHeader struct {
	version uint32
	pid     int32
}

type capData struct {
	effective   uint32
	permitted   uint32
	inheritable uint32
}

func capget(header *capHeader, data *[2]capData) error {
	_, _, errno := syscall.Syscall(syscall.SYS_CAPGET,
		uintptr(unsafe.Pointer(header)), uintptr(unsafe.Pointer(&data[0])), 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func capset(header *capHeader, data *[2]capData) error {
	_, _, errno := syscall.Syscall(syscall.SYS_CAPSET,
		uintptr(unsafe.Pointer(header)), uintptr(unsafe.Pointer(&data[0])), 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// raiseNetAdmin makes CAP_NET_ADMIN survive into ip(8).
//
// A file capability lands in this process's permitted and effective sets, and
// stops there: execve clears the ambient set, so a child gets nothing. That is
// the difference between this and systemd's AmbientCapabilities=, which does
// propagate -- and the reason this dance exists is that the same binary has to
// work from an interactive `anas apply` as well as from a unit.
//
// Running as root needs none of it: uid 0 already passes everything on.
func raiseNetAdmin() error {
	if os.Geteuid() == 0 {
		return nil
	}
	header := capHeader{version: linuxCapabilityVersion3}
	var data [2]capData
	if err := capget(&header, &data); err != nil {
		return fmt.Errorf("read this process's capabilities: %w", err)
	}
	index, mask := capNetAdmin/32, uint32(1)<<(capNetAdmin%32)
	if data[index].permitted&mask == 0 {
		return fmt.Errorf("this command needs CAP_NET_ADMIN and does not have it. " +
			"Install it with `sudo setcap cap_net_admin+ep <path to anas-helper>`, " +
			"grant it through a systemd unit's AmbientCapabilities=, or run as root")
	}
	// Ambient can only be raised for a capability that is both permitted and
	// inheritable, and a file capability sets neither of the latter.
	data[index].inheritable |= mask
	if err := capset(&header, &data); err != nil {
		return fmt.Errorf("make CAP_NET_ADMIN inheritable: %w", err)
	}
	if _, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, prCapAmbient, prCapAmbientRaise, capNetAdmin, 0, 0, 0); errno != 0 {
		return fmt.Errorf("raise CAP_NET_ADMIN into the ambient set: %w", errno)
	}
	return nil
}
