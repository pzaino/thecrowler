#!/usr/bin/env python3
# Copyright 2023 Paolo Fabio Zaino
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Loopback fixture for the opt-in CDP sticky-rotation integration test.

Start this script inside the VDI's network namespace so that Chrome can reach
it at 127.0.0.1. The script deliberately serves the bootstrap path as a plain
404 document, matching a microproxy process that does not expose a UI. The
rotate endpoint returns a successful microproxy response only when the browser
sent the expected bearer token and idempotency key; otherwise it returns 401.
"""

import argparse
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

BOOTSTRAP_PATH = "/__crowler_rotation_bootstrap"  # must match cdpBootstrapPath
ROTATE_PATH = "/api/v1/sessions/rotate"  # must match proxycontrol.RotatePath


class Handler(BaseHTTPRequestHandler):
    server_version = "CrowlerCDPFixture/1.0"

    def do_GET(self):
        if self.path == BOOTSTRAP_PATH:
            body = b"404 page not found\n"
            self.send_response(404)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_error(404)

    def do_POST(self):
        if self.path != ROTATE_PATH:
            self.send_error(404)
            return
        if self.headers.get("Authorization") != self.server.expected_authorization:
            self._json_response(401, {"error": {"code": "unauthorized"}})
            return
        if self.headers.get("Idempotency-Key") != self.server.expected_key:
            self._json_response(400, {"error": {"code": "invalid_request"}})
            return
        self._json_response(200, {
            "status": "rotated",
            "provider": self.server.provider,
            "generation": self.server.generation,
        })

    def _json_response(self, status, payload):
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        # Keep the fixture output quiet; the Go test performs the assertions.
        del fmt, args


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=18094)
    parser.add_argument("--token", default="e2e-token")
    parser.add_argument("--key", default="e2e-idempotency-key")
    parser.add_argument("--provider", default="e2e-provider")
    parser.add_argument("--generation", type=int, default=1)
    args = parser.parse_args()

    server = ThreadingHTTPServer((args.host, args.port), Handler)
    server.expected_authorization = "Bearer " + args.token
    server.expected_key = args.key
    server.provider = args.provider
    server.generation = args.generation
    print("listening on http://%s:%d" % (args.host, args.port), flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
