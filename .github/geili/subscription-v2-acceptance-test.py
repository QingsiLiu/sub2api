#!/usr/bin/env python3
"""Safety and monetary fixture checks; no HTTP, Docker, database or credentials."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("v2_acceptance", Path(__file__).with_name("subscription-v2-acceptance.py"))
v2 = importlib.util.module_from_spec(spec)
spec.loader.exec_module(v2)


class LocalFixtureSafety(unittest.TestCase):
    def test_rejects_nonlocal_and_ambiguous_targets(self):
        for target in ("https://sub.geiliapi.com", "http://127.0.0.1.evil.invalid:1234", "http://127.0.0.1:1234@8.8.8.8:80", "http://localhost:1234", "http://127.0.0.1:1234/path", "http://127.0.0.1:1234?next=prod", "http://127.0.0.1:1234#prod", "http://127.0.0.1"):
            with self.subTest(target=target), self.assertRaises(ValueError):
                v2.local_base(target)

    def test_allows_explicit_loopback_only(self):
        self.assertEqual(v2.local_base("http://127.0.0.1:18494/"), "http://127.0.0.1:18494")
        self.assertEqual(v2.local_base("http://[::1]:18494"), "http://[::1]:18494")

    def test_known_monetary_fixtures_round_total_only(self):
        self.assertEqual(str(v2.prorated("10.01", 2, 3, 30)), "2.00")
        self.assertEqual(str(v2.prorated("30.04", 4, 63, 30)), "252.34")
        self.assertEqual(str(v2.prorated("40.05", 1, 1, 30)), "1.34")
        self.assertEqual(str(v2.prorated("0.01", 3, 15, 30)), "0.02")

    def test_secret_material_is_redacted_from_reports(self):
        result = v2.safe_detail({"quote_id": "signed-quote", "nested": [{"access_token": "secret", "amount": 2}]})
        self.assertEqual(result, {"quote_id": "[redacted]", "nested": [{"access_token": "[redacted]", "amount": 2}]})

    def test_signature_excludes_protocol_fields_and_empty_values(self):
        fields = {"pid": "fixture", "money": "1.00", "empty": "", "sign": "old", "sign_type": "MD5"}
        self.assertEqual(v2.signature(fields, "secret"), v2.signature({"money": "1.00", "pid": "fixture"}, "secret"))


if __name__ == "__main__":
    unittest.main()
