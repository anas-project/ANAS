#!/usr/bin/env python3
# TEST_CASES: MAILMOD-T-072, MAILMOD-T-073, MAILMOD-T-074, MAILMOD-T-076, MAILMOD-T-081
"""Exercise a restricted OSA SSH fixture from a separate NAS over SSH tunnels.

The fixture owns ephemeral keys. They exist only in memory on the controller and
in mode-0600 files in the NAS test directory, never in output or command args.
Closing the fixture's stdin tears down its listener and mount namespace.
"""
import argparse
import io
import os
import queue
import re
import socket
import subprocess
import sys
import tarfile
import threading
import time
from pathlib import Path

TARGET = re.compile(r"^[a-z_][a-z0-9_-]*@[a-zA-Z0-9.-]+$")
ROOT = re.compile(r"^/tmp/anas-access-isolated\.[A-Za-z0-9]+$")
SSH = ["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=15"]


def tar_bytes(items):
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w") as tf:
        for name, content, mode in items:
            info = tarfile.TarInfo(name)
            info.size = len(content)
            info.mode = mode
            tf.addfile(info, io.BytesIO(content))
    return buf.getvalue()


def check(args, *, data=None, timeout=30):
    return subprocess.run(args, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True, timeout=timeout).stdout


def allocate_local_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def wait_process_port(proc, probe, timeout=15):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if proc.poll() is not None:
            raise RuntimeError("SSH forwarding stopped before becoming ready")
        try:
            probe()
            return
        except (OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired):
            time.sleep(0.2)
    raise RuntimeError("SSH forwarding did not become ready")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--vps-target", required=True)
    parser.add_argument("--nas-target", required=True)
    parser.add_argument("--daemon-bin", required=True)
    parser.add_argument("--ssh-test-bin", required=True)
    parser.add_argument("--host-test-bin", required=True)
    parser.add_argument("--admin-service-test-bin", required=True)
    args = parser.parse_args()
    if not TARGET.fullmatch(args.vps_target) or not TARGET.fullmatch(args.nas_target):
        raise ValueError("invalid SSH target")
    fixture = Path(__file__).with_name("mail-bootstrap-access-isolated.sh").read_bytes()
    daemon = Path(args.daemon_bin).read_bytes()
    test_binary = Path(args.ssh_test_bin).read_bytes()
    host_test_binary = Path(args.host_test_bin).read_bytes()
    service_test_binary = Path(args.admin_service_test_bin).read_bytes()
    forward = os.environ.get("MAIL_VPS_ADMIN_FORWARD") == "1"
    fixture_tar = tar_bytes([("cloudrelay", daemon, 0o700), ("client-test", test_binary, 0o700), ("fixture.sh", fixture, 0o700)])
    command = ("d=$(mktemp -d /tmp/anas-access-run.XXXXXXXX) || exit 1; "
               "trap 'rm -rf \"$d\"' EXIT; tar -xf - -C \"$d\" || exit 1; "
               "chmod 700 \"$d/cloudrelay\" \"$d/client-test\" \"$d/fixture.sh\"; "
               "sudo -n env ANAS_ACCESS_REAL_PAM=1 ANAS_ACCESS_CROSSHOST=1 " + ("ANAS_ACCESS_FORWARD=1 " if forward else "") +
               "ANAS_ACCESS_SSH_TEST_BIN=\"$d/client-test\" \"$d/fixture.sh\" \"$d/cloudrelay\"")
    server = subprocess.Popen(SSH + [args.vps_target, command], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    lines = queue.Queue()
    def reader():
        for line in iter(server.stdout.readline, b""):
            lines.put(line)
        lines.put(None)
    thread = threading.Thread(target=reader, daemon=True)
    thread.start()
    local_forward = None
    nas_forward = None
    ok = False
    try:
        server.stdin.write(fixture_tar)
        server.stdin.flush()
        end = time.monotonic() + 90
        port = None
        base = None
        while time.monotonic() < end:
            try:
                line = lines.get(timeout=1)
            except queue.Empty:
                continue
            if line is None:
                raise RuntimeError("VPS fixture stopped before cross-host test")
            sys.stdout.buffer.write(line)
            sys.stdout.buffer.flush()
            match = re.fullmatch(rb"CROSSHOST_READY ([0-9]+) (/tmp/anas-access-isolated\.[A-Za-z0-9]+)\n", line)
            if match:
                port = int(match.group(1))
                base = match.group(2).decode("ascii")
                break
        if port is None or not ROOT.fullmatch(base):
            raise RuntimeError("fixture readiness not observed")
        private_key = check(SSH + [args.vps_target, "sudo -n cat " + base + "/client"])
        host_key = check(SSH + [args.vps_target, "sudo -n cat " + base + "/hostkey.pub"])
        local_port = allocate_local_port()
        local_forward = subprocess.Popen(SSH + ["-N", "-o", "ExitOnForwardFailure=yes", "-L", f"127.0.0.1:{local_port}:127.0.0.1:{port}", args.vps_target], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        def local_probe():
            with socket.create_connection(("127.0.0.1", local_port), timeout=1):
                pass
        wait_process_port(local_forward, local_probe)
        port_cmd = "python3 -c 'import socket; s=socket.socket(); s.bind((\"127.0.0.1\",0)); print(s.getsockname()[1]); s.close()'"
        nas_port = int(check(SSH + [args.nas_target, port_cmd]).strip())
        nas_forward = subprocess.Popen(SSH + ["-N", "-o", "ExitOnForwardFailure=yes", "-R", f"127.0.0.1:{nas_port}:127.0.0.1:{local_port}", args.nas_target], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        probe_cmd = f"python3 -c 'import socket; s=socket.create_connection((\"127.0.0.1\",{nas_port}),2); s.close()'"
        wait_process_port(nas_forward, lambda: check(SSH + [args.nas_target, probe_cmd], timeout=8))
        payload = tar_bytes([("test", test_binary, 0o700), ("host-test", host_test_binary, 0o700), ("service-test", service_test_binary, 0o700), ("client", private_key, 0o600), ("hostkey.pub", host_key, 0o600)])
        nas_cmd = ("d=$(mktemp -d /tmp/anas-vps-client.XXXXXXXX) || exit 1; "
                   "trap 'rm -rf \"$d\"' EXIT; tar -xf - -C \"$d\" || exit 1; "
                   "chmod 700 \"$d/test\" \"$d/host-test\"; chmod 600 \"$d/client\" \"$d/hostkey.pub\"; "
                   f"ANAS_TEST_VPS_SSH_PORT={nas_port} ANAS_TEST_VPS_SSH_USER=anasmaildeploy "
                   "ANAS_TEST_VPS_SSH_KEY=\"$d/client\" ANAS_TEST_VPS_SSH_HOSTKEY=\"$d/hostkey.pub\" "
                   "\"$d/test\" -test.v -test.run '^TestRelaySSHRealRestrictedInspect$' || exit 1; "
                   f"ANAS_TEST_VPS_SSH_PORT={nas_port} ANAS_TEST_VPS_SSH_USER=anasmaildeploy "
                   "ANAS_TEST_VPS_SSH_KEY=\"$d/client\" ANAS_TEST_VPS_SSH_HOSTKEY=\"$d/hostkey.pub\" "
                   "\"$d/host-test\" -test.v -test.run '^TestMailVPSRealHostPublish$'")
        result = subprocess.run(SSH + [args.nas_target, nas_cmd], input=payload, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=90)
        sys.stdout.buffer.write(result.stdout)
        sys.stdout.buffer.flush()
        if result.returncode != 0 or b"--- PASS: TestRelaySSHRealRestrictedInspect" not in result.stdout or b"--- PASS: TestMailVPSRealHostPublish" not in result.stdout:
            raise RuntimeError("finance real SSH client test failed")
        print("finance client reached isolated VPS helper and preserved rejected publish", flush=True)
        server.stdin.write(b"publish\n")
        server.stdin.flush()
        end = time.monotonic() + 30
        ready = False
        while time.monotonic() < end:
            try:
                line = lines.get(timeout=1)
            except queue.Empty:
                continue
            if line is None:
                raise RuntimeError("VPS fixture stopped before publication")
            sys.stdout.buffer.write(line)
            sys.stdout.buffer.flush()
            if line == b"CROSSHOST_PUBLISH_READY\n":
                ready = True
                break
        if not ready:
            raise RuntimeError("private publication directory not ready")
        success_cmd = ("d=$(mktemp -d /tmp/anas-vps-client.XXXXXXXX) || exit 1; "
                       "trap 'rm -rf \"$d\"' EXIT; tar -xf - -C \"$d\" || exit 1; "
                       "chmod 700 \"$d/host-test\" \"$d/service-test\"; chmod 600 \"$d/client\" \"$d/hostkey.pub\"; "
                       f"sudo -n env MAIL_VPS_ADMIN_LIVE=1 {'MAIL_VPS_ADMIN_FORWARD=1 ' if forward else ''}ANAS_TEST_VPS_SSH_PORT={nas_port} "
                       "MAIL_VPS_ADMIN_SERVICE_TEST=\"$d/service-test\" "
                       "ANAS_TEST_VPS_SSH_KEY=\"$d/client\" ANAS_TEST_VPS_SSH_HOSTKEY=\"$d/hostkey.pub\" "
                       "\"$d/host-test\" -test.v -test.run '^TestMailVPSAdminSocketLive$'")
        result = subprocess.run(SSH + [args.nas_target, success_cmd], input=payload, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=90)
        sys.stdout.buffer.write(result.stdout)
        sys.stdout.buffer.flush()
        if result.returncode != 0 or b"--- PASS: TestMailVPSAdminSocketLive" not in result.stdout:
            raise RuntimeError("finance administrator VPS publication test failed")
        print("finance administrator API published and retried against isolated VPS", flush=True)
        server.stdin.write(b"finished\n")
        server.stdin.flush()
        server.stdin.close()
        finish_deadline = time.monotonic() + 180
        while True:
            try:
                line = lines.get(timeout=min(10, max(0, finish_deadline - time.monotonic())))
            except queue.Empty:
                if server.poll() is not None:
                    raise RuntimeError("VPS fixture exited without a final result")
                if time.monotonic() >= finish_deadline:
                    raise RuntimeError("VPS fixture did not finish before the deadline")
                continue
            if line is None:
                break
            sys.stdout.buffer.write(line)
            sys.stdout.buffer.flush()
        if server.wait(timeout=10) != 0:
            raise RuntimeError("VPS fixture failed after cross-host test")
        ok = True
    finally:
        if server.stdin and not server.stdin.closed:
            server.stdin.close()
            # Give the remote lease holder time to observe EOF and run its
            # own cleanup before terminating the transport on an error.
            try:
                server.wait(timeout=15)
            except subprocess.TimeoutExpired:
                pass
        for proc in (nas_forward, local_forward, server):
            if proc is not None and proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait(timeout=10)
        if not ok:
            print("cross-host fixture not confirmed", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
