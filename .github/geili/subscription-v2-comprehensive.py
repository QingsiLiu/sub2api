#!/usr/bin/env python3
"""Local-only full Subscription V2 lifecycle plus protocol/accounting acceptance.

Run against a fresh local server built from the matching checkout:
  python3 .github/geili/subscription-v2-comprehensive.py \
    --base http://127.0.0.1:18494 --database acceptance_v2_comprehensive_ready

Only synthetic payment/upstream replies are used. This exercises the real HTTP
handlers, scheduler, PostgreSQL ledger and Redis task binding, not live providers.
--protocol-only skips the already independently runnable lifecycle matrix.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
from decimal import Decimal
import importlib.util
import io
import json
import os
from pathlib import Path
import secrets
import threading
import time
import urllib.parse

SPEC = importlib.util.spec_from_file_location("subscription_v2_base", Path(__file__).with_name("subscription-v2-acceptance.py"))
v2 = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(v2)
CHAT = "gpt-4.1-mini"
ANTHROPIC = "claude-sonnet-4-6"
GEMINI = "gemini-2.5-flash"
EMBEDDING = "text-embedding-3-small"
IMAGE = "gpt-image-1"
VIDEO = "grok-imagine-video-1.5"
PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg=="


def close_amount(actual, expected):
    return abs(Decimal(str(actual)) - Decimal(str(expected))) < Decimal("0.000000001")


def mock_reply(path, body, videos):
    """Return (status, JSON body) for specialized native protocol endpoints."""
    if "/failure/" in path:
        return 400, {"error": {"type": "invalid_request_error", "message": "synthetic request failure"}}
    if "/down/" in path:
        return 503, {"error": {"type": "server_error", "message": "synthetic retryable upstream failure"}}
    if path.endswith("/embeddings"):
        return 200, {"object": "list", "model": body["model"], "data": [{"object": "embedding", "index": 0, "embedding": [0.1, -0.2, 0.3]}], "usage": {"prompt_tokens": 1000, "total_tokens": 1000}}
    if path.endswith("/images/generations"):
        return 200, {"created": int(time.time()), "data": [{"b64_json": PNG} for _ in range(body.get("n", 1))]}
    if ":generateContent" in path or ":streamGenerateContent" in path:
        return 200, {"candidates": [{"content": {"parts": [{"text": "acceptance ok"}], "role": "model"}, "finishReason": "STOP", "index": 0}], "usageMetadata": {"promptTokenCount": 1000, "candidatesTokenCount": 100, "totalTokenCount": 1100}, "modelVersion": GEMINI, "responseId": "gemini-" + secrets.token_hex(8)}
    if path.endswith("/videos/generations") or path.endswith("/videos"):
        ident = "synthetic-video-" + secrets.token_hex(8)
        videos[ident] = {"model": body["model"], "duration": body.get("duration", 5), "status": "pending"}
        return 200, {"request_id": ident, "status": "pending"}
    return None


class ComprehensiveFixture(v2.Fixture):
    def __init__(self, args):
        super().__init__(args)
        self.protocol_only = args.protocol_only
        self.videos = {}
        self.upstream_calls = []
        self.slow_entered = threading.Event()
        self.slow_release = threading.Event()
        self.protocol_groups = {}

    def start_mock(self):
        super().start_mock()
        original = self.server.RequestHandlerClass
        fixture = self

        class ProtocolMock(original):
            def do_POST(self):
                if not self.path.startswith("/protocol/"):
                    return super().do_POST()
                raw = self.rfile.read(int(self.headers.get("Content-Length", 0)))
                body = json.loads(raw)
                path = urllib.parse.urlsplit(self.path).path
                with fixture.lock:
                    fixture.upstream_calls.append({"path": path, "model": body.get("model"), "stream": body.get("stream", False)})
                    reply = mock_reply(path, body, fixture.videos)
                if "/slow/" in path:
                    fixture.slow_entered.set()
                    if not fixture.slow_release.wait(15):
                        return self.reply({"error": {"message": "synthetic slow upstream timed out"}}, 504)
                if reply is not None:
                    status, payload = reply
                    if path.endswith("/images/generations") and body.get("stream"):
                        self.send_response(status)
                        self.send_header("Content-Type", "text/event-stream")
                        self.end_headers()
                        event = {"type": "image_generation.completed", "b64_json": PNG, "output_format": "png"}
                        self.wfile.write(("event: image_generation.completed\ndata: " + json.dumps(event) + "\n\n").encode())
                        self.wfile.flush()
                        return
                    if ":streamGenerateContent" in path:
                        self.send_response(status)
                        self.send_header("Content-Type", "text/event-stream")
                        self.end_headers()
                        self.wfile.write(("data: " + json.dumps(payload) + "\n\n").encode())
                        self.wfile.flush()
                        return
                    return self.reply(payload, status)
                self.rfile = io.BytesIO(raw)
                return v2.BASE_HARNESS.Mock.do_POST(self)

            def do_GET(self):
                path = urllib.parse.urlsplit(self.path).path
                if path.startswith("/protocol/") and "/videos/" in path:
                    ident = path.rsplit("/", 1)[-1]
                    with fixture.lock:
                        video = fixture.videos.get(ident)
                        fixture.upstream_calls.append({"path": path, "method": "GET"})
                    if not video:
                        return self.reply({"error": {"message": "unknown synthetic task"}}, 404)
                    if video["status"] != "done":
                        return self.reply({"request_id": ident, "status": video["status"]})
                    return self.reply({"request_id": ident, "status": "done", "model": video["model"], "video": {"url": fixture.mock_base + "/assets/" + ident + ".mp4", "duration": video["duration"], "respect_moderation": True}})
                return super().do_GET()

        self.server.RequestHandlerClass = ProtocolMock

    def make_group(self, name, platform, models, rate=1, pricing=True, **extras):
        payload = {"name": "V2 comprehensive " + name + " " + self.suffix, "platform": platform, "rate_multiplier": 0.5, "subscription_rate_multiplier": rate, "subscription_enabled": True, "long_context_pricing_enabled": False, **extras}
        if pricing:
            payload["model_pricing"] = [{"models": models, "billing_mode": "token", "input_price": 0.000001, "output_price": 0.000002}]
        group = self.api("/admin/groups", payload, self.admin)
        account = self.api("/admin/accounts", {"name": "V2 protocol mock " + name + " " + self.suffix, "platform": platform, "type": "apikey", "credentials": {"api_key": "synthetic-test-key", "base_url": self.mock_base + "/protocol/" + name, "model_mapping": {model: model for model in models}}, "group_ids": [group["id"]], "concurrency": 15, "priority": 1}, self.admin)
        group["account_id"] = account["id"]
        self.protocol_groups[name] = group
        return group

    def setup_protocols(self):
        self.make_group("openai", "openai", [CHAT, EMBEDDING], 1.25)
        self.make_group("anthropic", "anthropic", [ANTHROPIC], 1.5)
        self.make_group("gemini", "gemini", [GEMINI], 0.75)
        self.make_group("images", "openai", [IMAGE], 1.2, pricing=False, allow_image_generation=True, image_price_1k=0.25, image_price_2k=0.4, image_price_4k=0.6)
        self.make_group("video", "grok", [VIDEO], 1.5, pricing=False, video_price_480p=0.03, video_price_720p=0.07, video_price_1080p=0.11)
        for name in ("failure", "down", "slow"):
            self.make_group(name, "openai", [CHAT], 1.25)
        self.owner = self.user("protocols")
        self.pay(self.owner, self.quote(self.owner, "month180", "purchase", units=1))
        self.protocol_sub = self.subscription(self.owner)
        self.protocol_sid = self.protocol_sub["id"]
        self.protocol_keys = {}
        for name, group in self.protocol_groups.items():
            self.protocol_keys[name] = self.api("/keys", {"name": "V2 " + name, "billing_source": "subscription", "subscription_id": self.protocol_sid, "group_id": group["id"]}, self.owner["token"])
        self.api("/admin/users/" + str(self.owner["id"]) + "/balance", {"balance": 5, "operation": "set", "notes": "synthetic protocol fixture"}, self.admin)
        self.balance_key = self.api("/keys", {"name": "V2 balance media", "billing_source": "balance", "group_id": self.protocol_groups["images"]["id"]}, self.owner["token"])

    def logs(self, uid):
        return self.sql(f"SELECT row_to_json(x) FROM (SELECT id,request_id,api_key_id,subscription_id,group_id,model,input_tokens,output_tokens,total_cost,actual_cost,rate_multiplier,route_billing_snapshot FROM usage_logs WHERE user_id={int(uid)} ORDER BY id) x;")

    def balance(self, uid):
        return self.sql(f"SELECT json_build_object('balance',balance) FROM users WHERE id={int(uid)};")[0]["balance"]

    def charge_baseline(self, user, sid):
        # Async workers can settle between marking mock output ready and the
        # first client status poll. Capture money BEFORE submitting creation.
        return {"user_id": user["id"], "subscription_id": sid,
                "daily_usage": self.subscription(user, sid)["quota_summary"]["daily_usage_usd"],
                "balance": self.balance(user["id"]), "logs": self.logs(user["id"])}

    def charged(self, label, key, path, payload, raw_cost, rate, group, user=None, sid=None, check_body=None, baseline=None, financial_request_id=None):
        user = user or self.owner
        sid = sid or self.protocol_sid
        if financial_request_id and baseline is None:
            raise AssertionError("async charge requires a pre-create baseline")
        baseline = baseline or self.charge_baseline(user, sid)
        if baseline["user_id"] != user["id"] or baseline["subscription_id"] != sid:
            raise AssertionError("charge baseline identity mismatch")
        before, bal, old = baseline["daily_usage"], baseline["balance"], baseline["logs"]
        last = old[-1]["id"] if old else 0
        status, body, _ = self.request(path, payload, key["key"])
        self.check(label + " HTTP200", status == 200, {"status": status, "body": str(body)[:500]})
        if check_body:
            self.check(label + " protocol shape", check_body(body), str(body)[:500])
        deadline = time.monotonic() + 120 if financial_request_id else None
        fresh, observed = [], []
        for _ in range(2400 if financial_request_id else 100):
            if deadline is not None and time.monotonic() >= deadline:
                break
            observed = self.logs(user["id"])
            if financial_request_id:
                fresh = [row for row in observed if row["request_id"] == financial_request_id and row["api_key_id"] == key["id"]]
            else:
                fresh = [row for row in observed if row["id"] > last]
            if fresh:
                break
            time.sleep(0.05)
        self.check(label + " one usage record", len(fresh) == 1, fresh)
        row = fresh[0]
        if financial_request_id:
            # A pre-existing unrelated log cannot satisfy this check; the one
            # newly accepted task has exactly one stable financial/log identity.
            old_ids = {item["id"] for item in old}
            new_rows = [item for item in observed if item["id"] not in old_ids]
            self.check(label + " one new task record since creation", row["id"] not in old_ids and len(new_rows) == 1, new_rows)
        self.check(label + " exact price and group multiplier", close_amount(row["total_cost"], raw_cost) and close_amount(row["actual_cost"], Decimal(str(raw_cost)) * Decimal(str(rate))) and close_amount(row["rate_multiplier"], rate) and row["group_id"] == group["id"], row)
        if key["billing_source"] == "subscription":
            after = self.await_usage(user, sid, Decimal(str(before)) + Decimal(str(raw_cost)) * Decimal(str(rate)))
            self.check(label + " same subscription ledger and balance unchanged", row["subscription_id"] == sid and close_amount(self.balance(user["id"]), bal) and close_amount(after["quota_summary"]["daily_usage_usd"], Decimal(str(before)) + Decimal(str(raw_cost)) * Decimal(str(rate))))
        else:
            self.check(label + " balance only with no subscription debit", row["subscription_id"] is None and close_amount(self.subscription(user, sid)["quota_summary"]["daily_usage_usd"], before) and close_amount(self.balance(user["id"]), Decimal(str(bal)) - Decimal(str(raw_cost)) * Decimal(str(rate))))
        return body, row

    def verify_protocols(self):
        keys, groups = self.protocol_keys, self.protocol_groups
        for stream in (False, True):
            suffix = " SSE" if stream else " JSON"
            self.charged("Chat Completions" + suffix, keys["openai"], "/v1/chat/completions", {"model": CHAT, "messages": [{"role": "user", "content": "synthetic"}], "max_tokens": 16, "stream": stream}, "0.0012", "1.25", groups["openai"], check_body=lambda b: "acceptance ok" in str(b))
            self.charged("Responses" + suffix, keys["openai"], "/v1/responses", {"model": CHAT, "input": "synthetic", "max_output_tokens": 16, "stream": stream}, "0.0012", "1.25", groups["openai"], check_body=lambda b: "acceptance ok" in str(b))
            self.charged("Anthropic Messages" + suffix, keys["anthropic"], "/v1/messages", {"model": ANTHROPIC, "messages": [{"role": "user", "content": "synthetic"}], "max_tokens": 16, "stream": stream}, "0.0012", "1.5", groups["anthropic"], check_body=lambda b: "acceptance ok" in str(b))
            action = "streamGenerateContent?alt=sse" if stream else "generateContent"
            self.charged("Gemini native" + suffix, keys["gemini"], "/v1beta/models/" + GEMINI + ":" + action, {"contents": [{"role": "user", "parts": [{"text": "synthetic"}]}], "generationConfig": {"maxOutputTokens": 16}}, "0.0012", "0.75", groups["gemini"], check_body=lambda b: "acceptance ok" in str(b))
        self.charged("Embeddings", keys["openai"], "/v1/embeddings", {"model": EMBEDDING, "input": "synthetic vector input"}, "0.001", "1.25", groups["openai"], check_body=lambda b: isinstance(b, dict) and len(b["data"][0]["embedding"]) == 3)
        image_body = {"model": IMAGE, "prompt": "synthetic one pixel", "n": 2, "size": "1024x1024"}
        self.charged("Synchronous images group image price only", keys["images"], "/v1/images/generations", image_body, "0.5", "1.2", groups["images"], check_body=lambda b: isinstance(b, dict) and len(b.get("data", [])) == 2)
        self.charged("Streaming image completed output", keys["images"], "/v1/images/generations", {**image_body, "n": 1, "stream": True}, "0.25", "1.2", groups["images"], check_body=lambda b: "image_generation.completed" in str(b) and PNG in str(b))
        self.charged("Balance images accept non-token group price", self.balance_key, "/v1/images/generations", {**image_body, "n": 1}, "0.25", "0.5", groups["images"])
        self.api("/admin/groups/" + str(groups["openai"]["id"]), {"subscription_rate_multiplier": 0}, self.admin, "PUT")
        self.charged("Zero subscription multiplier", keys["openai"], "/v1/chat/completions", {"model": CHAT, "messages": [{"role": "user", "content": "free synthetic"}], "max_tokens": 16}, "0.0012", "0", groups["openai"])
        self.api("/admin/groups/" + str(groups["openai"]["id"]), {"subscription_rate_multiplier": 1.25}, self.admin, "PUT")
        self.api("/admin/groups/" + str(groups["images"]["id"]), {"subscription_rate_multiplier": 0}, self.admin, "PUT")
        self.charged("Zero subscription image multiplier", keys["images"], "/v1/images/generations", {**image_body, "n": 1}, "0.25", "0", groups["images"])
        self.api("/admin/groups/" + str(groups["images"]["id"]), {"subscription_rate_multiplier": 1.2}, self.admin, "PUT")

    def verify_failed_and_fallback(self):
        before = self.logs(self.owner["id"])
        used = self.subscription(self.owner)["quota_summary"]["daily_usage_usd"]
        status, body, _ = self.request("/v1/chat/completions", {"model": CHAT, "messages": [{"role": "user", "content": "reject synthetic"}], "max_tokens": 16}, self.protocol_keys["failure"]["key"])
        self.check("upstream rejected request is not billed", 400 <= status < 600 and self.logs(self.owner["id"]) == before and self.subscription(self.owner)["quota_summary"]["daily_usage_usd"] == used, {"status": status, "body": body})
        multi = self.api("/keys", {"name": "V2 fallback same quota", "billing_source": "subscription", "routing_mode": "composite", "subscription_id": self.protocol_sid, "group_ids": [self.protocol_groups["down"]["id"], self.protocol_groups["openai"]["id"]]}, self.owner["token"])
        for stream in (False, True):
            _, row = self.charged("Cross-group fallback " + ("SSE" if stream else "JSON"), multi, "/v1/chat/completions", {"model": CHAT, "messages": [{"role": "user", "content": "fallback synthetic"}], "max_tokens": 16, "stream": stream}, "0.0012", "1.25", self.protocol_groups["openai"])
            attempts = (row.get("route_billing_snapshot") or {}).get("attempts", [])
            self.check("fallback preserves both route attempts without duplicate charge", len(attempts) == 2 and attempts[0]["group_id"] == self.protocol_groups["down"]["id"] and attempts[1]["group_id"] == self.protocol_groups["openai"]["id"], attempts)

    def verify_rejected_routes_and_daily_limit(self):
        user, sid = self.owner, self.protocol_sid
        key = self.protocol_keys["openai"]
        old = self.logs(user["id"])
        used = self.subscription(user)["quota_summary"]["daily_usage_usd"]
        before = self.sql(f"SELECT json_build_object('pending',COUNT(*)) FROM subscription_requests WHERE subscription_id={sid} AND status='admitted';")[0]["pending"]
        invalid = [("unknown model", "/v1/chat/completions", {"model": "synthetic-unsupported-model", "messages": []}, key), ("missing model", "/v1/chat/completions", {"messages": []}, key), ("unsupported responses subpath", "/v1/responses/%3Finvalid", {"model": CHAT, "input": "no"}, key), ("unsupported video platform", "/v1/videos/generations", {"model": CHAT, "prompt": "no"}, key), ("streaming async image", "/v1/images/generations/async", {"model": IMAGE, "prompt": "no", "stream": True}, self.protocol_keys["images"])]
        for label, path, payload, auth in invalid:
            status, body, _ = self.request(path, payload, auth["key"])
            self.check(label + " rejected without financial use", 400 <= status < 500 and self.logs(user["id"]) == old and close_amount(self.subscription(user)["quota_summary"]["daily_usage_usd"], used), {"status": status, "body": body})
        pending = self.sql(f"SELECT json_build_object('pending',COUNT(*)) FROM subscription_requests WHERE subscription_id={sid} AND status='admitted';")[0]["pending"]
        self.check("pre-dispatch validation failures release admission records", pending == before, {"before": before, "after": pending})
        self.sql(f"UPDATE subscription_daily_usage SET used_usd=180 WHERE subscription_id={sid} AND term_id=(SELECT term_id FROM subscription_contracts WHERE subscription_id={sid}) AND usage_date=(NOW() AT TIME ZONE 'Asia/Shanghai')::DATE;")
        routes = [("chat", "/v1/chat/completions", {"model": CHAT, "messages": [{"role": "user", "content": "no budget"}]}, "openai"), ("responses", "/v1/responses", {"model": CHAT, "input": "no budget"}, "openai"), ("messages", "/v1/messages", {"model": ANTHROPIC, "messages": [{"role": "user", "content": "no budget"}], "max_tokens": 1}, "anthropic"), ("gemini", "/v1beta/models/" + GEMINI + ":generateContent", {"contents": [{"parts": [{"text": "no budget"}]}]}, "gemini"), ("embeddings", "/v1/embeddings", {"model": EMBEDDING, "input": "no budget"}, "openai"), ("images", "/v1/images/generations", {"model": IMAGE, "prompt": "no budget"}, "images"), ("video", "/v1/videos/generations", {"model": VIDEO, "prompt": "no budget", "duration": 5}, "video")]
        call_count = len(self.upstream_calls)
        for label, path, payload, group in routes:
            status, body, headers = self.request(path, payload, self.protocol_keys[group]["key"])
            headers = {key.lower(): value for key, value in headers.items()}
            self.check(label + " shares daily429 metadata and Retry-After", status == 429 and "DAILY_LIMIT_EXCEEDED" in str(body) and int(headers.get("retry-after", "0")) > 0, {"status": status, "body": body})
        self.check("exhausted protocol requests never reach upstream or settle", len(self.upstream_calls) == call_count and self.logs(user["id"]) == old)
        self.sql(f"UPDATE subscription_daily_usage SET used_usd={Decimal(str(used))} WHERE subscription_id={sid} AND term_id=(SELECT term_id FROM subscription_contracts WHERE subscription_id={sid}) AND usage_date=(NOW() AT TIME ZONE 'Asia/Shanghai')::DATE;")

    def verify_video_outcomes(self):
        user = self.user("video-outcomes")
        self.pay(user, self.quote(user, "month45", "purchase", units=1))
        sub = self.subscription(user)
        sid = sub["id"]
        group = self.protocol_groups["video"]
        key = self.api("/keys", {"name": "V2 video outcomes", "billing_source": "subscription", "subscription_id": sid, "group_id": group["id"]}, user["token"])
        payload = {"model": VIDEO, "prompt": "synthetic outcome", "duration": 5, "resolution": "720p"}
        status, body, _ = self.request("/v1/videos/generations", payload, key["key"])
        self.check("failed-video fixture creation accepted", status == 200, body)
        task = body["request_id"]
        with self.lock:
            self.videos[task]["status"] = "failed"
        status, failed, _ = self.request("/v1/videos/" + task, token=key["key"])
        self.check("failed async video never charges quota", status == 200 and failed.get("status") == "failed" and not self.logs(user["id"]) and self.subscription(user)["quota_summary"]["daily_usage_usd"] == 0, failed)
        self.api("/admin/groups/" + str(group["id"]), {"subscription_rate_multiplier": 0}, self.admin, "PUT")
        zero_baseline = self.charge_baseline(user, sid)
        status, body, _ = self.request("/v1/videos/generations", payload, key["key"])
        self.check("zero-rate video creation accepted", status == 200, body)
        task = body["request_id"]
        with self.lock:
            self.videos[task]["status"] = "done"
        self.charged("Zero subscription video multiplier", key, "/v1/videos/" + task, None, "0.35", "0", group, user=user, sid=sid, baseline=zero_baseline, financial_request_id="grok-video:" + task)
        self.api("/admin/groups/" + str(group["id"]), {"subscription_rate_multiplier": 1.5}, self.admin, "PUT")
        self.api("/admin/users/" + str(user["id"]) + "/balance", {"balance": 5, "operation": "set", "notes": "synthetic video balance"}, self.admin)
        balance = self.api("/keys", {"name": "V2 video balance price only", "billing_source": "balance", "group_id": group["id"]}, user["token"])
        balance_baseline = self.charge_baseline(user, sid)
        status, body, _ = self.request("/v1/videos/generations", payload, balance["key"])
        self.check("balance video uses non-token group price", status == 200, body)
        task = body["request_id"]
        with self.lock:
            self.videos[task]["status"] = "done"
        self.charged("Balance video group seconds price", balance, "/v1/videos/" + task, None, "0.35", "0.5", group, user=user, sid=sid, baseline=balance_baseline, financial_request_id="grok-video:" + task)

    def verify_video_cross_expiry(self):
        user = self.user("video-expiry")
        self.pay(user, self.quote(user, "month45", "purchase", units=1))
        sub = self.subscription(user)
        sid, term = sub["id"], sub["contract"]["term_id"]
        key = self.api("/keys", {"name": "V2 video frozen original owner", "billing_source": "subscription", "subscription_id": sid, "group_id": self.protocol_groups["video"]["id"]}, user["token"])
        before = self.logs(user["id"])
        status, body, _ = self.request("/v1/videos/generations", {"model": VIDEO, "prompt": "synthetic waves", "duration": 5, "resolution": "720p"}, key["key"])
        self.check("asynchronous video creation accepted without immediate billing", status == 200 and bool(body.get("request_id")) and self.logs(user["id"]) == before, {"status": status, "body": body})
        task = body["request_id"]
        status, pending, _ = self.request("/v1/videos/" + task, token=key["key"])
        self.check("video pending poll creates no debit", status == 200 and pending.get("status") == "pending" and self.logs(user["id"]) == before)
        self.fixture_expiry(user, sub, -1)
        self.pay(user, self.quote(user, "month45", "purchase", units=1))
        current = self.subscription(user)
        self.check("new contract term is separate before old video completes", current["contract"]["term_id"] != term and current["quota_summary"]["daily_usage_usd"] == 0)
        with self.lock:
            self.videos[task]["status"] = "done"
        status, done, _ = self.request("/v1/videos/" + task, token=key["key"])
        self.check("old video lookup works after original term expires", status == 200 and done.get("status") == "done", {"status": status, "body": done})
        for _ in range(100):
            rows = self.logs(user["id"])
            if len(rows) > len(before):
                break
            time.sleep(0.05)
        self.check("video completion bills group price by seconds exactly once", len(rows) == len(before) + 1 and close_amount(rows[-1]["total_cost"], "0.35") and close_amount(rows[-1]["actual_cost"], "0.525") and rows[-1]["subscription_id"] == sid, rows)
        ledger = self.sql(f"SELECT json_build_object('term_id',term_id,'used',SUM(used_usd)) FROM subscription_daily_usage WHERE subscription_id={sid} GROUP BY term_id;")
        self.check("late video debits original term leaving new period unused", any(row["term_id"] == term and close_amount(row["used"], "0.525") for row in ledger) and self.subscription(user)["quota_summary"]["daily_usage_usd"] == 0, ledger)
        with ThreadPoolExecutor(max_workers=4) as pool:
            statuses = list(pool.map(lambda _: self.request("/v1/videos/" + task, token=key["key"])[0], range(4)))
        self.check("concurrent completed video polls do not double bill", statuses == [200] * 4 and len(self.logs(user["id"])) == len(rows))
        foreign = self.protocol_keys["video"]
        status, body, _ = self.request("/v1/videos/" + task, token=foreign["key"])
        self.check("another user's key cannot poll owned video", status in (403, 404), {"status": status, "body": body})

    def verify_inflight_lifecycle(self):
        user = self.user("inflight-expiry")
        self.pay(user, self.quote(user, "month45", "purchase", units=1))
        sub = self.subscription(user)
        sid, term = sub["id"], sub["contract"]["term_id"]
        key = self.api("/keys", {"name": "V2 inflight old term", "billing_source": "subscription", "subscription_id": sid, "group_id": self.protocol_groups["slow"]["id"]}, user["token"])
        self.slow_entered.clear()
        self.slow_release.clear()
        with ThreadPoolExecutor(max_workers=1) as pool:
            result = pool.submit(self.request, "/v1/chat/completions", {"model": CHAT, "messages": [{"role": "user", "content": "slow synthetic"}], "max_tokens": 16, "stream": True}, key["key"])
            try:
                self.check("streaming request has been admitted before term mutation", self.slow_entered.wait(10))
                self.fixture_expiry(user, sub, -1)
                self.pay(user, self.quote(user, "month45", "purchase", units=1))
            finally:
                self.slow_release.set()
            status, body, _ = result.result(timeout=20)
        self.check("already admitted stream completes across expiry and repurchase", status == 200 and "acceptance ok" in str(body), {"status": status, "body": str(body)[:250]})
        for _ in range(100):
            logs = self.logs(user["id"])
            if logs:
                break
            time.sleep(0.05)
        ledger = self.sql(f"SELECT json_build_object('term_id',term_id,'used',SUM(used_usd)) FROM subscription_daily_usage WHERE subscription_id={sid} GROUP BY term_id;")
        self.check("inflight stream settles original term once without new-term debit", len(logs) == 1 and any(row["term_id"] == term and close_amount(row["used"], "0.0015") for row in ledger) and self.subscription(user)["quota_summary"]["daily_usage_usd"] == 0, ledger)

    def verify_inflight_midnight(self):
        user = self.user("inflight-midnight")
        self.pay(user, self.quote(user, "month45", "purchase", units=1))
        sub = self.subscription(user)
        sid = sub["id"]
        self.sql(f"BEGIN; UPDATE user_subscriptions SET starts_at=starts_at-INTERVAL '1 day' WHERE id={sid}; UPDATE subscription_contracts SET starts_at=starts_at-INTERVAL '1 day' WHERE subscription_id={sid}; UPDATE subscription_contract_terms SET starts_at=starts_at-INTERVAL '1 day' WHERE subscription_id={sid}; UPDATE user_subscription_entitlements SET starts_at=starts_at-INTERVAL '1 day' WHERE user_subscription_id={sid}; COMMIT;")
        key = self.api("/keys", {"name": "V2 original Beijing date", "billing_source": "subscription", "subscription_id": sid, "group_id": self.protocol_groups["slow"]["id"]}, user["token"])
        self.slow_entered.clear()
        self.slow_release.clear()
        with ThreadPoolExecutor(max_workers=1) as pool:
            result = pool.submit(self.request, "/v1/chat/completions", {"model": CHAT, "messages": [{"role": "user", "content": "synthetic yesterday admission"}], "max_tokens": 16, "stream": True}, key["key"])
            try:
                self.check("midnight fixture pauses after admission", self.slow_entered.wait(10))
                # Simulate an admission just before Beijing midnight without
                # changing the host clock or timestamps of any other user.
                self.sql(f"BEGIN; UPDATE subscription_requests SET admitted_at=admitted_at-INTERVAL '1 day' WHERE subscription_id={sid} AND status='admitted'; UPDATE subscription_request_contracts SET usage_date=usage_date-1 WHERE request_key IN (SELECT request_key FROM subscription_requests WHERE subscription_id={sid} AND status='admitted'); COMMIT;")
            finally:
                self.slow_release.set()
            status, body, _ = result.result(timeout=20)
        self.check("original-date stream completes after simulated midnight", status == 200 and "acceptance ok" in str(body))
        for _ in range(100):
            if self.logs(user["id"]):
                break
            time.sleep(0.05)
        previous = self.sql(f"SELECT json_build_object('used',COALESCE(SUM(used_usd),0)) FROM subscription_daily_usage WHERE subscription_id={sid} AND usage_date=(NOW() AT TIME ZONE 'Asia/Shanghai')::DATE-1;")[0]["used"]
        self.check("late stream settles captured Beijing date leaving today unused", close_amount(previous, "0.0015") and self.subscription(user)["quota_summary"]["daily_usage_usd"] == 0 and len(self.logs(user["id"])) == 1, {"previous_day_used": previous})

    def verify_concurrent_actual_settlement(self):
        user = self.user("concurrent-budget")
        self.pay(user, self.quote(user, "month45", "purchase", units=1))
        sub = self.subscription(user)
        sid = sub["id"]
        key = self.api("/keys", {"name": "V2 admitted actual settlement", "billing_source": "subscription", "subscription_id": sid, "group_id": self.protocol_groups["slow"]["id"]}, user["token"])
        self.sql(f"UPDATE subscription_daily_usage SET used_usd=44.9999 WHERE subscription_id={sid} AND term_id=(SELECT term_id FROM subscription_contracts WHERE subscription_id={sid}) AND usage_date=(NOW() AT TIME ZONE 'Asia/Shanghai')::DATE;")
        self.slow_entered.clear()
        self.slow_release.clear()
        count = len([call for call in self.upstream_calls if "/slow/" in call["path"]])
        with ThreadPoolExecutor(max_workers=4) as pool:
            results = [pool.submit(self.request, "/v1/chat/completions", {"model": CHAT, "messages": [{"role": "user", "content": "synthetic parallel " + str(i)}], "max_tokens": 16}, key["key"]) for i in range(4)]
            try:
                for _ in range(100):
                    current = len([call for call in self.upstream_calls if "/slow/" in call["path"]])
                    if current >= count + 4:
                        break
                    time.sleep(0.05)
                self.check("four requests admitted before last remaining budget settles", current == count + 4, {"admitted": current - count})
            finally:
                self.slow_release.set()
            statuses = [result.result(timeout=20)[0] for result in results]
        self.await_usage(user, sid, "45.0059")
        self.check("admitted requests finish and record all actual costs once", statuses == [200] * 4 and len(self.logs(user["id"])) == 4)
        status, _, _ = self.request("/v1/chat/completions", {"model": CHAT, "messages": [{"role": "user", "content": "exhausted"}]}, key["key"])
        self.check("subsequent request rejected after concurrent actual exhaustion", status == 429 and len(self.logs(user["id"])) == 4)

    def verify_comprehensive_paths(self):
        """Reusable Stage adapter entry after its guarded setup/snapshot boundary."""
        self.setup_protocols()
        self.verify_protocols()
        self.verify_failed_and_fallback()
        self.verify_rejected_routes_and_daily_limit()
        self.verify_video_outcomes()
        self.verify_video_cross_expiry()
        self.verify_inflight_lifecycle()
        self.verify_inflight_midnight()
        self.verify_concurrent_actual_settlement()

    def run(self):
        self.start_mock()
        try:
            self.setup()
            if not self.protocol_only:
                acquired = self.verify_tiers()
                self.verify_changes(acquired)
                self.verify_concurrency()
                self.verify_refund(acquired)
                self.verify_refund_freeze()
                self.verify_paid_conflict_and_expiry(acquired)
                self.verify_legacy_day_ledger()
                self.verify_migration_edge_cases()
            self.verify_comprehensive_paths()
            print(f"Comprehensive synthetic lifecycle/protocol acceptance: {len(self.checks)} checks passed", flush=True)
            print("Report: " + str(self.private / "report.json"), flush=True)
        finally:
            self.slow_release.set()
            if self.server:
                self.server.shutdown()
                self.server.server_close()


def arguments():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--base", default=os.getenv("GEILI_V2_BASE", "http://127.0.0.1:18494"))
    parser.add_argument("--database", default=os.getenv("GEILI_V2_DB", "acceptance_v2_comprehensive_ready"))
    parser.add_argument("--project", default=os.getenv("GEILI_ACCEPTANCE_PROJECT", "geili-acceptance-local"))
    parser.add_argument("--mock-port", type=int, default=19013)
    parser.add_argument("--protocol-only", action="store_true")
    return parser.parse_args()


if __name__ == "__main__":
    ComprehensiveFixture(arguments()).run()
