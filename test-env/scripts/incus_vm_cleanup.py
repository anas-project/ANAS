"""Bounded cleanup helpers for the disposable Incus VM experiment owners.

Callbacks belong to the trusted experiment owner. QMP callbacks must verify
the exact VM name before sending a command; no names/paths come from a guest.
These helpers do not grant permissions or touch production Docker resources.
"""
import errno
import math
import socket


def require_no_loopback_listeners(ports):
    """TIME_WAIT after tunnel shutdown is not evidence of a live listener.

    Only a refused connection proves absence. Timeout/unreachable/error remains
    unknown; never reinterpret it as successful cleanup or bind over a service.
    """
    for port in ports:
        if type(port) is not int or not 1024 <= port <= 65535:
            raise ValueError('invalid experimental loopback port')
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as connection:
            connection.settimeout(1)
            if connection.connect_ex(('127.0.0.1', port)) != errno.ECONNREFUSED:
                raise RuntimeError('experimental listener absence is unverified')


def finalize_vm(qemu, shutdown, quit_vm, *, stop_tunnel, after_exit, baseline,
                shutdown_timeout=90, quit_timeout=20):
    """Attempt every independent cleanup, retaining actual child wait status.

    A tunnel/port failure must not skip VM shutdown. Forced QMP quit is recorded
    separately from graceful shutdown, even if QEMU's eventual exit code is 0.
    The owner must persist this result before raising its final gate failure.
    """
    for timeout in (shutdown_timeout, quit_timeout):
        if type(timeout) not in (int, float) or not math.isfinite(timeout) or not 0 < timeout <= 180:
            raise ValueError('cleanup deadlines must be positive and bounded')
    result = {'normal_shutdown_requested': False, 'forced_quit_requested': False,
              'qemu_exit': None, 'cleanup_errors': []}

    def attempt(stage, operation):
        try:
            operation()
            return True
        except Exception as error:
            # Error messages can contain paths, commands or private replies.
            result['cleanup_errors'].append({'stage': stage, 'error_type': type(error).__name__})
            return False

    attempt('stop_tunnel', stop_tunnel)

    def graceful_stop():
        if qemu.poll() is None:
            shutdown()  # Exact QMP identity is checked by the caller here.
            result['normal_shutdown_requested'] = True
        result['qemu_exit'] = qemu.wait(timeout=shutdown_timeout)

    if not attempt('normal_shutdown', graceful_stop):
        def forced_stop():
            if qemu.poll() is None:
                quit_vm()  # Does not bypass the same exact identity check.
                result['forced_quit_requested'] = True
            result['qemu_exit'] = qemu.wait(timeout=quit_timeout)
        attempt('forced_quit', forced_stop)
    if result['qemu_exit'] is not None:
        attempt('resource_absence', after_exit)
    # This is a read-only physical-host comparison and can run even if exit
    # remains unknown. Its success never substitutes for process reaping.
    attempt('host_baseline', baseline)
    result['cleanup_passed'] = (not result['cleanup_errors'] and result['qemu_exit'] == 0 and
                                result['normal_shutdown_requested'] and not result['forced_quit_requested'])
    return result
