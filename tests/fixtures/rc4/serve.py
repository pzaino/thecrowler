#!/usr/bin/env python3
"""Deterministic RC4 acceptance fixture.

Serves a small set of pages plus controllable redirect endpoints so the
per-source redirect policy can be exercised against both the browser and the
Go HTTP client. Redirect behaviour is entirely local: no external host is
contacted.
"""
import http.server
import os
import socketserver
import sys

PORT = int(os.environ.get("FIXTURE_PORT", "8000"))
CROSS_HOST = os.environ.get("FIXTURE_CROSS_HOST", "rc4-fixture-alt:8000")

INDEX = """<!doctype html><html><head><title>RC4 Fixture Home</title></head>
<body><h1>RC4 Fixture Home</h1>
<p>marker rc4-home-v1</p>
<a href="/target.html">target</a>
</body></html>
"""

TARGET = """<!doctype html><html><head><title>RC4 Target Page</title></head>
<body><h1>RC4 Target</h1><p>marker rc4-target-v1</p></body></html>
"""

HOTEL_TRAILING = """<!doctype html><html><head><title>RC4 Hotel Trailing</title></head>
<body><h1>RC4 Hotel</h1><p>marker rc4-hotel-trailing</p></body></html>
"""

MOTEL_TRAILING = """<!doctype html><html><head><title>RC4 Motel Bare</title></head>
<body><h1>RC4 Motel</h1><p>marker rc4-motel-bare</p></body></html>
"""

COOKIE = """<!doctype html><html><head><title>RC4 Cookie Page</title></head>
<body><h1>RC4 Cookie</h1><p>marker rc4-cookie-v1</p>
<script>
document.cookie = "rc4_state=leak; path=/";
try { localStorage.setItem("rc4_state", "leak"); } catch (e) {}
</script></body></html>
"""

STATE = """<!doctype html><html><head><title>RC4 State Page</title></head>
<body><h1>RC4 State</h1><pre id="out"></pre>
<script>
document.getElementById("out").textContent =
  "cookie=" + document.cookie + ";storage=" + String(localStorage.getItem("rc4_state"));
</script></body></html>
"""


class Handler(http.server.SimpleHTTPRequestHandler):
    def end_headers(self):
        self.send_header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
        self.send_header("Pragma", "no-cache")
        self.send_header("Expires", "0")
        super().end_headers()

    def do_GET(self):  # noqa: N802
        raw_path, _, query = self.path.partition("?")
        path = raw_path
        if path.startswith("/r/"):
            self.handle_redirect(path[3:])
            return
        if path == "/cookie":
            self.send(200, COOKIE)
            return
        if path == "/state":
            self.send(200, STATE)
            return
        if path == "/target.html":
            self.send(200, TARGET)
            return
        # Path-distinction fixture: /hotel and /hotel/ are different resources.
        # The bare path redirects so a source that forbids redirects must refuse
        # it, while a source that permits redirects indexes the slash body.
        if path == "/hotel":
            self.redirect(302, "/hotel/")
            return
        if path == "/hotel/":
            self.send(200, HOTEL_TRAILING)
            return
        # The reverse direction, used to prove the comparison is symmetric.
        if path == "/motel/":
            self.redirect(302, "/motel")
            return
        if path == "/motel":
            self.send(200, MOTEL_TRAILING)
            return
        if path == "/health":
            self.send(200, "ok", "text/plain")
            return
        if path == "/page":
            params = {}
            for item in query.split("&"):
                if "=" in item:
                    key, value = item.split("=", 1)
                    params[key] = value
            src = params.get("src", "a")
            marker = "marker rc4-v-1"
            body_file = os.path.join(os.path.dirname(os.path.abspath(__file__)), f"{src}.body")
            try:
                with open(body_file, encoding="utf-8") as handle:
                    marker = handle.read().strip()
            except OSError:
                pass
            self.send(200, "<!doctype html><html><head><title>RC4 Page</title></head>"
                           f"<body><h1>RC4 Page</h1><p>{marker}</p></body></html>")
            return
        super().do_GET()

    def handle_redirect(self, spec):
        parts = spec.strip("/").split("/")
        kind = parts[0]
        if kind in ("301", "302", "303", "307", "308"):
            self.redirect(int(kind), "/target.html")
            return
        if kind == "cross":
            self.redirect(302, f"http://{CROSS_HOST}/target.html")
            return
        if kind == "loop":
            self.redirect(302, "/r/loop")
            return
        if kind == "file":
            self.redirect(302, "file:///etc/passwd")
            return
        if kind == "scheme":
            self.redirect(302, "ftp://127.0.0.1/secret")
            return
        if kind == "chain":
            remaining = int(parts[1]) if len(parts) > 1 else 6
            if remaining <= 0:
                self.redirect(302, "/target.html")
            else:
                self.redirect(302, f"/r/chain/{remaining - 1}")
            return
        self.send(404, "unknown redirect", "text/plain")

    def redirect(self, code, location):
        self.send_response(code)
        self.send_header("Location", location)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def send(self, code, body, content_type="text/html; charset=utf-8"):
        data = body.encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, fmt, *args):
        sys.stderr.write("fixture %s\n" % (fmt % args))


if __name__ == "__main__":
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    socketserver.TCPServer.allow_reuse_address = True
    with socketserver.ThreadingTCPServer(("0.0.0.0", PORT), Handler) as httpd:
        httpd.serve_forever()
