#!/usr/bin/env python3
"""The emulated Internet server: answers with the client address it saw.

Each router translates the site prefix to its ISP's prefix (NPTv6), so the
address the server sees tells which ISP the connection came through.
"""

from __future__ import annotations

import socket
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:  # noqa: N802 (name fixed by the base class)
        body = f"client={self.client_address[0]} server={self.headers.get('Host', '')}\n".encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args: object) -> None:
        return


class Server(ThreadingHTTPServer):
    address_family = socket.AF_INET6
    request_queue_size = 512
    daemon_threads = True


if __name__ == "__main__":
    Server(("::", int(sys.argv[1]) if len(sys.argv) > 1 else 80), Handler).serve_forever()
