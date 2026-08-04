#!/usr/bin/env python3
from __future__ import annotations

import os
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

UPSTREAM = os.environ.get("UPSTREAM_URL", "http://identity-service:8080").rstrip("/")
DELAY_SECONDS = max(0.0, float(os.environ.get("DELAY_SECONDS", "1.5")))
LISTEN_ADDR = os.environ.get("LISTEN_ADDR", "0.0.0.0")
LISTEN_PORT = int(os.environ.get("LISTEN_PORT", "8080"))
ALLOWED_RESPONSE_HEADERS = {"content-type", "cache-control", "vary", "www-authenticate", "x-request-id"}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self) -> None:  # noqa: N802 - stdlib handler contract
        time.sleep(DELAY_SECONDS)
        request = urllib.request.Request(f"{UPSTREAM}{self.path}", method="GET")
        for name in ("Authorization", "X-Request-Id", "Accept"):
            value = self.headers.get(name)
            if value:
                request.add_header(name, value)
        try:
            with urllib.request.urlopen(request, timeout=5) as response:
                body = response.read(65537)
                if len(body) > 65536:
                    self.send_error(502)
                    return
                self.send_response(response.status)
                for name, value in response.headers.items():
                    if name.lower() in ALLOWED_RESPONSE_HEADERS:
                        self.send_header(name, value)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
        except urllib.error.HTTPError as error:
            body = error.read(65537)
            if len(body) > 65536:
                body = b""
            self.send_response(error.code)
            content_type = error.headers.get("Content-Type")
            if content_type:
                self.send_header("Content-Type", content_type)
            request_id = error.headers.get("X-Request-Id")
            if request_id:
                self.send_header("X-Request-Id", request_id)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except (OSError, urllib.error.URLError):
            self.send_response(503)
            self.send_header("Content-Length", "0")
            self.end_headers()

    def log_message(self, _format: str, *_args: object) -> None:
        return


if __name__ == "__main__":
    ThreadingHTTPServer((LISTEN_ADDR, LISTEN_PORT), Handler).serve_forever()
