#!/usr/bin/env python3
"""Check capture groups in current Cloud Armor HCL string literals, offline.

This checks one API restriction; it is not a CEL or Cloud Armor compiler.
Terraform mock plans do not submit expressions to the service for compilation.
"""
import json
from pathlib import Path
import re
import unittest


SOURCE = Path(__file__).resolve().parents[1] / "security.tf"


def policy_patterns(source):
    expressions = [json.loads(literal) for literal in re.findall(
        r'\bexpression\s*=\s*("(?:[^"\\]|\\.)*")', source)]
    patterns = [pattern for expression in expressions for pattern in re.findall(
        r"\.matches\('([^']*)'\)", expression)]
    if not patterns or len(patterns) != len(re.findall(r"\.matches\(", source)):
        raise AssertionError("Every current matches() literal must be checked")
    return patterns


def check_no_capture_groups(source):
    for pattern in policy_patterns(source):
        if re.compile(pattern).groups != 0:
            raise AssertionError(f"Cloud Armor forbids capture groups: {pattern}")


class PolicyRegexTests(unittest.TestCase):
    def test_all_deployed_policy_regex_literals_have_zero_capture_groups(self):
        check_no_capture_groups(SOURCE.read_text())

    def test_password_throttle_covers_both_identity_namespaces(self):
        pattern = next(p for p in policy_patterns(SOURCE.read_text())
                       if "publisher-auth/login" in p)
        for path in ("/api/v1/auth/login", "/api/v1/auth/register",
                     "/api/v1/publisher-auth/login"):
            self.assertIsNotNone(re.fullmatch(pattern, path), path)
        for path in ("/api/v1/publisher-auth/logout", "/other/login",
                     "/api/v1/auth/login/extra"):
            self.assertIsNone(re.fullmatch(pattern, path), path)

    def test_regression_to_a_capturing_group_is_detected(self):
        source = SOURCE.read_text()
        self.assertIn("matches('^(?:", source)
        with self.assertRaisesRegex(AssertionError, "forbids capture groups"):
            check_no_capture_groups(source.replace("matches('^(?:", "matches('^(", 1))


if __name__ == "__main__":
    unittest.main()
