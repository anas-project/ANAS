//go:build linux

package incusingresshost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// withInstalledNamespace is internal to the fixed root command executor. It
// derives the process only from installed scope data and independently rereads
// Docker and procfs. RouteNetNS is never opened as a pathname.
func withInstalledNamespace(ctx context.Context, config backendConfig, effect func() error) (result error) {
	if err := validateInstalledNamespacePin(config.Namespace); err != nil {
		return err
	}
	if err := verifyInstalledDockerNamespace(ctx, config); err != nil {
		return err
	}
	if err := verifyNamespaceProcess(config.Namespace); err != nil {
		return err
	}
	namespace, err := openKernelNetworkNamespace(strconv.Itoa(config.Namespace.PID))
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, namespace.Close()) }()
	if err := verifyNamespaceDescriptor(namespace, config.Namespace); err != nil {
		return err
	}
	if err := verifyNamespaceProcess(config.Namespace); err != nil {
		return err
	}
	result = inOpenedNetworkNamespace(ctx, namespace, config.Namespace.NetNSCookie, effect)
	// Check even on an effect failure. Unknown external effects retain their
	// durable intent; neither a successful command nor a stale PID is a receipt.
	return errors.Join(result, verifyNamespaceProcess(config.Namespace), verifyInstalledDockerNamespace(ctx, config), verifyProcessNamespace(config.Namespace))
}

func verifyInstalledDockerNamespace(ctx context.Context, config backendConfig) (result error) {
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: config.CommandTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+config.Namespace.DockerContainerID+"/json", nil)
	if err != nil {
		return fmt.Errorf("create installed Docker identity observation")
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("observe installed Docker identity")
	}
	defer func() {
		if response.Body.Close() != nil {
			result = errors.Join(result, fmt.Errorf("close installed Docker observation"))
		}
	}()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("installed Docker container is unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		return fmt.Errorf("installed Docker observation is incomplete")
	}
	return verifyDockerNamespaceDocument(body, config)
}

func readNamespaceProc(path string, limit int64) (body []byte, result error) {
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("installed process identity is unavailable")
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	var fs unix.Statfs_t
	if unix.Fstatfs(int(file.Fd()), &fs) != nil || fs.Type != unix.PROC_SUPER_MAGIC {
		return nil, fmt.Errorf("installed process identity requires procfs")
	}
	body, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(body) == 0 || int64(len(body)) > limit {
		return nil, fmt.Errorf("installed process identity is incomplete")
	}
	return body, nil
}

func verifyNamespaceProcess(pin RouteNamespacePin) error {
	boot, err := readNamespaceProc("/proc/sys/kernel/random/boot_id", 64)
	if err != nil || strings.TrimSpace(string(boot)) != pin.BootID {
		return fmt.Errorf("installed namespace boot identity changed")
	}
	stat, err := readNamespaceProc("/proc/"+strconv.Itoa(pin.PID)+"/stat", 8192)
	if err != nil {
		return err
	}
	ticks, err := processStartTicks(stat, pin.PID)
	if err != nil || ticks != pin.StartTimeTicks {
		return fmt.Errorf("installed namespace process incarnation changed")
	}
	return nil
}

func openKernelNetworkNamespace(process string) (*os.File, error) {
	// Following the kernel's ns magic link is intentional. A regular file,
	// directory or different namespace type is rejected by fstatfs and ioctl.
	file, err := os.Open("/proc/" + process + "/ns/net")
	if err != nil {
		return nil, fmt.Errorf("open kernel network namespace")
	}
	var fs unix.Statfs_t
	kind, kindErr := unix.IoctlRetInt(int(file.Fd()), unix.NS_GET_NSTYPE)
	if unix.Fstatfs(int(file.Fd()), &fs) != nil || fs.Type != unix.NSFS_MAGIC || kindErr != nil || kind != unix.CLONE_NEWNET {
		return nil, errors.Join(fmt.Errorf("not a kernel network namespace"), file.Close())
	}
	return file, nil
}

func verifyNamespaceDescriptor(file *os.File, pin RouteNamespacePin) error {
	var stat unix.Stat_t
	if file == nil || unix.Fstat(int(file.Fd()), &stat) != nil || uint64(stat.Dev) != pin.NetNSDevice || stat.Ino != pin.NetNSInode {
		return fmt.Errorf("installed kernel namespace identity changed")
	}
	return nil
}

func verifyProcessNamespace(pin RouteNamespacePin) (result error) {
	file, err := openKernelNetworkNamespace(strconv.Itoa(pin.PID))
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	return verifyNamespaceDescriptor(file, pin)
}

func networkNamespaceCookie() (cookie uint64, result error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return 0, fmt.Errorf("create namespace attestation socket")
	}
	defer func() { result = errors.Join(result, unix.Close(fd)) }()
	cookie, err = unix.GetsockoptUint64(fd, unix.SOL_SOCKET, unix.SO_NETNS_COOKIE)
	if err != nil || cookie == 0 {
		return 0, fmt.Errorf("kernel cannot attest network namespace cookie")
	}
	return cookie, nil
}

// Never return a thread to Go's scheduler with an unverified namespace. A
// restore failure leaves this dedicated goroutine locked; Go terminates that
// OS thread when the goroutine exits. No caller-supplied executable is involved.
func inOpenedNetworkNamespace(ctx context.Context, namespace *os.File, cookie uint64, effect func() error) error {
	if ctx == nil || namespace == nil || cookie == 0 || effect == nil {
		return fmt.Errorf("invalid opened namespace invocation")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		mayUnlock := true
		result := func() (result error) {
			defer func() {
				if recover() != nil {
					result = errors.Join(result, fmt.Errorf("namespace execution interrupted"))
				}
			}()
			original, err := openKernelNetworkNamespace("thread-self")
			if err != nil {
				return err
			}
			defer func() { result = errors.Join(result, original.Close()) }()
			before, err := original.Stat()
			if err != nil {
				return fmt.Errorf("inspect original namespace")
			}
			target, err := namespace.Stat()
			if err != nil {
				return fmt.Errorf("inspect opened target namespace")
			}
			switched := !os.SameFile(before, target)
			if switched {
				if err := unix.Setns(int(namespace.Fd()), unix.CLONE_NEWNET); err != nil {
					return fmt.Errorf("enter opened network namespace")
				}
				mayUnlock = false
			}
			defer func() {
				if switched && unix.Setns(int(original.Fd()), unix.CLONE_NEWNET) != nil {
					result = errors.Join(result, fmt.Errorf("restore original network namespace failed"))
					return
				}
				current, err := openKernelNetworkNamespace("thread-self")
				if err != nil {
					mayUnlock = false
					result = errors.Join(result, err)
					return
				}
				after, statErr := current.Stat()
				closeErr := current.Close()
				mayUnlock = statErr == nil && os.SameFile(before, after)
				if !mayUnlock {
					result = errors.Join(result, fmt.Errorf("original namespace readback failed"))
				}
				result = errors.Join(result, closeErr)
			}()
			observed, err := networkNamespaceCookie()
			if err != nil || observed != cookie {
				return fmt.Errorf("opened network namespace cookie does not match installation")
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return errors.Join(effect(), ctx.Err())
		}()
		if mayUnlock {
			runtime.UnlockOSThread()
		}
		done <- result
	}()
	// Join the worker even after cancellation. Returning before a possible
	// external effect has stopped would falsely make its lifetime appear over.
	return <-done
}
