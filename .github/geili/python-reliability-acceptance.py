#!/usr/bin/env python3
"""Python single-call reliability through Nginx and the full application.

Only generated accounts and local synthetic providers are used. Local mode
creates uniquely labelled PostgreSQL/Redis/Nginx containers and starts the
caller-provided binary. Evidence remains under ignored deploy/.secrets/.
Stage adapters may import run_cases() and provide their existing guarded fixture;
this file never accepts an arbitrary external provider or production target.
"""
from __future__ import annotations

import argparse
import asyncio
import base64
import copy
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from decimal import Decimal
import hashlib
import http.server
import importlib.metadata
import ipaddress
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import threading
import time
import urllib.parse

ROOT = Path(__file__).resolve().parents[2]
TEXT = "python reliability ok"
PARTIAL = "partial A must not be replayed"
MODELS = ("claude-opus-5-5", "gpt-6-astra")
ENDPOINTS = ("messages", "responses", "chat/completions")
PINNED = {"requests": "2.34.2", "httpx": "0.28.1", "openai": "3.26.1", "anthropic": "1.12.1", "httpx2": "2.13.1"}
PNG_DATA = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jP1sAAAAASUVORK5CYII="


def dependencies():
    actual = {name: importlib.metadata.version(name) for name in PINNED}
    if actual != PINNED:
        raise RuntimeError("install the exact python-reliability-requirements.txt versions")
    return actual


def run(argv, **kwargs):
    return subprocess.run(argv, check=True, capture_output=True, text=True, **kwargs).stdout


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def private_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd = os.open(path, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "w") as out:
        json.dump(value, out, ensure_ascii=False, indent=2)
        out.write("\n")
    path.chmod(0o600)


def wait_for(label, predicate, timeout=60):
    import httpx
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        try:
            result = predicate()
            if result:
                return result
        except (OSError, subprocess.CalledProcessError, httpx.HTTPError):
            pass
        time.sleep(.1)
    raise AssertionError(label + " timed out")


def protocol(path):
    path = urllib.parse.urlsplit(path).path
    return "messages" if path.endswith("/messages") else "responses" if path.endswith("/responses") else "chat/completions"


def response_object(kind, model, rid, mode="normal"):
    text = PARTIAL if mode.startswith("partial") else TEXT
    usage = {"input_tokens": 1000, "output_tokens": 100}
    if kind == "messages":
        content = [{"type": "text", "text": text}]
        if mode == "empty":
            content = []
        if mode in ("thinking", "partial_thinking"):
            content = [{"type": "thinking", "thinking": "synthetic thinking", "signature": "fixture-signature"}, *content]
        if mode in ("tool", "partial_tool"):
            content = [{"type": "tool_use", "id": "call_" + rid, "name": "lookup", "input": {"nonce": 9007199254740993}}]
        return {"id": rid, "type": "message", "role": "assistant", "model": model, "content": content,
                "stop_reason": "tool_use" if mode in ("tool", "partial_tool") else "max_tokens" if mode == "limit" else "end_turn", "stop_sequence": None, "usage": usage}
    if kind == "responses":
        output = [{"id": "msg_" + rid, "type": "message", "role": "assistant", "status": "completed",
                   "content": [{"type": "output_text", "text": text, "annotations": [], "logprobs": []}]}]
        if mode == "empty":
            output = []
        if mode in ("tool", "partial_tool"):
            output = [{"id": "fc_" + rid, "type": "function_call", "call_id": "call_" + rid,
                       "name": "lookup", "arguments": '{"nonce":9007199254740993}', "status": "completed"}]
        if mode in ("thinking", "partial_thinking"):
            output = [{"id": "rs_" + rid, "type": "reasoning", "status": "completed", "summary": [{"type": "summary_text", "text": "synthetic thinking"}]}, *output]
        return {"id": rid, "object": "response", "created_at": int(time.time()), "status": "incomplete" if mode == "limit" else "completed",
                "model": model, "output": output, "error": None, "incomplete_details": {"reason": "max_output_tokens"} if mode == "limit" else None,
                "parallel_tool_calls": True, "temperature": 1.0, "tool_choice": "auto", "tools": [], "top_p": 1.0,
                "usage": {**usage, "total_tokens": 1100}, "metadata": {}}
    message = {"role": "assistant", "content": "" if mode in ("empty", "tool", "partial_tool") else text}
    if mode in ("tool", "partial_tool"):
        message["tool_calls"] = [{"id": "call_" + rid, "type": "function", "function": {"name": "lookup", "arguments": '{"nonce":9007199254740993}'}}]
    return {"id": rid, "object": "chat.completion", "created": int(time.time()), "model": model,
            "choices": [{"index": 0, "message": message, "finish_reason": "tool_calls" if mode in ("tool", "partial_tool") else "length" if mode == "limit" else "stop"}],
            "usage": {"prompt_tokens": 1000, "completion_tokens": 100, "total_tokens": 1100}}


def response_frames(kind, result, mode="normal"):
    """Complete native streams, with explicit content-block and tool boundaries."""
    if kind == "messages":
        yield "message_start", {"type": "message_start", "message": {**result, "content": [], "stop_reason": None, "usage": {"input_tokens": 1000, "output_tokens": 0}}}
        for index, block in enumerate(result["content"]):
            field = "text" if block["type"] == "text" else "thinking" if block["type"] == "thinking" else None
            start = {**block, **({field: ""} if field else {"input": {}})}
            yield "content_block_start", {"type": "content_block_start", "index": index, "content_block": start}
            delta = {"type": field + "_delta", field: block[field]} if field else {"type": "input_json_delta", "partial_json": json.dumps(block["input"], separators=(",", ":"))}
            yield "content_block_delta", {"type": "content_block_delta", "index": index, "delta": delta}
            if field == "thinking":
                yield "content_block_delta", {"type": "content_block_delta", "index": index, "delta": {"type": "signature_delta", "signature": block["signature"]}}
            yield "content_block_stop", {"type": "content_block_stop", "index": index}
        yield "message_delta", {"type": "message_delta", "delta": {"stop_reason": result["stop_reason"], "stop_sequence": None}, "usage": {"output_tokens": 100}}
        yield "message_stop", {"type": "message_stop"}
    elif kind == "responses":
        early_usage = {"input_tokens": 1000, "output_tokens": 0, "total_tokens": 1000} if mode.startswith("partial") or mode in ("missing_terminal", "cancel") else None
        yield "response.created", {"type": "response.created", "sequence_number": 0, "response": {**result, "status": "in_progress", "output": [], "usage": early_usage}}
        sequence = 1
        for index, item in enumerate(result["output"]):
            start = {**item, "status": "in_progress", **({"content": []} if item["type"] == "message" else {"summary": []} if item["type"] == "reasoning" else {"arguments": ""})}
            yield "response.output_item.added", {"type": "response.output_item.added", "sequence_number": sequence, "output_index": index, "item": start}
            sequence += 1
            if item["type"] == "message":
                part = item["content"][0]
                yield "response.content_part.added", {"type": "response.content_part.added", "sequence_number": sequence, "output_index": index, "content_index": 0, "item_id": item["id"], "part": {**part, "text": ""}}
                sequence += 1
                yield "response.output_text.delta", {"type": "response.output_text.delta", "sequence_number": sequence, "output_index": index, "content_index": 0, "item_id": item["id"], "delta": part["text"], "logprobs": []}
                sequence += 1
                yield "response.output_text.done", {"type": "response.output_text.done", "sequence_number": sequence, "output_index": index, "content_index": 0, "item_id": item["id"], "text": part["text"], "logprobs": []}
                sequence += 1
                yield "response.content_part.done", {"type": "response.content_part.done", "sequence_number": sequence, "output_index": index, "content_index": 0, "item_id": item["id"], "part": part}
            elif item["type"] == "reasoning":
                part = item["summary"][0]
                fields = {"output_index": index, "item_id": item["id"], "summary_index": 0}
                for event, values in (("response.reasoning_summary_part.added", {"part": {**part, "text": ""}}),
                                      ("response.reasoning_summary_text.delta", {"delta": part["text"]}),
                                      ("response.reasoning_summary_text.done", {"text": part["text"]}),
                                      ("response.reasoning_summary_part.done", {"part": part})):
                    yield event, {"type": event, "sequence_number": sequence, **fields, **values}
                    sequence += 1
            else:
                yield "response.function_call_arguments.delta", {"type": "response.function_call_arguments.delta", "sequence_number": sequence, "output_index": index, "item_id": item["id"], "delta": item["arguments"]}
                sequence += 1
                yield "response.function_call_arguments.done", {"type": "response.function_call_arguments.done", "sequence_number": sequence, "output_index": index, "item_id": item["id"], "arguments": item["arguments"]}
            sequence += 1
            yield "response.output_item.done", {"type": "response.output_item.done", "sequence_number": sequence, "output_index": index, "item": item}
            sequence += 1
        event = "response.incomplete" if result["status"] == "incomplete" else "response.completed"
        yield event, {"type": event, "sequence_number": sequence, "response": result}
    else:
        delta = dict(result["choices"][0]["message"])
        if delta.get("tool_calls"):
            delta["tool_calls"] = [{**value, "index": index} for index, value in enumerate(delta["tool_calls"])]
        yield "", {**result, "object": "chat.completion.chunk", "choices": [{"index": 0, "delta": delta, "finish_reason": None}], "usage": None}
        yield "", {**result, "object": "chat.completion.chunk", "choices": [{"index": 0, "delta": {}, "finish_reason": result["choices"][0]["finish_reason"]}]}
        yield "", "[DONE]"


def encode_frame(event, data, crlf=False):
    newline = "\r\n" if crlf else "\n"
    payload = data if isinstance(data, str) else json.dumps(data, separators=(",", ":"))
    return (("event: " + event + newline if event else "") + "data: " + payload + newline + newline).encode()


class ProviderState:
    def __init__(self):
        self.lock = threading.Lock()
        self.calls = []
        self.scenarios = {}

    def register(self, ident, mode, delay=0):
        with self.lock:
            self.scenarios[ident] = {"mode": mode, "delay": delay}

    def observed(self, ident, include_fixture=False):
        with self.lock:
            return [dict(item) for item in self.calls if item["case"] == ident and (include_fixture or item.get("purpose") == "inference")]


def mock_handler(state):
    class Provider(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.0"

        def log_message(self, *_):
            pass

        def do_GET(self):
            if self.path.endswith("/fixture-image.png"):
                raw = base64.b64decode(PNG_DATA)
                self.send_response(200)
                self.send_header("Content-Type", "image/png")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)
                return
            self.reply({"data": []})

        def reply(self, data, status=200):
            raw = json.dumps(data).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def do_POST(self):
            try:
                self.handle_post()
            except (BrokenPipeError, ConnectionResetError):
                pass
            finally:
                if hasattr(self, "call_record"):
                    with state.lock:
                        self.call_record["finished_monotonic"] = time.monotonic()

        def handle_post(self):
            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
            parts = urllib.parse.urlsplit(self.path).path.strip("/").split("/")
            ident, role = parts[0], parts[1]
            kind, model = protocol(self.path), body.get("model", "")
            is_inference = any(marker in json.dumps(body) for marker in ("synthetic Python request", "synthetic images", "synthetic history"))
            with state.lock:
                scenario = state.scenarios[ident]
                self.call_record = {"case": ident, "role": role, "purpose": "inference" if is_inference else "fixture_probe", "protocol": kind, "model": model, "stream": bool(body.get("stream")),
                                    "input_image_blocks": count_images(body), "thinking_blocks": count_type(body, "thinking"),
                                    "thinking_mode": body.get("thinking"),
                                    "output_budget": body.get("max_tokens", body.get("max_output_tokens")),
                                    "context_management": "context_management" in body, "tool_nonce_preserved": "9007199254740993" in json.dumps(body),
                                    "started_monotonic": time.monotonic()}
                state.calls.append(self.call_record)
            mode = scenario["mode"] if is_inference and (role == "a" or scenario["mode"].startswith("all_")) else "normal"
            delay = scenario["delay"] if is_inference and (role == "a" or scenario["mode"].startswith("all_")) else 0
            mode = mode.removeprefix("all_")
            if mode == "legacy_context" and "context_management" not in body:
                mode = "normal"
            if mode in ("http502", "http503", "http524", "quota", "concurrency", "invalid", "images51", "signature", "legacy_context", "output_limit_error"):
                if mode == "signature" and count_type(body, "thinking") == 0:
                    mode = "normal"
                else:
                    status = {"http502": 502, "http503": 503, "http524": 524, "quota": 400, "concurrency": 429, "invalid": 400, "images51": 400, "signature": 400, "legacy_context": 400, "output_limit_error": 400}[mode]
                    error = {"type": "server_error", "message": "Upstream service temporarily unavailable"}
                    if mode == "quota":
                        error = {"type": "upstream_error", "message": "Insufficient quota available for instant inference."}
                    if mode == "concurrency":
                        error = {"type": "rate_limit_error", "code": "gateway_concurrency_limit", "message": "upstream gateway concurrency limit exceeded"}
                    if mode == "invalid":
                        error = {"type": "invalid_request_error", "message": "synthetic invalid input"}
                    if mode == "images51":
                        error = {"type": "invalid_request_error", "param": "input", "message": "Exceeded maximum number of images (50) allowed in the request."}
                    if mode == "signature":
                        error = {"type": "invalid_request_error", "message": "Invalid `signature` in `thinking` block"}
                    if mode == "legacy_context":
                        error = {"type": "invalid_request_error", "message": "context_management: Extra inputs are not permitted"}
                    if mode == "output_limit_error":
                        error = {"type": "invalid_request_error", "message": "Claude's response exceeded the 128000 output token maximum."}
                    return self.reply({"error": error}, status)
            if mode == "disconnect":
                self.close_connection = True
                self.connection.shutdown(socket.SHUT_RDWR)
                return
            if mode == "jsonerror":
                return self.reply({"error": {"type": "server_error", "message": "Upstream service temporarily unavailable"}})
            failed_id = role == "a" and (mode in ("empty_stream", "prelude", "sseerror", "idle", "missing_terminal", "cancel") or mode.startswith("partial"))
            rid = ("failed_attempt_" if failed_id else "ok_") + ident
            result = response_object(kind, model, rid, mode)
            if mode == "missing_usage":
                result.pop("usage", None)
            if not body.get("stream") and mode not in ("empty_stream", "prelude", "sseerror", "idle"):
                if delay:
                    time.sleep(delay)
                return self.reply(result)
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("X-Request-ID", rid)
            self.end_headers()
            frames = list(response_frames(kind, result, mode))
            if mode == "empty_stream":
                return
            if mode in ("prelude", "idle"):
                self.wfile.write(b": synthetic heartbeat\n\n")
                self.wfile.write(encode_frame(*frames[0]))
                self.wfile.flush()
                if mode == "idle":
                    time.sleep(delay)
                return
            if mode == "sseerror":
                error = {"type": "server_error", "message": "Upstream service temporarily unavailable"}
                frame = ("error", {"type": "error", "error": error}) if kind == "messages" else ("response.failed", {"type": "response.failed", "response": {**result, "status": "failed", "error": error}}) if kind == "responses" else ("", {"error": error})
                self.wfile.write(encode_frame(*frame))
                self.wfile.flush()
                return
            if delay:
                time.sleep(delay)
            for event, data in frames:
                if mode in ("missing_terminal", "partial") and (event in ("message_delta", "message_stop", "response.completed", "response.incomplete") or (kind == "chat/completions" and (data == "[DONE]" or isinstance(data, dict) and data["choices"][0]["finish_reason"]))):
                    return
                self.wfile.write(encode_frame(event, data, crlf=mode == "crlf"))
                self.wfile.flush()
                if mode == "cancel" and (event in ("content_block_delta", "response.output_text.delta") or kind == "chat/completions"):
                    time.sleep(8)
                    return
                if mode.startswith("partial") and (event in ("content_block_delta", "response.output_text.delta", "response.function_call_arguments.delta", "response.reasoning_summary_text.delta") or kind == "chat/completions"):
                    return
    return Provider


def count_type(value, kind):
    if isinstance(value, dict):
        return int(value.get("type") == kind) + sum(count_type(item, kind) for item in value.values())
    return sum(count_type(item, kind) for item in value) if isinstance(value, list) else 0


def count_images(value):
    return sum(count_type(value, kind) for kind in ("image", "image_url", "input_image"))


def parse_sse(raw):
    events = []
    for block in raw.replace("\r\n", "\n").split("\n\n"):
        event = ""
        lines = []
        for line in block.splitlines():
            if line.startswith("event:"):
                event = line[6:].strip()
            if line.startswith("data:"):
                lines.append(line[5:].lstrip())
        if not lines:
            continue
        text = "\n".join(lines)
        if text == "[DONE]":
            events.append({"type": "[DONE]"})
            continue
        value = json.loads(text)
        if event and isinstance(value, dict) and "type" not in value:
            value["type"] = event
        events.append(value)
    return events


def payload(kind, model, stream, mode="normal", image_url=None):
    value = {"model": model, "stream": stream}
    if kind == "responses":
        value.update(input="synthetic Python request", max_output_tokens=1024)
    else:
        value.update(messages=[{"role": "user", "content": "synthetic Python request"}], max_tokens=1024)
    if mode in ("tool", "partial_tool"):
        if kind == "messages":
            value["tools"] = [{"name": "lookup", "input_schema": {"type": "object", "properties": {"nonce": {"type": "integer"}}}}]
        else:
            tool = {"name": "lookup", "description": "synthetic", "parameters": {"type": "object", "properties": {"nonce": {"type": "integer"}}}}
            value["tools"] = [{"type": "function", **tool}] if kind == "responses" else [{"type": "function", "function": tool}]
    if mode == "signature":
        value["thinking"] = {"type": "adaptive"}
        value["tools"] = [{"name": "lookup", "input_schema": {"type": "object", "properties": {"nonce": {"type": "integer"}}}}]
        value["messages"] = [{"role": "assistant", "content": [{"type": "thinking", "thinking": "synthetic history", "signature": "invalid-fixture"}, {"type": "text", "text": "prior"}, {"type": "tool_use", "id": "prior_tool", "name": "lookup", "input": {"nonce": 9007199254740993}}]},
                             {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "prior_tool", "content": "synthetic result"}, {"type": "text", "text": "synthetic Python request continue"}]}]
    if mode == "legacy_context":
        value["context_management"] = {"edits": [{"type": "clear_thinking_20251015", "keep": "all"}]}
    if mode == "output_limit_error":
        value["max_tokens"] = 128000
    if mode.startswith(("images50", "images51")):
        n = 50 if mode.startswith("images50") else 51
        url = (image_url or "http://127.0.0.1:1/fixture-image.png") if mode.endswith("_url") else "data:image/png;base64," + PNG_DATA
        if kind == "responses":
            value["input"] = [{"role": "user", "content": [{"type": "input_text", "text": "synthetic images"}, *[{"type": "input_image", "image_url": url} for _ in range(n)]]}]
        else:
            source = {"type": "url", "url": url} if mode.endswith("_url") else {"type": "base64", "media_type": "image/png", "data": PNG_DATA}
            image = {"type": "image", "source": source} if kind == "messages" else {"type": "image_url", "image_url": {"url": url}}
            value["messages"][0]["content"] = [{"type": "text", "text": "synthetic images"}, *[image for _ in range(n)]]
    return value


def semantic_outcome(kind, stream, data, error=None, status=200):
    encoded = json.dumps(data, ensure_ascii=False)
    events = data if stream and isinstance(data, list) else []
    objects = events if stream else [data]
    error_event = any(isinstance(item, dict) and (item.get("error") or item.get("type") in ("error", "response.failed") or item.get("status") == "failed") for item in objects)
    if kind == "messages":
        terminal = (any(item.get("type") == "message_stop" for item in events) and any(item.get("delta", {}).get("stop_reason") for item in events)) if stream else isinstance(data, dict) and bool(data.get("stop_reason"))
    elif kind == "responses":
        terminal = any(item.get("type") in ("response.completed", "response.incomplete") for item in events) if stream else isinstance(data, dict) and data.get("status") in ("completed", "incomplete")
    else:
        terminal = any(any(choice.get("finish_reason") for choice in item.get("choices", [])) for item in events) if stream else isinstance(data, dict) and any(choice.get("finish_reason") for choice in data.get("choices", []))
    returned_models = sorted({str(value.get("model") or value.get("response", {}).get("model") or value.get("message", {}).get("model")) for value in objects if isinstance(value, dict)} - {"None"})
    stop_reasons, completion_statuses, incomplete_reasons = set(), set(), set()
    for value in objects:
        if not isinstance(value, dict):
            continue
        response = value.get("response", value)
        delta = value.get("delta")
        reason = value.get("stop_reason") or (delta.get("stop_reason") if isinstance(delta, dict) else None)
        if reason:
            stop_reasons.add(reason)
        stop_reasons.update(choice["finish_reason"] for choice in value.get("choices", []) if choice.get("finish_reason"))
        if isinstance(response, dict) and response.get("status"):
            completion_statuses.add(response["status"])
            details = response.get("incomplete_details") or {}
            if details.get("reason"):
                incomplete_reasons.add(details["reason"])
    return {"success": status == 200 and terminal and not error and not error_event, "terminal": terminal,
            "error_event": error_event, "error_type": type(error).__name__ if error else None, "http_status": status,
            "text_seen": TEXT in encoded, "partial_seen": PARTIAL in encoded, "failed_prelude_seen": "failed_attempt_" in encoded,
            "thinking_seen": "synthetic thinking" in encoded,
            "tool_nonce_preserved": "9007199254740993" in encoded, "returned_models": returned_models,
            "stop_reasons": sorted(stop_reasons), "completion_statuses": sorted(completion_statuses), "incomplete_reasons": sorted(incomplete_reasons),
            "body_sha256": hashlib.sha256(encoded.encode()).hexdigest()}


def upload_timeout_evidence(response, elapsed, ident, records):
    """Separate actual HTTP bytes from a correlated Nginx inactivity timeout."""
    status = None
    if response.startswith(b"HTTP/"):
        parts = response.split(b"\r\n", 1)[0].split()
        if len(parts) > 1 and parts[1].isdigit():
            status = int(parts[1])
    row = records[0] if len(records) == 1 else {}
    correlated = row.get("id") == ident
    proxy_status = row.get("status") if correlated else None
    seconds = row.get("seconds", -1)
    valid = correlated and proxy_status == 408 and 30 <= seconds < 40 and 30 <= elapsed < 40 and (status == 408 or not response)
    return {"http_status": status, "proxy_status": proxy_status, "transport_eof": not response, "received_http_bytes": len(response), "valid_inactivity_timeout": valid}


def client_call(client_name, base, key, kind, body, ident, timeout=900, route=None):
    """Each adapter makes exactly one request; retries are disabled at every layer."""
    import httpx
    headers = {"Authorization": "Bearer " + key, "X-Client-Request-ID": ident}
    stream = body["stream"]
    started = time.monotonic()
    data, error, status = [], None, 200
    response_headers = []
    def capture_headers(response):
        response_headers.append(dict(response.headers))

    async def capture_async_headers(response):
        capture_headers(response)
    try:
        if client_name in ("requests", "httpx", "httpx_async", "httpx_cancel"):
            url = base + (route or "/v1/" + kind)
            if client_name == "requests":
                import requests
                with requests.Session() as session:
                    session.mount("http://", requests.adapters.HTTPAdapter(max_retries=0))
                    with session.post(url, json=body, headers=headers, timeout=timeout, stream=stream, allow_redirects=False) as response:
                        status = response.status_code
                        capture_headers(response)
                        raw = response.content.decode()
            elif client_name == "httpx_cancel":
                with httpx.Client(transport=httpx.HTTPTransport(retries=0), trust_env=False, timeout=timeout) as client:
                    raw = ""
                    with client.stream("POST", url, json=body, headers=headers) as response:
                        status = response.status_code
                        capture_headers(response)
                        for chunk in response.iter_text():
                            raw += chunk
                            if TEXT in raw:
                                break
            elif client_name == "httpx":
                with httpx.Client(transport=httpx.HTTPTransport(retries=0), trust_env=False, timeout=timeout) as client:
                    response = client.post(url, json=body, headers=headers, follow_redirects=False)
                    status, raw = response.status_code, response.text
                    capture_headers(response)
            else:
                async def raw_call():
                    async with httpx.AsyncClient(transport=httpx.AsyncHTTPTransport(retries=0), trust_env=False, timeout=timeout) as client:
                        response = await client.post(url, json=body, headers=headers, follow_redirects=False)
                        capture_headers(response)
                        return response.status_code, response.text
                status, raw = asyncio.run(raw_call())
            data = parse_sse(raw) if stream and status == 200 and raw.lstrip().startswith(("event:", "data:", ":")) else json.loads(raw)
        else:
            import openai
            import anthropic
            import httpx2
            is_async = client_name.endswith("_async")
            high_level = "final" in client_name
            kwargs = dict(api_key=key, base_url=base + ("/v1" if kind != "messages" else ""), max_retries=0, timeout=timeout)
            extra = {"X-Client-Request-ID": ident}
            if is_async:
                kwargs["http_client"] = httpx2.AsyncClient(transport=httpx2.AsyncHTTPTransport(retries=0), trust_env=False, timeout=timeout, event_hooks={"response": [capture_async_headers]})
                async def sdk_call():
                    nonlocal data
                    client = anthropic.AsyncAnthropic(**kwargs) if kind == "messages" else openai.AsyncOpenAI(**kwargs)
                    async with client:
                        api = client.messages if kind == "messages" else client.responses if kind == "responses" else client.chat.completions
                        if high_level and stream:
                            args = {k: v for k, v in body.items() if k != "stream"}
                            async with api.stream(**args, extra_headers=extra) as manager:
                                async for item in manager:
                                    data.append(item.model_dump(mode="json", warnings=False))
                                result = await (manager.get_final_message() if kind == "messages" else manager.get_final_response())
                                return result.model_dump(mode="json", warnings=False), False
                        result = await api.create(**body, extra_headers=extra)
                        if stream:
                            async for item in result:
                                data.append(item.model_dump(mode="json", warnings=False))
                            return data, True
                        return result.model_dump(mode="json", warnings=False), False
                data, stream = asyncio.run(sdk_call())
            else:
                kwargs["http_client"] = httpx2.Client(transport=httpx2.HTTPTransport(retries=0), trust_env=False, timeout=timeout, event_hooks={"response": [capture_headers]})
                client = anthropic.Anthropic(**kwargs) if kind == "messages" else openai.OpenAI(**kwargs)
                with client:
                    api = client.messages if kind == "messages" else client.responses if kind == "responses" else client.chat.completions
                    if high_level and stream:
                        args = {k: v for k, v in body.items() if k != "stream"}
                        with api.stream(**args, extra_headers=extra) as manager:
                            for item in manager:
                                data.append(item.model_dump(mode="json", warnings=False))
                            result = manager.get_final_message() if kind == "messages" else manager.get_final_response()
                            data, stream = result.model_dump(mode="json", warnings=False), False
                    else:
                        result = api.create(**body, extra_headers=extra)
                        if stream:
                            for item in result:
                                data.append(item.model_dump(mode="json", warnings=False))
                        else:
                            data = result.model_dump(mode="json", warnings=False)
    except Exception as exc:
        error = exc
        status = getattr(exc, "status_code", status)
    outcome = semantic_outcome(kind, stream, data, error, status)
    outcome.update(client=client_name, elapsed_seconds=round(time.monotonic() - started, 3), failed_header_seen=any("failed_attempt_" in value for headers in response_headers for value in headers.values()))
    return outcome


class ReliabilityFixture:
    """API/SQL surface also implemented by the guarded Stage adapter."""
    def __init__(self, base, pg, database, credentials, state, mock_base, private, owned_guard, access_counter):
        self.base, self.pg, self.database = base, pg, database
        self.credentials, self.state, self.mock_base, self.private = credentials, state, mock_base, private
        self.owned_guard, self.access_counter = owned_guard, access_counter
        self.admin = None
        self.user = None
        self.users = set()
        self.report = {"cases": [], "passed": False, "real_providers_executed": False, "client_retries": 0}
        self.report_lock = threading.Lock()

    def sql(self, query):
        self.owned_guard(self.pg)
        result = run(["docker", "exec", "-i", self.pg, "psql", "-XAt", "-v", "ON_ERROR_STOP=1", "-U", "acceptance", "-d", self.database], input=query)
        return [json.loads(line) for line in result.splitlines() if line.startswith("{")]

    def api(self, path, body=None, token=None, method=None):
        import httpx
        with httpx.Client(transport=httpx.HTTPTransport(retries=0), trust_env=False, timeout=30) as client:
            for attempt in range(3):
                response = client.request(method or ("POST" if body is not None else "GET"), self.base + "/api/v1" + path, json=body, headers={"Authorization": "Bearer " + token} if token else {})
                if response.status_code != 429 or attempt == 2:
                    break
                if "Retry-After" not in response.headers:
                    break  # Hourly key-create caps are not a short panel window.
                # Only fixture-management operations wait for their panel rate
                # window. Inference client_call() always issues one request.
                delay = min(60, max(1, int(response.headers.get("Retry-After", "60"))))
                print("WAIT fixture panel rate window " + str(delay) + "s", flush=True)
                time.sleep(delay)
        if response.status_code >= 400:
            raise RuntimeError(path + " fixture HTTP " + str(response.status_code))
        value = response.json()
        return value.get("data", value)

    def setup(self):
        self.admin = self.api("/auth/login", {"email": "admin@python-reliability.invalid", "password": self.credentials["admin_password"]})["access_token"]
        self.new_owner()

    def new_owner(self):
        self.user = self.api("/admin/users", {"email": "python-" + secrets.token_hex(6) + "@example.invalid", "password": self.credentials["admin_password"], "balance": 100, "concurrency": 32}, self.admin)
        self.user["token"] = self.api("/auth/login", {"email": self.user["email"], "password": self.credentials["admin_password"]})["access_token"]
        self.users.add(self.user["id"])
        self.owner_key_count = 0

    def fixture(self, ident, model, mode, delay=0, conversion=None, zero_rate=False):
        if getattr(self, "owner_key_count", 0) >= 40:
            self.new_owner()
        platform = conversion or ("anthropic" if model.startswith("claude") else "openai")
        group = self.api("/admin/groups", {"name": ident, "platform": platform, "subscription_type": "standard", "allow_messages_dispatch": True,
            "rate_multiplier": 0 if zero_rate else 1, "subscription_rate_multiplier": 1, "long_context_pricing_enabled": False,
            "model_pricing": [{"models": [model], "billing_mode": "token", "input_price": .000001, "output_price": .000002}]}, self.admin)
        group_ids = [group["id"]]
        if mode == "group_prelude":
            second = self.api("/admin/groups", {"name": ident + "-fallback", "platform": platform, "subscription_type": "standard", "allow_messages_dispatch": True,
                "rate_multiplier": 1, "subscription_rate_multiplier": 1, "model_pricing": [{"models": [model], "billing_mode": "token", "input_price": .000001, "output_price": .000002}]}, self.admin)
            group_ids.append(second["id"])
        provider_mode = "normal" if mode.startswith("images50") else "images51" if mode.startswith("images51") else "prelude" if mode == "group_prelude" else mode
        self.state.register(ident, provider_mode, delay)
        accounts = []
        for role, priority in (("a", 1), ("b", 10)):
            credentials = {"api_key": "synthetic-python-key", "base_url": self.mock_base + "/" + ident + "/" + role,
                           "model_mapping": {model: model}, "pool_mode": True, "pool_mode_retry_count": 0}
            account = self.api("/admin/accounts", {"name": ident + "-" + role, "platform": platform, "type": "apikey", "credentials": credentials,
                "extra": {"openai_responses_mode": "force_responses"} if platform == "openai" else {}, "group_ids": [group_ids[-1] if role == "b" else group_ids[0]], "concurrency": 32, "priority": priority}, self.admin)
            account["_fixture_group_id"] = group_ids[-1] if role == "b" else group_ids[0]
            accounts.append(account)
        key_payload = {"name": ident, "billing_source": "balance", "quota": 10}
        key_payload.update({"routing_mode": "composite", "group_ids": group_ids} if mode == "group_prelude" else {"routing_mode": "single", "group_id": group["id"]})
        key = self.api("/keys", key_payload, self.user["token"])
        self.owner_key_count = getattr(self, "owner_key_count", 0) + 1
        return key, group, accounts

    def snapshot(self, key):
        return self.sql(f"SELECT json_build_object('balance',balance,'quota',(SELECT quota_used FROM api_keys WHERE id={int(key['id'])})) FROM users WHERE id={int(self.user['id'])};")[0]

    def settlement(self, key, before, success, zero_rate=False, known_usage=True, observed_failure=False, failure_output_tokens=0, expected_identity=None):
        key_id = int(key["id"])
        receipts = f"usage_settlement_receipts WHERE api_key_id={key_id}"
        logs = f"usage_logs WHERE api_key_id={key_id}"
        query = f"""SELECT json_build_object(
            'receipts',(SELECT COUNT(*) FROM {receipts}),
            'settled',(SELECT COUNT(*) FROM {receipts} AND state='settled'),
            'delivered',(SELECT COUNT(*) FROM {receipts} AND delivered_at IS NOT NULL),
            'logs',(SELECT COUNT(*) FROM {logs}),
            'input_tokens',(SELECT COALESCE(SUM(input_tokens),0) FROM {logs}),
            'output_tokens',(SELECT COALESCE(SUM(output_tokens),0) FROM {logs}),
            'charged',(SELECT COALESCE(SUM(charged_amount),0) FROM {receipts} AND state='settled'),
            'receipt_identity',(SELECT json_build_object('user_id',user_id,'account_id',account_id,'group_id',group_id) FROM {receipts} ORDER BY id LIMIT 1),
            'log_identity',(SELECT json_build_object('user_id',user_id,'account_id',account_id,'group_id',group_id,'model',model) FROM {logs} ORDER BY id LIMIT 1))"""
        if success or observed_failure:
            try:
                receipt = wait_for("one delivered usage receipt", lambda: (row if (row := self.sql(query)[0])["delivered"] else None), 20)
            except AssertionError:
                receipt = self.sql(query)[0]
        else:
            time.sleep(.3)
            receipt = self.sql(query)[0]
        after = self.snapshot(key)
        charge = Decimal(str(receipt["charged"]))
        receipt["one_logical_settlement"] = receipt["receipts"] <= 1 and receipt["logs"] <= 1
        # Calls are serial for money checks; concurrent slow cases have separate users.
        receipt["exact_balance_delta"] = abs(Decimal(str(before["balance"])) - Decimal(str(after["balance"])) - charge) < Decimal("0.000000001")
        receipt["exact_key_quota_delta"] = abs(Decimal(str(after["quota"])) - Decimal(str(before["quota"])) - charge) < Decimal("0.000000001")
        expected_output = failure_output_tokens if observed_failure else 100
        expected_charge = Decimal("0") if zero_rate else Decimal("0.001") + Decimal(expected_output) * Decimal("0.000002")
        receipt["known_usage_exact_charge"] = not (success or observed_failure) or not known_usage or charge == expected_charge
        receipt["known_usage_exact_tokens"] = not (success or observed_failure) or not known_usage or (receipt["input_tokens"] == 1000 and receipt["output_tokens"] == expected_output)
        receipt["correct_billing_identity"] = expected_identity is None or not (success or observed_failure) or (receipt.get("log_identity") == expected_identity and receipt.get("receipt_identity") == {k: v for k, v in expected_identity.items() if k != "model"})
        receipt["no_preoutput_settlement"] = success or observed_failure or (receipt["receipts"] == receipt["logs"] == 0)
        receipt["valid"] = receipt["one_logical_settlement"] and receipt["exact_balance_delta"] and receipt["exact_key_quota_delta"] and receipt["known_usage_exact_charge"] and receipt["known_usage_exact_tokens"] and receipt["correct_billing_identity"] and receipt["no_preoutput_settlement"] and (not (success or observed_failure) or receipt["settled"] == receipt["delivered"] == receipt["logs"] == 1) and (not zero_rate or charge == 0)
        return receipt

    def save(self):
        with self.report_lock:
            private_json(self.private / "report.json", self.report)

    def case(self, kind, model, stream, mode="normal", client="requests", conversion=None, delay=0, expected="success", zero_rate=False, min_elapsed=0, route=None, max_elapsed=0):
        ident = "py-" + secrets.token_hex(8)
        key, group, accounts = self.fixture(ident, model, mode, delay, conversion, zero_rate)
        before = self.snapshot(key)
        client_base = self.base + "/edge" if mode in ("edge", "edge_stream") else self.base
        result = client_call(client, client_base, key["key"], kind, payload(kind, model, stream, mode, image_url=self.mock_base + "/fixture-image.png"), ident, route=route)
        calls = self.state.observed(ident)
        try:
            counter = wait_for("nginx caller access evidence", lambda: self.access_counter(ident), 5)
        except AssertionError:
            counter = 0
        drain_seconds = None
        if mode == "edge":
            drain_started = time.monotonic()
            wait_for("edge timeout existing upstream usage drain", lambda: self.state.observed(ident) and all(item.get("finished_monotonic") for item in self.state.observed(ident)), 10)
            time.sleep(1)
            drain_seconds = round(time.monotonic() - drain_started, 3)
        observed_failure = mode.startswith("partial") or mode in ("missing_terminal", "edge")
        winner = accounts[1 if calls and calls[-1]["role"] == "b" else 0]
        expected_identity = {"user_id": self.user["id"], "account_id": winner["id"], "group_id": winner["_fixture_group_id"], "model": model}
        receipt = self.settlement(key, before, result["success"], zero_rate, known_usage=mode != "missing_usage", observed_failure=observed_failure, failure_output_tokens=100 if mode == "edge" else 0, expected_identity=expected_identity)
        recovery_modes = {"disconnect", "http502", "http503", "http524", "quota", "concurrency", "jsonerror", "empty_stream", "prelude", "sseerror", "idle", "group_prelude"}
        recovered = any(item["role"] == "a" for item in calls) and any(item["role"] == "b" for item in calls)
        good = result["success"] if expected == "success" else not result["success"]
        if expected == "success" and mode not in ("empty", "tool"):
            good = good and result["text_seen"]
        if expected == "success":
            good = good and model in result["returned_models"]
        if expected == "success" and mode in recovery_modes:
            good = good and recovered and not result["failed_prelude_seen"] and not result["failed_header_seen"]
        if mode.startswith("partial") or mode == "missing_terminal":
            good = good and not any(item["role"] == "b" for item in calls) and bool(result["error_event"] or result["error_type"])
        if mode == "partial":
            good = good and result["partial_seen"]
        if mode in ("thinking", "partial_thinking"):
            good = good and result["thinking_seen"]
        if mode in ("invalid", "output_limit_error") or mode.startswith("images51"):
            good = good and len(calls) == 1 and result["http_status"] == 400
        if mode == "output_limit_error":
            good = good and calls[0]["output_budget"] == 128000
        if mode == "legacy_context":
            good = good and len(calls) == 1 and not calls[0]["context_management"]
        if mode == "tool":
            good = good and result["tool_nonce_preserved"]
        if mode == "limit":
            good = good and ("incomplete" in result["completion_statuses"] and "max_output_tokens" in result["incomplete_reasons"] if kind == "responses" else ("max_tokens" if kind == "messages" else "length") in result["stop_reasons"])
        if mode.startswith("images50"):
            good = good and bool(calls) and all(item["input_image_blocks"] == 50 for item in calls)
        if mode == "signature":
            good = good and len(calls) == 2 and calls[0]["thinking_blocks"] > 0 and calls[1]["thinking_blocks"] == 0 and all(item["tool_nonce_preserved"] and item["thinking_mode"] == {"type": "adaptive"} for item in calls)
        if mode == "edge":
            good = good and result["http_status"] == 504 and len(calls) == 1 and result["elapsed_seconds"] < 135
        good = good and counter == 1 and receipt["valid"] and result["elapsed_seconds"] >= min_elapsed
        if max_elapsed:
            good = good and result["elapsed_seconds"] < max_elapsed
        item = {"id": ident, "endpoint": kind, "route": route or "/v1/" + kind, "model": model, "stream": stream, "mode": mode, "conversion": conversion,
                "expected": expected, "passed": bool(good), "client_requests": counter, "upstream_attempts": len(calls),
                "fixture_probe_calls": len(self.state.observed(ident, include_fixture=True)) - len(calls),
                "upstream_calls": calls, "result": result, "settlement": receipt, "user_id": self.user["id"], "group_id": group["id"], "account_ids": [item["id"] for item in accounts]}
        if drain_seconds is not None:
            item["existing_usage_drain_seconds"] = drain_seconds
        self.report["cases"].append(item)
        self.save()
        print(("PASS " if good else "FAIL ") + kind + " " + model + " " + ("SSE " if stream else "JSON ") + client + " " + mode, flush=True)
        return good

    def cancel_case(self, kind, model):
        ident = "cancel-" + secrets.token_hex(8)
        key, group, accounts = self.fixture(ident, model, "cancel")
        for account in accounts:
            self.api("/admin/accounts/" + str(account["id"]), {"concurrency": 1}, self.admin, "PUT")
        before = self.snapshot(key)
        result = client_call("httpx_cancel", self.base, key["key"], kind, payload(kind, model, True), ident)
        wait_for("cancelled caller access", lambda: self.access_counter(ident), 5)
        drain_started = time.monotonic()
        wait_for("bounded existing upstream usage drain", lambda: self.state.observed(ident) and all(item.get("finished_monotonic") for item in self.state.observed(ident)), 10)
        # Allow the existing asynchronous settlement after usage draining to
        # finish before measuring the second request's independent balance delta.
        time.sleep(1)
        drain_seconds = time.monotonic() - drain_started
        first_calls = self.state.observed(ident)
        expected_identity = {"user_id": self.user["id"], "account_id": accounts[0]["id"], "group_id": group["id"], "model": model}
        receipt = self.settlement(key, before, False, observed_failure=True, expected_identity=expected_identity)
        self.state.register(ident, "normal")
        probe_ident = "probe-" + ident
        probe = self.api("/keys", {"name": probe_ident, "billing_source": "balance", "routing_mode": "single", "group_id": group["id"], "quota": 10}, self.user["token"])
        probe_before = self.snapshot(probe)
        probe_result = client_call("requests", self.base, probe["key"], kind, payload(kind, model, False), probe_ident, timeout=10)
        probe_receipt = self.settlement(probe, probe_before, probe_result["success"], expected_identity=expected_identity)
        all_calls = self.state.observed(ident)
        good = not result["success"] and result["text_seen"] and len(first_calls) == 1 and first_calls[0]["role"] == "a" and self.access_counter(ident) == 1 and receipt["valid"]
        good = good and probe_result["success"] and probe_result["elapsed_seconds"] < 5 and len(all_calls) == 2 and all_calls[-1]["role"] == "a" and probe_receipt["valid"]
        item = {"id": ident, "endpoint": kind, "model": model, "stream": True, "mode": "cancel", "passed": bool(good), "client_requests": self.access_counter(ident),
                "upstream_attempts": len(first_calls), "result": result, "settlement": receipt, "existing_usage_drain_seconds": round(drain_seconds, 3), "slot_release_probe": probe_result, "probe_settlement": probe_receipt}
        self.report["cases"].append(item)
        self.save()
        print(("PASS " if good else "FAIL ") + kind + " client cancel and slot release", flush=True)

    def slow_clone(self):
        # A distinct synthetic balance owner lets slow HTTP cases overlap while
        # preserving exact financial before/after proofs for each request.
        clone = copy.copy(self)
        clone.user = clone.api("/admin/users", {"email": "slow-" + secrets.token_hex(6) + "@example.invalid", "password": self.credentials["admin_password"], "balance": 100, "concurrency": 4}, self.admin)
        clone.user["token"] = clone.api("/auth/login", {"email": clone.user["email"], "password": self.credentials["admin_password"]})["access_token"]
        clone.users.add(clone.user["id"])
        clone.owner_key_count = 0
        return clone

    def slow_upload_case(self, delay=30):
        ident = "upload-" + secrets.token_hex(8)
        key, group, accounts = self.fixture(ident, MODELS[1], "normal")
        before = self.snapshot(key)
        parsed = urllib.parse.urlsplit(self.base)
        raw = json.dumps(payload("responses", MODELS[1], False)).encode()
        prefix = "/upload" if delay == 120 else "/upload-idle"
        header = ("POST " + prefix + "/v1/responses HTTP/1.1\r\nHost: " + str(parsed.hostname) + "\r\nAuthorization: Bearer " + key["key"] +
                  "\r\nContent-Type: application/json\r\nContent-Length: " + str(len(raw)) + "\r\nX-Client-Request-ID: " + ident + "\r\nConnection: close\r\n\r\n").encode()
        started = time.monotonic()
        response = b""
        with socket.create_connection((parsed.hostname, parsed.port), timeout=45) as sock:
            sock.sendall(header + raw[:len(raw) // 2])
            if delay == 120:
                time.sleep(120)
                sock.sendall(raw[len(raw) // 2:])
            while True:
                value = sock.recv(65536)
                if not value:
                    break
                response += value
        elapsed = time.monotonic() - started
        counter = wait_for("slow upload caller evidence", lambda: self.access_counter(ident), 5)
        reader = getattr(self.access_counter, "evidence", None)
        rows = reader(ident) if reader else []
        evidence = upload_timeout_evidence(response, elapsed, ident, rows)
        status = evidence["http_status"]
        success = status == 200 and TEXT.encode() in response and b"text/event-stream" not in response.split(b"\r\n\r\n", 1)[0].lower()
        expected_identity = {"user_id": self.user["id"], "account_id": accounts[0]["id"], "group_id": group["id"], "model": MODELS[1]}
        receipt = self.settlement(key, before, success, expected_identity=expected_identity)
        calls = self.state.observed(ident)
        good = (success and 120 <= elapsed < 130 and len(calls) == 1) if delay == 120 else (evidence["valid_inactivity_timeout"] and len(calls) == 0 and receipt["receipts"] == 0)
        good = good and counter == 1 and receipt["valid"]
        self.report["cases"].append({"id": ident, "endpoint": "responses", "model": MODELS[1], "stream": False, "mode": "slow_upload_" + str(delay) + "s_edge_emulator",
                                     "passed": good, "client_requests": counter, "upstream_attempts": len(calls), **evidence,
                                     "elapsed_seconds": round(elapsed, 3), "settlement": receipt})
        self.save()
        print(("PASS " if good else "FAIL ") + str(delay) + "-second slow upload edge emulator", flush=True)


def run_fast_cases(fixture, args):
    """Shared local/Stage matrix. It does not deploy or change gateway config."""
    for model in MODELS:
        for kind in ENDPOINTS:
            for stream in (False, True):
                fixture.case(kind, model, stream)
    if args.basics_only:
        return
    for kind in ENDPOINTS:
        model = MODELS[0] if kind == "messages" else MODELS[1]
        sdk = "anthropic" if kind == "messages" else "openai"
        for stream in (False, True):
            for client in ("httpx", "httpx_async", sdk, sdk + "_async"):
                fixture.case(kind, model, stream, client=client)
        if kind != "chat/completions":
            for client in (sdk + "_final", sdk + "_final_async"):
                fixture.case(kind, model, True, client=client)
        for mode in ("tool", "empty", "limit", "crlf"):
            fixture.case(kind, model, True, mode, client=sdk)
        for stream in (False, True):
            for mode in ("disconnect", "http502", "http503", "http524", "concurrency", "jsonerror", "empty_stream", "prelude", "sseerror"):
                fixture.case(kind, model, stream, mode)
        fixture.case(kind, model, True, "partial", client=sdk, expected="failure")
        fixture.case(kind, model, True, "partial_tool", client=sdk, expected="failure")
        fixture.case(kind, model, True, "missing_terminal", client=sdk, expected="failure")
        fixture.case(kind, model, True, "all_prelude", expected="failure")
        fixture.case(kind, model, False, "invalid", expected="failure")
        fixture.case(kind, model, False, zero_rate=True)
        fixture.cancel_case(kind, model)
        fixture.case(kind, model, True, "group_prelude")
        if kind != "chat/completions":
            for client in (sdk + "_final", sdk + "_final_async"):
                for mode in ("prelude", "partial", "missing_terminal"):
                    fixture.case(kind, model, True, mode, client=client, expected="success" if mode == "prelude" else "failure")
    fixture.case("responses", MODELS[1], False, "quota")
    fixture.case("responses", MODELS[1], True, "quota", client="openai")
    for stream in (False, True):
        fixture.case("responses", MODELS[1], stream, "missing_usage", client="openai")
    fixture.case("messages", MODELS[0], False, "signature", client="anthropic")
    for stream in (False, True):
        fixture.case("messages", MODELS[0], stream, "legacy_context")
        fixture.case("messages", MODELS[0], stream, "output_limit_error", expected="failure")
    fixture.case("messages", MODELS[0], True, "thinking", client="anthropic_final")
    fixture.case("messages", MODELS[0], True, "partial_thinking", client="anthropic", expected="failure")
    fixture.case("responses", MODELS[1], True, "thinking", client="openai_final")
    fixture.case("responses", MODELS[1], True, "partial_thinking", client="openai", expected="failure")
    for kind in ("responses", "chat/completions"):
        fixture.case(kind, MODELS[1], False, "images50")
        fixture.case(kind, MODELS[1], False, "images51", expected="failure")
        fixture.case(kind, MODELS[1], False, "images50_url")
        fixture.case(kind, MODELS[1], False, "images51_url", expected="failure")
    for kind, model, platform in (("messages", MODELS[1], "openai"), ("responses", MODELS[0], "anthropic"), ("chat/completions", MODELS[0], "anthropic")):
        for stream in (False, True):
            fixture.case(kind, model, stream, "prelude", conversion=platform)
    for route, kind in (("/responses", "responses"), ("/backend-api/codex/responses", "responses"), ("/chat/completions", "chat/completions")):
        for stream in (False, True):
            for mode in ("normal", "prelude"):
                fixture.case(kind, MODELS[1], stream, mode, route=route)
    for model, kind in (("gpt-4.1-mini", "responses"), ("claude-sonnet-4-6", "messages")):
        for stream in (False, True):
            fixture.case(kind, model, stream)


def run_slow_cases(fixture, args):
    if args.slow:
        slow_cases = [("messages", MODELS[0], "normal", 181, 181), ("responses", MODELS[1], "normal", 181, 181),
                      ("responses", MODELS[1], "idle", 620, 600)]
        if getattr(fixture, "edge_emulator", False):
            slow_cases[1] = ("responses", MODELS[1], "edge_stream", 181, 181)
            slow_cases.append(("responses", MODELS[1], "edge", 130, 125))
            slow_cases.append(("responses", "gpt-4.1-mini", "idle", 190, 180))
            slow_cases.append(("responses", MODELS[1], "upload", 120, 120))
        else:
            fixture.report.setdefault("unexecuted", []).append("125-second edge emulator")
        clones = [fixture.slow_clone() for _ in slow_cases]
        def execute(pair):
            clone, (kind, model, mode, delay, minimum) = pair
            if mode == "upload":
                return clone.slow_upload_case(delay)
            return clone.case(kind, model, mode != "edge", mode, delay=delay, min_elapsed=minimum,
                              max_elapsed=minimum + (15 if minimum == 600 else 5) if mode == "idle" else 0,
                              expected="failure" if mode == "edge" else "success")
        with ThreadPoolExecutor(max_workers=4) as pool:
            list(pool.map(execute, zip(clones, slow_cases)))
        if getattr(fixture, "edge_emulator", False):
            fixture.slow_upload_case()
    elif args.idle:
        for kind, model in (("messages", MODELS[0]), ("responses", MODELS[1])):
            fixture.case(kind, model, True, "idle", delay=40, min_elapsed=30, max_elapsed=35)


def run_cases(fixture, args):
    """Shared local/Stage matrix; deployment and config remain adapter-owned."""
    if getattr(args, "partial_only", False):
        for kind in ENDPOINTS:
            model = MODELS[0] if kind == "messages" else MODELS[1]
            sdk = "anthropic" if kind == "messages" else "openai"
            for mode in ("partial", "partial_tool", "missing_terminal"):
                fixture.case(kind, model, True, mode, client=sdk, expected="failure")
            fixture.cancel_case(kind, model)
        fixture.case("messages", MODELS[0], True, "partial_thinking", client="anthropic", expected="failure")
        fixture.case("responses", MODELS[1], True, "partial_thinking", client="openai", expected="failure")
        return
    if not getattr(args, "slow_only", False):
        run_fast_cases(fixture, args)
    if not args.basics_only:
        run_slow_cases(fixture, args)


def source_identity():
    runtime_files = sorted(path for path in (ROOT / "backend").rglob("*.go") if not path.name.endswith("_test.go") and "vendor" not in path.parts)
    runtime_files.extend(ROOT / path for path in ("backend/go.mod", "backend/go.sum", "backend/cmd/server/VERSION"))
    hashes = {str(path.relative_to(ROOT)): hashlib.sha256(path.read_bytes()).hexdigest() for path in runtime_files}
    return {"revision": run(["git", "-C", str(ROOT), "rev-parse", "HEAD"]).strip(),
            "tracked_diff_sha256": hashlib.sha256(run(["git", "-C", str(ROOT), "diff", "HEAD"]).encode()).hexdigest(),
            "runtime_files_sha256": hashes,
            "runtime_tree_sha256": hashlib.sha256(json.dumps(hashes, sort_keys=True, separators=(",", ":")).encode()).hexdigest(),
            "version": (ROOT / "backend/cmd/server/VERSION").read_text().strip()}


def nginx_config(upstream, listen):
    peer = urllib.parse.urlsplit(upstream).netloc
    locations = []
    for prefix, read_timeout, body_timeout in (("/upload-idle/", 125, 30), ("/upload/", 125, 125), ("/edge/", 125, None), ("/", 1200, None)):
        target = "http://acceptance_gateway" + ("" if prefix == "/" else "/")
        locations.append("\n".join([
            "location " + prefix + " {", "proxy_pass " + target + ";", "proxy_http_version 1.1;",
            'proxy_set_header Connection "";',
            "proxy_buffering off;", "proxy_request_buffering off;", "proxy_read_timeout " + str(read_timeout) + "s;",
            "proxy_send_timeout 1200s;", "add_header X-Accel-Buffering no always;",
            "client_body_timeout " + str(body_timeout) + "s;" if body_timeout else "", "}"]))
    return "\n".join(["events {}", "http {", "upstream acceptance_gateway { server " + peer + " max_fails=0; keepalive 32; }", 'log_format proof escape=json \'{"id":"$http_x_client_request_id","status":$status,"seconds":$request_time}\';',
                       "access_log /tmp/python-access.json proof;", "server {", "listen " + listen + ";", *locations, "}", "}", ""])


def docker_host_ipv4(stamp):
    # Docker Desktop's DNS also advertises IPv6 on hosts without an IPv6 route.
    # After the deliberate edge timeout Nginx may select that unreachable peer.
    # Read Docker's own host-gateway mapping rather than hardcoding a host address.
    hosts = run(["docker", "run", "--rm", "--label", "geili.python_reliability=" + stamp,
                 "--add-host", "host.docker.internal:host-gateway", "nginx:1.27-alpine", "cat", "/etc/hosts"])
    for line in hosts.splitlines():
        fields = line.split()
        if len(fields) > 1 and "host.docker.internal" in fields[1:]:
            address = ipaddress.ip_address(fields[0])
            if address.version == 4:
                return str(address)
    raise RuntimeError("Docker did not provide an IPv4 host-gateway mapping")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--basics-only", action="store_true")
    parser.add_argument("--partial-only", action="store_true", help="strict partial-output and cancel usage settlement checks")
    parser.add_argument("--idle", action="store_true", help="real minimum 30-second timeout checks")
    parser.add_argument("--slow", action="store_true", help="real 180/600-second production waiting checks")
    parser.add_argument("--slow-only", action="store_true", help="production timing and edge checks without repeating the fast matrix")
    parser.add_argument("--report", help="optional sanitized summary output")
    args = parser.parse_args()
    if args.slow_only:
        args.slow = True
    deps = dependencies()
    binary = Path(args.binary).resolve(strict=True)
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + secrets.token_hex(4)
    prefix = "geili-python-" + stamp.lower()
    private = ROOT / "deploy/.secrets/python-reliability" / stamp
    private.mkdir(parents=True, mode=0o700)
    runtime = private / "runtime"
    runtime.mkdir(mode=0o700)
    pg, redis, nginx = [prefix + "-" + name for name in ("postgres", "redis", "nginx")]
    ports = {name: free_port() for name in ("pg", "redis", "app", "mock", "nginx")}
    database = "acceptance_python_reliability"
    credentials = {"password": secrets.token_urlsafe(30), "admin_password": secrets.token_urlsafe(30)}
    private_json(private / "credentials.json", credentials)
    envfile = private / "postgres.env"
    envfile.write_text("POSTGRES_USER=acceptance\nPOSTGRES_DB=" + database + "\nPOSTGRES_PASSWORD=" + credentials["password"] + "\n")
    envfile.chmod(0o600)
    created, app, provider, logfile = [], None, None, None
    fixture = None
    state = ProviderState()
    report = {"identity": source_identity(), "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "runner_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), "dependencies": deps,
              "matrix_mode": "partial-only" if args.partial_only else "slow-only" if args.slow_only else "basics-only" if args.basics_only else "full",
              "local_only": True, "cases": [], "passed": False, "real_providers_executed": False, "slow_executed": args.slow,
              "unexecuted": ["paid supplier traffic", "payments", "media generation", "production deployment", "Cloudflare actual edge", "HTTP/2 reset through the Python full chain"]}
    if not args.slow:
        report["unexecuted"].append("real 180/600-second production waiting checks")
    private_json(private / "report.json", report)

    def owned(name):
        if name not in created or run(["docker", "inspect", "--format", '{{index .Config.Labels "geili.python_reliability"}}', name]).strip() != stamp:
            raise RuntimeError("resource ownership guard failed")

    def access_evidence(ident):
        owned(nginx)
        lines = run(["docker", "exec", nginx, "cat", "/tmp/python-access.json"]).splitlines()
        return [row for line in lines if (row := json.loads(line)).get("id") == ident]

    def access_count(ident):
        return len(access_evidence(ident))

    access_count.evidence = access_evidence

    try:
        for name, image, extra in ((pg, "postgres:18-alpine", ["--env-file", str(envfile), "-p", f"127.0.0.1:{ports['pg']}:5432"]),
                                   (redis, "redis:8.4-alpine", ["-p", f"127.0.0.1:{ports['redis']}:6379"])):
            run(["docker", "run", "-d", "--name", name, "--label", "geili.acceptance=local", "--label", "geili.python_reliability=" + stamp, *extra, image])
            created.append(name)
        wait_for("owned PostgreSQL TCP ready", lambda: subprocess.run(["docker", "exec", pg, "pg_isready", "-h", "127.0.0.1", "-U", "acceptance"], capture_output=True).returncode == 0)
        provider = http.server.ThreadingHTTPServer(("127.0.0.1", ports["mock"]), mock_handler(state))
        provider.daemon_threads = True
        threading.Thread(target=provider.serve_forever, daemon=True).start()
        env = {**os.environ, "AUTO_SETUP": "true", "SERVER_HOST": "127.0.0.1", "SERVER_PORT": str(ports["app"]), "DATA_DIR": str(runtime),
               "DATABASE_HOST": "127.0.0.1", "DATABASE_PORT": str(ports["pg"]), "DATABASE_USER": "acceptance", "DATABASE_DBNAME": database,
               "DATABASE_PASSWORD": credentials["password"], "DATABASE_SSLMODE": "disable", "REDIS_HOST": "127.0.0.1", "REDIS_PORT": str(ports["redis"]), "REDIS_DB": "0",
               "ADMIN_EMAIL": "admin@python-reliability.invalid", "ADMIN_PASSWORD": credentials["admin_password"], "JWT_SECRET": credentials["password"] * 2,
               "TOTP_ENCRYPTION_KEY": secrets.token_hex(32), "TZ": "Asia/Shanghai", "PRICING_REMOTE_URL": f"http://127.0.0.1:{ports['mock']}/pricing",
               "PRICING_HASH_URL": f"http://127.0.0.1:{ports['mock']}/pricing.sha256", "GATEWAY_STREAM_DATA_INTERVAL_TIMEOUT": "180" if args.slow else "30",
               "GATEWAY_LONG_THINKING_STREAM_DATA_INTERVAL_TIMEOUT": "600" if args.slow else "30", "GATEWAY_STREAM_KEEPALIVE_INTERVAL": "5"}
        logfile = open(private / "server.log", "w", opener=lambda path, flags: os.open(path, flags, 0o600))
        app = subprocess.Popen([str(binary)], cwd=runtime, env=env, stdout=logfile, stderr=subprocess.STDOUT)
        import httpx
        def healthy():
            if app.poll() is not None:
                raise RuntimeError("application exited; inspect private server.log")
            with httpx.Client(trust_env=False, timeout=1) as client:
                return client.get(f"http://127.0.0.1:{ports['app']}/health").status_code == 200
        wait_for("full application health", healthy)
        owned(pg)
        run(["docker", "exec", "-i", pg, "psql", "-XAt", "-v", "ON_ERROR_STOP=1", "-U", "acceptance", "-d", database], input="INSERT INTO settings(key,value) SELECT 'admin_compliance_acknowledgement:'||id,'{\"version\":\"v2026.06.10\",\"user_agent\":\"isolated Python reliability fixture\"}' FROM users WHERE email='admin@python-reliability.invalid' ON CONFLICT DO NOTHING;")
        config = private / "nginx.conf"
        linux = sys.platform.startswith("linux")
        upstream = "http://" + ("127.0.0.1" if linux else docker_host_ipv4(stamp)) + ":" + str(ports["app"])
        config.write_text(nginx_config(upstream, "127.0.0.1:" + str(ports["nginx"]) if linux else "8080"))
        config.chmod(0o600)
        network = ["--network", "host"] if linux else ["--add-host", "host.docker.internal:host-gateway", "-p", f"127.0.0.1:{ports['nginx']}:8080"]
        run(["docker", "run", "-d", "--name", nginx, "--label", "geili.python_reliability=" + stamp, *network, "-v", str(config) + ":/etc/nginx/nginx.conf:ro", "nginx:1.27-alpine"])
        created.append(nginx)
        base = f"http://127.0.0.1:{ports['nginx']}"
        wait_for("actual nginx health", lambda: httpx.get(base + "/health", timeout=2, trust_env=False).status_code == 200)
        fixture = ReliabilityFixture(base, pg, database, credentials, state, f"http://127.0.0.1:{ports['mock']}", private, owned, access_count)
        fixture.edge_emulator = True
        fixture.report = report
        fixture.setup()
        run_cases(fixture, args)
        report["passed"] = bool(report["cases"]) and all(item["passed"] for item in report["cases"])
    except Exception as exc:
        report["error_type"] = type(exc).__name__
        # Exception messages contain fixture paths/status only, never response bodies.
        report["error"] = str(exc)[:300]
        raise
    finally:
        if app is not None:
            app.terminate()
            try:
                app.wait(timeout=15)
            except subprocess.TimeoutExpired:
                app.kill()
                app.wait()
        if logfile is not None:
            logfile.close()
        if provider is not None:
            provider.shutdown()
            provider.server_close()
        for name in reversed(created):
            owned(name)
            run(["docker", "stop", "--time", "3", name])
        report["owned_resources_stopped"] = True
        report["final_identity"] = source_identity()
        report["binary_unchanged"] = hashlib.sha256(binary.read_bytes()).hexdigest() == report["binary_sha256"]
        report["runtime_source_unchanged"] = report["identity"]["runtime_tree_sha256"] == report["final_identity"]["runtime_tree_sha256"]
        report["runner_unchanged"] = hashlib.sha256(Path(__file__).read_bytes()).hexdigest() == report["runner_sha256"]
        report["passed"] = report["passed"] and report["binary_unchanged"] and report["runtime_source_unchanged"] and report["runner_unchanged"]
        private_json(private / "report.json", report)
        if args.report:
            private_json(Path(args.report), report)
        print("REPORT " + str(private / "report.json"), flush=True)
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
