#!/usr/bin/env python3
"""Compare exact production/candidate API Nginx blocks through the full gateway.

Only private listen/upstream addresses and diagnostic logs/header support differ.
Imports the frozen Python fixture runner; creates its own labelled local resources.
Never contacts a real supplier or changes the operations repository configuration.
"""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import http.client
import importlib.util
import json
from pathlib import Path
import re
import secrets
import select
import socket
import sys
import tempfile
import time
import urllib.parse

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("python_reliability", Path(__file__).with_name("python-reliability-acceptance.py"))
h = importlib.util.module_from_spec(spec)
spec.loader.exec_module(h)


def api_blocks(text):
    blocks = []
    for match in re.finditer(r"(?m)^\s*location\s+[^\n]+\{", text):
        header = match.group().strip()
        if "responses" not in header and header != "location / {":
            continue
        start = match.start()
        at, depth = text.index("{", match.start()) + 1, 1
        while depth and at < len(text):
            depth += (text[at] == "{") - (text[at] == "}")
            at += 1
        if depth:
            raise RuntimeError("unclosed production Nginx API block")
        blocks.append(text[start:at].strip())
    if len(blocks) != 2 or not blocks[-1].startswith("location / {"):
        raise RuntimeError("expected the exact API regex and fallback blocks")
    return blocks


def candidate_text(original, patch):
    # Apply the actual patch to a disposable copy, including its contextual
    # checks; do not reproduce its intended changes with a hand-written config.
    with tempfile.TemporaryDirectory(dir=ROOT / "deploy/.secrets", prefix="python-nginx-patch-") as private:
        path = Path(private) / "sub.geiliapi.com.conf"
        path.write_text(original)
        path.chmod(0o600)
        h.run(["patch", "--batch", "--forward", str(path)], input=patch)
        return path.read_text()


def private_config(text, upstream, listen, upgrade_map):
    blocks = api_blocks(text)
    # Canonical production uses the upgrade map declared by the outer Nginx
    # configuration. Retain its effect for ordinary HTTP calls and aliases.
    return "\n".join([
        "events {}", "http {",
        upgrade_map,
        'log_format proof escape=json \'{"id":"$http_x_client_request_id","status":$status,"seconds":$request_time}\';',
        "access_log /tmp/python-access.json proof;",
        "upstream sub2api_backend { server " + urllib.parse.urlsplit(upstream).netloc + "; keepalive 32; }",
        "server {", "listen " + listen + ";", "client_max_body_size 50m;", *blocks, "}", "}", ""])


def baseline_sync_timeout(fixture, kind, model, delay):
    ident = "nginx-timeout-" + secrets.token_hex(8)
    key, group, accounts = fixture.fixture(ident, model, "normal", delay)
    before = fixture.snapshot(key)
    result = h.client_call("requests", fixture.base, key["key"], kind, h.payload(kind, model, False), ident)
    h.wait_for("original fallback 300s timeout access", lambda: fixture.access_counter(ident), 5)
    h.wait_for("bounded already-started supplier completion", lambda: fixture.state.observed(ident) and all(call.get("finished_monotonic") for call in fixture.state.observed(ident)), 20)
    time.sleep(1)
    # A cancelled request that never observes supplier usage may have no receipt.
    # If the established drain path observes it, verify the full actual usage.
    settled = fixture.sql(f"SELECT json_build_object('n',COUNT(*)) FROM usage_settlement_receipts WHERE api_key_id={int(key['id'])};")[0]["n"]
    identity = {"user_id": fixture.user["id"], "account_id": accounts[0]["id"], "group_id": group["id"], "model": model}
    receipt = fixture.settlement(key, before, False, observed_failure=bool(settled), failure_output_tokens=100, expected_identity=identity)
    calls = fixture.state.observed(ident)
    good = result["http_status"] == 504 and not result["success"] and 300 <= result["elapsed_seconds"] < 310 and len(calls) == 1 and fixture.access_counter(ident) == 1 and receipt["valid"]
    fixture.report["cases"].append({"id": ident, "endpoint": kind, "model": model, "stream": False, "mode": "original_fallback_300s", "passed": good,
        "expected": "original Nginx fallback times out before delayed supplier", "client_requests": fixture.access_counter(ident), "upstream_attempts": len(calls),
        "result": result, "settlement": receipt, "supplier_usage_observed_by_gateway": bool(settled)})
    fixture.save()
    print(("PASS " if good else "FAIL ") + kind + " original sync 300s fallback limit", flush=True)


def complete_slow_upload(fixture, kind, model, delay):
    ident = "nginx-upload-" + secrets.token_hex(8)
    key, group, accounts = fixture.fixture(ident, model, "normal")
    before = fixture.snapshot(key)
    parsed = urllib.parse.urlsplit(fixture.base)
    body = json.dumps(h.payload(kind, model, False)).encode()
    header = ("POST /v1/" + kind + " HTTP/1.1\r\nHost: " + parsed.netloc + "\r\nAuthorization: Bearer " + key["key"] +
              "\r\nContent-Type: application/json\r\nContent-Length: " + str(len(body)) + "\r\nX-Client-Request-ID: " + ident + "\r\nConnection: close\r\n\r\n").encode()
    started = time.monotonic()
    with socket.create_connection((parsed.hostname, parsed.port), timeout=90) as sock:
        sock.sendall(header + body[:len(body) // 2])
        time.sleep(delay)
        early_bytes = bool(select.select([sock], [], [], 0)[0] and sock.recv(1, socket.MSG_PEEK))
        before_upload_attempts = len(fixture.state.observed(ident))
        sock.sendall(body[len(body) // 2:])
        response = http.client.HTTPResponse(sock)
        response.begin()
        raw = response.read()
        status, content_type = response.status, response.getheader("Content-Type", "")
    try:
        result = h.semantic_outcome(kind, False, json.loads(raw), status=status)
    except (ValueError, TypeError) as exc:
        result = h.semantic_outcome(kind, False, [], error=exc, status=status)
    identity = {"user_id": fixture.user["id"], "account_id": accounts[0]["id"], "group_id": group["id"], "model": model}
    receipt = fixture.settlement(key, before, result["success"], expected_identity=identity)
    counter = h.wait_for("slow complete upload access evidence", lambda: fixture.access_counter(ident), 5)
    elapsed = time.monotonic() - started
    calls = fixture.state.observed(ident)
    good = result["success"] and result["text_seen"] and model in result["returned_models"] and not early_bytes and before_upload_attempts == 0 and "application/json" in content_type and len(calls) == 1 and counter == 1 and receipt["valid"] and delay <= elapsed < delay + 10
    fixture.report["cases"].append({"id": ident, "endpoint": kind, "model": model, "stream": False, "mode": "complete_upload_" + str(delay) + "s", "passed": good,
        "client_requests": counter, "upstream_attempts": len(calls), "bytes_before_upload_complete": early_bytes, "provider_attempts_before_upload_complete": before_upload_attempts,
        "content_type": content_type, "elapsed_seconds": round(elapsed, 3), "result": result, "settlement": receipt})
    fixture.save()
    print(("PASS " if good else "FAIL ") + kind + " slow complete JSON upload", flush=True)


def delayed_sse_heartbeat(fixture, kind, model, delay):
    import httpx
    ident = "nginx-heartbeat-" + secrets.token_hex(8)
    key, group, accounts = fixture.fixture(ident, model, "normal", delay)
    before = fixture.snapshot(key)
    started = time.monotonic()
    raw, pending, heartbeats = "", "", []
    first_content = None
    with httpx.Client(transport=httpx.HTTPTransport(retries=0), trust_env=False, timeout=900) as client:
        with client.stream("POST", fixture.base + "/v1/" + kind, json=h.payload(kind, model, True), headers={"Authorization": "Bearer " + key["key"], "X-Client-Request-ID": ident}) as response:
            status = response.status_code
            for chunk in response.iter_text():
                raw += chunk
                pending += chunk.replace("\r\n", "\n")
                while "\n\n" in pending:
                    block, pending = pending.split("\n\n", 1)
                    now = time.monotonic() - started
                    if block.lstrip().startswith(":"):
                        heartbeats.append(round(now, 3))
                    if h.TEXT in block and first_content is None:
                        first_content = now
    elapsed = time.monotonic() - started
    result = h.semantic_outcome(kind, True, h.parse_sse(raw), status=status)
    result.update(client="httpx_stream_retry0", elapsed_seconds=round(elapsed, 3))
    identity = {"user_id": fixture.user["id"], "account_id": accounts[0]["id"], "group_id": group["id"], "model": model}
    receipt = fixture.settlement(key, before, result["success"], expected_identity=identity)
    counter = h.wait_for("delayed SSE ingress evidence", lambda: fixture.access_counter(ident), 5)
    calls = fixture.state.observed(ident)
    good = result["success"] and result["text_seen"] and model in result["returned_models"] and counter == 1 and len(calls) == 1 and receipt["valid"]
    good = good and bool(heartbeats) and 0 < heartbeats[0] < 15 and first_content is not None and first_content >= delay
    fixture.report["cases"].append({"id": ident, "endpoint": kind, "model": model, "stream": True, "mode": "delayed_application_heartbeat", "passed": good,
        "client_requests": counter, "upstream_attempts": len(calls), "first_heartbeat_seconds": heartbeats[0] if heartbeats else None,
        "heartbeat_count": len(heartbeats), "first_content_seconds": round(first_content, 3) if first_content is not None else None,
        "heartbeat_origin": "actual full Go gateway; supplier sends no SSE frames until delay", "result": result, "settlement": receipt})
    fixture.save()
    print(("PASS " if good else "FAIL ") + kind + " delayed supplier with observed gateway heartbeat", flush=True)


def run_profile(fixture, args, profile, delay, upload_delay):
    fixture.report["matrix_mode"] = "exact-nginx-" + profile
    fixture.edge_emulator = False
    cases = [(kind, h.MODELS[0] if kind == "messages" else h.MODELS[1], stream) for kind in h.ENDPOINTS for stream in (False, True)]
    clones = [fixture.slow_clone() for _ in cases]
    def execute(pair):
        clone, (kind, model, stream) = pair
        if stream:
            delayed_sse_heartbeat(clone, kind, model, delay)
        elif profile == "original" and kind != "responses":
            baseline_sync_timeout(clone, kind, model, delay)
        else:
            clone.case(kind, model, stream, delay=delay, min_elapsed=delay)
    with ThreadPoolExecutor(max_workers=4) as pool:
        list(pool.map(execute, zip(clones, cases)))
    for route, kind in (("/responses", "responses"), ("/backend-api/codex/responses", "responses"), ("/chat/completions", "chat/completions")):
        for stream in (False, True):
            fixture.case(kind, h.MODELS[1], stream, delay=6, min_elapsed=6, route=route)
    upload_clones = [fixture.slow_clone() for _ in h.ENDPOINTS]
    with ThreadPoolExecutor(max_workers=3) as pool:
        list(pool.map(lambda pair: complete_slow_upload(pair[0], pair[1], h.MODELS[0] if pair[1] == "messages" else h.MODELS[1], upload_delay), zip(upload_clones, h.ENDPOINTS)))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--profile", required=True, choices=("original", "candidate"))
    parser.add_argument("--original-config", default=str(ROOT.parent / "geili/subscription-lab-ops/deploy/ovh/sub.geiliapi.com.conf"))
    parser.add_argument("--delay", type=int, default=305)
    parser.add_argument("--upload-delay", type=int, default=35)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--report")
    args = parser.parse_args()
    if args.delay <= 300 or args.upload_delay >= 60 or args.upload_delay < 1:
        parser.error("delayed supplier must exceed original300s; complete upload must stay within canonical60s body inactivity")
    original = Path(args.original_config).resolve(strict=True).read_text()
    map_source = Path(args.original_config).resolve().with_name("newapi-forwarder.conf").read_text()
    map_match = re.search(r"map \$http_upgrade \$connection_upgrade\s*\{[^}]+\}", map_source)
    if map_match is None:
        raise RuntimeError("canonical production upgrade map missing")
    upgrade_map = map_match.group()
    patch = (ROOT / ".github/geili/cli-reliability-nginx.patch").read_text()
    candidate = candidate_text(original, patch)
    selected = original if args.profile == "original" else candidate
    metadata = {"profile": args.profile, "production_config_sha256": hashlib.sha256(original.encode()).hexdigest(),
        "candidate_patch_sha256": hashlib.sha256(patch.encode()).hexdigest(), "candidate_config_sha256": hashlib.sha256(candidate.encode()).hexdigest(),
        "profile_blocks_sha256": hashlib.sha256("\n".join(api_blocks(selected)).encode()).hexdigest(), "wrapper_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        "upgrade_map_sha256": hashlib.sha256(upgrade_map.encode()).hexdigest(),
        "supplier_delay_seconds": args.delay, "complete_upload_delay_seconds": args.upload_delay, "production_mutated": False,
        "adaptations": ["private upstream address", "private HTTP listener without TLS/Cloudflare allowlist", "canonical upgrade map support", "private proof access log"],
        "actual_cloudflare_executed": False}
    h.nginx_config = lambda upstream, listen: private_config(selected, upstream, listen, upgrade_map)
    identity = h.source_identity
    h.source_identity = lambda: identity() | {"nginx_profile": metadata}
    if args.self_test:
        for text, timeout in ((original, 600), (candidate, 1800)):
            blocks = api_blocks(text)
            assert "proxy_read_timeout " + str(timeout) + "s;" in blocks[0] and "proxy_read_timeout 300s;" in blocks[1]
            config = private_config(text, "http://127.0.0.1:19999", "8080", upgrade_map)
            assert "proxy_request_buffering off;" in config and "proxy_pass http://sub2api_backend;" in config
        assert api_blocks(original)[1] == api_blocks(candidate)[1]
        assert "messages|chat/completions" in api_blocks(candidate)[0]
        print(json.dumps({"self_test": "passed", **metadata}))
        return 0
    h.run_cases = lambda fixture, options: run_profile(fixture, options, args.profile, args.delay, args.upload_delay)
    sys.argv = [str(h.__file__), "--binary", args.binary, "--slow-only"] + (["--report", args.report] if args.report else [])
    return h.main()


if __name__ == "__main__":
    raise SystemExit(main())
