#!/usr/bin/env python3
"""Verify the acceptance oracle and retry-disabled Python adapters themselves."""
import http.server
import importlib.util
from pathlib import Path
import threading
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("python_reliability", Path(__file__).with_name("python-reliability-acceptance.py"))
h = importlib.util.module_from_spec(spec)
spec.loader.exec_module(h)


class OracleTests(unittest.TestCase):
    def test_usage_or_done_is_not_completion(self):
        for kind in h.ENDPOINTS:
            with self.subTest(kind=kind):
                self.assertFalse(h.semantic_outcome(kind, True, [{"usage": {"output_tokens": 100}}, {"type": "[DONE]"}])["success"])

    def test_native_terminals_and_failure(self):
        for kind in h.ENDPOINTS:
            result = h.response_object(kind, h.MODELS[0], "fixture")
            frames = b"".join(h.encode_frame(*frame) for frame in h.response_frames(kind, result)).decode()
            self.assertTrue(h.semantic_outcome(kind, True, h.parse_sse(frames))["success"])
            self.assertFalse(h.semantic_outcome(kind, True, h.parse_sse(frames) + [{"type": "error", "error": {"message": "failed"}}])["success"])

    def test_images_count_blocks_not_unique_payloads(self):
        for kind in h.ENDPOINTS:
            for suffix in ("", "_url"):
                self.assertEqual(h.count_images(h.payload(kind, h.MODELS[1], False, "images50" + suffix)), 50)
                self.assertEqual(h.count_images(h.payload(kind, h.MODELS[1], False, "images51" + suffix)), 51)

    def test_observed_partial_usage_requires_one_exact_settlement(self):
        fixture = h.ReliabilityFixture.__new__(h.ReliabilityFixture)
        before = {"balance": 100, "quota": 0}
        for count, charge, expected in ((0, "0", False), (1, "0.001", True), (2, "0.002", False), (1, "0.0012", False)):
            with self.subTest(count=count, charge=charge):
                row = {"receipts": count, "settled": count, "delivered": count, "logs": count, "charged": charge, "input_tokens": count * 1000, "output_tokens": 0}
                fixture.sql = lambda _: [dict(row)]
                fixture.snapshot = lambda _: {"balance": h.Decimal("100") - h.Decimal(charge), "quota": h.Decimal(charge)}
                with mock.patch.object(h, "wait_for", return_value=dict(row)):
                    result = fixture.settlement({"id": 1}, before, False, observed_failure=True)
                self.assertEqual(result["valid"], expected)

    def test_docker_gateway_uses_only_observed_ipv4(self):
        with mock.patch.object(h, "run", return_value="127.0.0.1 localhost\nfd00::1 host.docker.internal\n192.168.65.254 host.docker.internal\n"):
            self.assertEqual(h.docker_host_ipv4("owned-fixture"), "192.168.65.254")
        with mock.patch.object(h, "run", return_value="fd00::1 host.docker.internal\n"):
            with self.assertRaises(RuntimeError):
                h.docker_host_ipv4("owned-fixture")

    def test_tool_integer_survives_frame_and_json(self):
        for kind in h.ENDPOINTS:
            result = h.response_object(kind, h.MODELS[0], "fixture", "tool")
            self.assertTrue(h.semantic_outcome(kind, False, result)["tool_nonce_preserved"])

    def test_crlf_and_comments(self):
        data = ': comment\r\n\r\nevent: response.completed\r\ndata: {"response":{"status":"completed"}}\r\n\r\n'
        self.assertEqual(h.parse_sse(data)[0]["type"], "response.completed")

    def test_empty_upload_eof_requires_exact_proxy_timeout_evidence(self):
        row = {"id": "upload-fixture", "status": 408, "seconds": 30.03}
        result = h.upload_timeout_evidence(b"", 30.04, "upload-fixture", [row])
        self.assertTrue(result["valid_inactivity_timeout"])
        self.assertIsNone(result["http_status"])
        self.assertEqual(result["proxy_status"], 408)
        self.assertTrue(result["transport_eof"])
        for records, elapsed in (([], 30.04), ([row, row], 30.04), ([{**row, "id": "other"}], 30.04), ([{**row, "status": 200}], 30.04), ([{**row, "seconds": 2}], 30.04), ([row], 2)):
            self.assertFalse(h.upload_timeout_evidence(b"", elapsed, "upload-fixture", records)["valid_inactivity_timeout"])


class ClientTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        h.dependencies()
        cls.state = h.ProviderState()
        cls.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), h.mock_handler(cls.state))
        cls.server.daemon_threads = True
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        cls.base = "http://127.0.0.1:" + str(cls.server.server_port)

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()

    def call(self, kind, client, stream, mode="normal"):
        ident = "case" + str(len(self.state.scenarios))
        self.state.register(ident, mode)
        result = h.client_call(client, self.base + "/" + ident + "/a", "synthetic", kind, h.payload(kind, h.MODELS[0], stream, mode), ident, timeout=5)
        self.assertEqual(len(self.state.observed(ident)), 1, (kind, client, stream, mode, result))
        return result

    def test_all_retry_disabled_clients_with_valid_protocols(self):
        for kind in h.ENDPOINTS:
            sdk = "anthropic" if kind == "messages" else "openai"
            for stream in (False, True):
                for client in ("requests", "httpx", "httpx_async", sdk, sdk + "_async"):
                    with self.subTest(kind=kind, client=client, stream=stream):
                        result = self.call(kind, client, stream)
                        self.assertTrue(result["success"], result)
            if kind != "chat/completions":
                for client in (sdk + "_final", sdk + "_final_async"):
                    with self.subTest(kind=kind, client=client):
                        result = self.call(kind, client, True)
                        self.assertTrue(result["success"], result)

    def test_supplier_failure_does_not_trigger_python_retry(self):
        for kind in h.ENDPOINTS:
            sdk = "anthropic" if kind == "messages" else "openai"
            for client in ("requests", "httpx", "httpx_async", sdk, sdk + "_async"):
                with self.subTest(kind=kind, client=client):
                    result = self.call(kind, client, False, "http503")
                    self.assertFalse(result["success"])
                    self.assertEqual(result["http_status"], 503)

    def test_thinking_frames_are_accepted_by_final_response_sdks(self):
        for kind, sdk in (("messages", "anthropic"), ("responses", "openai")):
            for client in (sdk + "_final", sdk + "_final_async"):
                with self.subTest(kind=kind, client=client):
                    result = self.call(kind, client, True, "thinking")
                    self.assertTrue(result["success"], result)
                    self.assertTrue(result["thinking_seen"], result)

    def test_stream_failure_is_not_a_successful_iterator(self):
        for kind in h.ENDPOINTS:
            sdk = "anthropic" if kind == "messages" else "openai"
            clients = (sdk,) if kind == "chat/completions" else (sdk, sdk + "_final", sdk + "_final_async")
            for client in clients:
                for mode in ("sseerror", "empty_stream", "prelude", "partial"):
                    with self.subTest(kind=kind, mode=mode, client=client):
                        result = self.call(kind, client, True, mode)
                        self.assertFalse(result["success"], result)

    def test_account_probe_is_separate_from_inference_and_does_not_inject_fault(self):
        import httpx
        ident = "probe-" + str(len(self.state.scenarios))
        self.state.register(ident, "http503", delay=20)
        result = httpx.post(self.base + "/" + ident + "/a/v1/responses", json={"model": h.MODELS[1], "input": "probe", "stream": False}, timeout=2, trust_env=False)
        self.assertEqual(result.status_code, 200)
        self.assertEqual(len(self.state.observed(ident)), 0)
        self.assertEqual(len(self.state.observed(ident, include_fixture=True)), 1)
        inference = h.client_call("requests", self.base + "/" + ident + "/a", "synthetic", "responses", h.payload("responses", h.MODELS[1], False), ident, timeout=2)
        self.assertFalse(inference["success"])
        self.assertEqual(inference["http_status"], 503)
        self.assertEqual(len(self.state.observed(ident)), 1)

    def test_signature_retry_stays_an_inference_after_thinking_removal(self):
        ident = "signature-" + str(len(self.state.scenarios))
        self.state.register(ident, "signature")
        body = h.payload("messages", h.MODELS[0], False, "signature")
        first = h.client_call("requests", self.base + "/" + ident + "/a", "synthetic", "messages", body, ident, timeout=2)
        self.assertEqual(first["http_status"], 400)
        body["messages"][0]["content"] = [item for item in body["messages"][0]["content"] if item["type"] != "thinking"]
        second = h.client_call("requests", self.base + "/" + ident + "/a", "synthetic", "messages", body, ident, timeout=2)
        self.assertTrue(second["success"])
        calls = self.state.observed(ident)
        self.assertEqual(len(calls), 2)
        self.assertTrue(all(item["tool_nonce_preserved"] and item["thinking_mode"] == {"type": "adaptive"} for item in calls))


if __name__ == "__main__":
    unittest.main()
