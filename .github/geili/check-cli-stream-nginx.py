#!/usr/bin/env python3
"""Local mock-only Nginx regression: response starts before a slow body finishes.

Uses an existing nginx:1.27-alpine image. No supplier API, credentials or live config.
The upstream deliberately emits an SSE ping after the first request body byte.
"""
import argparse
import http.server
import json
import pathlib
import re
import socket
import subprocess
import tempfile
import threading
import time


class Upstream(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_POST(self):
        size = int(self.headers["Content-Length"])
        self.rfile.read(1)
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(b'event: ping\ndata: {"type":"ping"}\n\n')
        self.wfile.flush()
        self.rfile.read(size - 1)
        self.close_connection = True

    def log_message(self, *_args):
        pass


def run_nginx(config, route):
    with tempfile.TemporaryDirectory(prefix="geili-cli-nginx-") as directory:
        path = pathlib.Path(directory) / "nginx.conf"
        path.write_text(config)
        container = subprocess.check_output([
            "docker", "run", "--rm", "-d", "-p", "127.0.0.1::8080",
            "-v", f"{path}:/etc/nginx/nginx.conf:ro", "nginx:1.27-alpine",
        ], text=True).strip()
        try:
            port_info = json.loads(subprocess.check_output([
                "docker", "inspect", "--format", '{{json .NetworkSettings.Ports}}', container,
            ], text=True))
            port = int(port_info["8080/tcp"][0]["HostPort"])
            deadline = time.monotonic() + 5
            while True:
                try:
                    client = socket.create_connection(("127.0.0.1", port), timeout=1)
                    break
                except OSError:
                    if time.monotonic() > deadline:
                        raise
                    time.sleep(0.05)
            with client:
                client.sendall((f"POST {route}?beta=true HTTP/1.1\r\nHost: sub.geiliapi.com\r\n"
                                "Content-Type: application/json\r\nContent-Length: 2\r\n"
                                "Connection: close\r\n\r\n{").encode())
                # The last body byte is withheld: request buffering cannot pass
                # the request to the mock yet; streaming proxying can.
                client.settimeout(0.8)
                data = b""
                try:
                    while b"event: ping" not in data:
                        chunk = client.recv(4096)
                        if not chunk:
                            break
                        data += chunk
                except socket.timeout:
                    pass
                received_before_body_finished = b"event: ping" in data
                client.sendall(b"}")
                return received_before_body_finished
        finally:
            subprocess.run(["docker", "rm", "-f", container], check=True, capture_output=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--ops-config", type=pathlib.Path, required=True)
    args = parser.parse_args()
    source = args.ops_config.read_text()
    old = re.search(r"    location ~ \^/\(v1/\)\?responses\$ \{.*?\n    \}", source, re.S).group()
    fallback = re.search(r"    location / \{.*?\n    \}", source, re.S).group()
    new = old.replace("^/(v1/)?responses$", "^/((v1/)?(responses|messages|chat/completions)|backend-api/codex/responses)$")
    upstream = http.server.ThreadingHTTPServer(("0.0.0.0", 0), Upstream)
    thread = threading.Thread(target=upstream.serve_forever, daemon=True)
    thread.start()
    prefix = ("events {}\nhttp { map $http_upgrade $connection_upgrade { default upgrade; \"\" close; } "
              f"upstream sub2api_backend {{ server host.docker.internal:{upstream.server_port}; }} "
              "server { listen 8080;\n")
    reports = []
    try:
        for route in ["/v1/messages", "/v1/chat/completions", "/backend-api/codex/responses", "/v1/responses"]:
            before = run_nginx(prefix + old + fallback + "}\n}\n", route)
            after = run_nginx(prefix + new + fallback + "}\n}\n", route)
            assert after, f"patched route still buffers: {route}"
            assert before == (route == "/v1/responses"), f"baseline drift: {route}"
            reports.append({"route": route, "baseline_ping_before_upload_end": before,
                            "patched_ping_before_upload_end": after})
        print(json.dumps({"mock_only": True, "passed": len(reports), "results": reports}, indent=2))
    finally:
        upstream.shutdown()
        upstream.server_close()


if __name__ == "__main__":
    main()
