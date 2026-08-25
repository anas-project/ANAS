#!/usr/bin/env python3
"""Minimal one-shot receiver for the isolated Vikunja webhook E2E.

The receiver intentionally has no request logging. It stores only the verified
payload and a non-secret result marker in the caller-provided private directory.
"""

from __future__ import annotations

import argparse
import hashlib
import hmac
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


MAX_BODY_BYTES = 2 * 1024 * 1024


def signature_is_valid(secret: bytes, body: bytes, supplied: str) -> bool:
    expected = hmac.new(secret, body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, supplied.strip().lower())


class WebhookHandler(BaseHTTPRequestHandler):
    server: "WebhookServer"

    def log_message(self, _format: str, *_args: object) -> None:
        return

    def _reply(self, status: int) -> None:
        self.send_response(status)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
        self._reply(200 if self.path == "/health" else 404)

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
        if self.path != "/vikunja":
            self._reply(404)
            return

        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            self._reply(400)
            return
        if length <= 0 or length > MAX_BODY_BYTES:
            self._reply(413)
            return

        body = self.rfile.read(length)
        supplied = self.headers.get("X-Vikunja-Signature", "")
        if not signature_is_valid(self.server.secret, body, supplied):
            self.server.write_marker("invalid-signature", {"status": "rejected"})
            self._reply(401)
            return

        try:
            payload = json.loads(body)
        except json.JSONDecodeError:
            self._reply(400)
            return

        self.server.payload_path.write_bytes(body)
        os.chmod(self.server.payload_path, 0o600)
        self.server.write_marker(
            "verified",
            {
                "status": "verified",
                "event_name": payload.get("event_name"),
            },
        )
        self._reply(204)


class WebhookServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, address: tuple[str, int], secret: bytes, output_dir: Path):
        super().__init__(address, WebhookHandler)
        self.secret = secret
        self.output_dir = output_dir
        self.payload_path = output_dir / "payload.json"

    def write_marker(self, name: str, value: dict[str, object]) -> None:
        path = self.output_dir / f"{name}.json"
        path.write_text(json.dumps(value, separators=(",", ":")) + "\n")
        os.chmod(path, 0o600)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--bind", default=os.environ.get("ANAS_WEBHOOK_BIND", "1.1.1.1"))
    parser.add_argument(
        "--port", type=int, default=int(os.environ.get("ANAS_WEBHOOK_PORT", "18080"))
    )
    parser.add_argument(
        "--output-dir", required=True, type=Path, help="private E2E artifact directory"
    )
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    secret = os.environ.get("ANAS_WEBHOOK_SECRET", "").encode()
    if not secret:
        raise SystemExit("ANAS_WEBHOOK_SECRET is required")
    args.output_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(args.output_dir, 0o700)
    server = WebhookServer((args.bind, args.port), secret, args.output_dir)
    pid_path = args.output_dir / "receiver.pid"
    pid_path.write_text(f"{os.getpid()}\n")
    os.chmod(pid_path, 0o600)
    server.serve_forever()


if __name__ == "__main__":
    main()
