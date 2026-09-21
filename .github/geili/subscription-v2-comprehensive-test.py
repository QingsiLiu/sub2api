#!/usr/bin/env python3
"""Protocol fixture contracts; no Docker, credentials or live HTTP required."""
import importlib.util
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("comprehensive", Path(__file__).with_name("subscription-v2-comprehensive.py"))
c = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(c)


class ProtocolFixtureContracts(unittest.TestCase):
    def test_image_fixture_counts_outputs_without_token_usage(self):
        status, body = c.mock_reply("/protocol/images/v1/images/generations", {"model": c.IMAGE, "n": 2}, {})
        self.assertEqual(status, 200)
        self.assertEqual(len(body["data"]), 2)
        self.assertNotIn("usage", body)
        self.assertTrue(all(item["b64_json"] == c.PNG for item in body["data"]))

    def test_embedding_fixture_reports_input_usage_only(self):
        _, body = c.mock_reply("/protocol/openai/v1/embeddings", {"model": c.EMBEDDING}, {})
        self.assertEqual(body["usage"], {"prompt_tokens": 1000, "total_tokens": 1000})
        self.assertEqual(body["data"][0]["object"], "embedding")

    def test_gemini_native_fixture_is_not_openai_envelope(self):
        _, body = c.mock_reply("/protocol/gemini/v1beta/models/gemini-2.5-flash:streamGenerateContent", {}, {})
        self.assertEqual(body["usageMetadata"]["totalTokenCount"], 1100)
        self.assertEqual(body["candidates"][0]["finishReason"], "STOP")
        self.assertNotIn("choices", body)

    def test_video_creation_preserves_duration_but_does_not_complete(self):
        tasks = {}
        status, body = c.mock_reply("/protocol/video/v1/videos/generations", {"model": c.VIDEO, "duration": 5}, tasks)
        self.assertEqual(status, 200)
        self.assertEqual(body["status"], "pending")
        self.assertEqual(tasks[body["request_id"]]["duration"], 5)
        self.assertNotIn("video", body)

    def test_failure_routes_never_return_billable_usage(self):
        for name, expected in (("failure", 400), ("down", 503)):
            status, body = c.mock_reply("/protocol/" + name + "/v1/chat/completions", {}, {})
            self.assertEqual(status, expected)
            self.assertIn("error", body)
            self.assertNotIn("usage", body)

    def test_accounting_comparison_preserves_small_actual_costs(self):
        self.assertTrue(c.close_amount("0.0015", "0.0015000000"))
        self.assertFalse(c.close_amount("0.0015", "0.0016"))
        self.assertFalse(c.close_amount("0.00001", "0"))


if __name__ == "__main__":
    unittest.main()
