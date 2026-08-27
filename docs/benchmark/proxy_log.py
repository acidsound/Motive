#!/usr/bin/env python3
"""Proxy: listen on 8088, forward to 8787, dump request bodies to /tmp/motive_reqs.ndjson"""
import json, threading, sys, urllib.request, urllib.error, http.server, socketserver

UPSTREAM = "http://127.0.0.1:8787"
LOG = "/tmp/motive_reqs.ndjson"

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(n)
        rec = {"path": self.path, "body": json.loads(body.decode("utf-8", "replace"))}
        with open(LOG, "a") as f:
            f.write(json.dumps(rec, ensure_ascii=False) + "\n")
        req = urllib.request.Request(UPSTREAM + self.path, data=body,
                                     headers={k: v for k, v in self.headers.items() if k.lower() != "content-length"},
                                     method="POST")
        try:
            with urllib.request.urlopen(req, timeout=180) as r:
                data = r.read()
                self.send_response(r.status)
                for k, v in r.getheaders():
                    self.send_header(k, v)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)
        except urllib.error.HTTPError as e:
            data = e.read()
            self.send_response(e.code)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
    def do_GET(self):
        req = urllib.request.Request(UPSTREAM + self.path, method="GET")
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                data = r.read()
                self.send_response(r.status)
                for k, v in r.getheaders():
                    self.send_header(k, v)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)
        except urllib.error.HTTPError as e:
            data = e.read()
            self.send_response(e.code)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

class TS(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True

if __name__ == "__main__":
    open(LOG, "w").close()
    TS(("127.0.0.1", 8088), H).serve_forever()