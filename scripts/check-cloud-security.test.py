"""Exercise IAM regression detection without contacting Google Cloud."""

import importlib.util
import io
from pathlib import Path
import subprocess
from types import SimpleNamespace
import unittest
from unittest.mock import patch


SPEC = importlib.util.spec_from_file_location(
    "check_cloud_security", Path(__file__).with_name("check-cloud-security.py")
)
CHECK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK)
PROJECT = "rangle-example"
RUNTIME = "rangle-stg-runtime@" + PROJECT + ".iam.gserviceaccount.com"
DEFAULT = "123456789012-compute@developer.gserviceaccount.com"


class SecurityCheckTests(unittest.TestCase):
    def setUp(self):
        self.args = SimpleNamespace(
            project=PROJECT, account="operator@example.com", region="asia-northeast1",
            service="rangle-stg", runtime_service_account=RUNTIME,
            service_account=[], bucket=["rangle-example-catalog"],
        )
        self.project_policy = {"bindings": [{"role": "roles/editor", "members": ["user:operator@example.com"]}]}
        self.bucket_policy = {"bindings": [{"role": "roles/storage.legacyBucketOwner", "members": ["projectEditor:" + PROJECT]}]}
        self.service = {"metadata": {"name": "rangle-stg", "annotations": {
            "run.googleapis.com/ingress": "internal-and-cloud-load-balancing"}},
            "spec": {"template": {"spec": {"serviceAccountName": RUNTIME}}}}
        self.keys = []
        self.bucket_metadata = {"projectNumber": "123456789012", "iamConfiguration": {
            "publicAccessPrevention": "enforced", "uniformBucketLevelAccess": {"enabled": True}}}
        self.calls = []

    def read(self, label, *command):
        self.calls.append(command)
        if label == "project identity":
            return {"projectId": PROJECT, "projectNumber": "123456789012"}
        if label == "project IAM":
            return self.project_policy
        if label == "Cloud Run service":
            return self.service
        if label == "service account key metadata":
            return self.keys
        if label == "bucket IAM":
            return self.bucket_policy
        if label == "bucket metadata":
            return self.bucket_metadata
        self.fail("Unexpected metadata probe: " + label)

    def result(self):
        return CHECK.check_cloud(self.args, self.read)

    def test_unrelated_operator_and_bucket_owner_roles_are_preserved(self):
        self.assertTrue(self.result()["ok"])

    def test_conditional_editor_binding_is_still_reported(self):
        self.project_policy["bindings"].append({
            "role": "roles/editor", "members": ["serviceAccount:" + DEFAULT],
            "condition": {"expression": "request.time < timestamp('2026-11-14T15:00:00Z')"},
        })
        result = self.result()
        self.assertFalse(result["ok"])
        self.assertEqual(result["checks"][0]["roles"], ["roles/editor"])

    def test_replacing_editor_with_another_basic_role_is_reported(self):
        self.project_policy["bindings"].append({"role": "roles/viewer", "members": ["serviceAccount:" + DEFAULT]})
        self.assertFalse(self.result()["ok"])

    def test_implicit_default_runtime_and_public_ingress_are_reported(self):
        self.service["spec"]["template"]["spec"].clear()
        self.service["metadata"]["annotations"]["run.googleapis.com/ingress"] = "all"
        result = self.result()
        self.assertFalse(result["checks"][1]["ok"])
        self.assertFalse(result["checks"][2]["ok"])

    def test_disabled_user_managed_key_is_not_accepted_as_keyless(self):
        self.keys = [{"name": "never-print-key-material", "disabled": True}]
        result = self.result()
        self.assertFalse(result["ok"])
        self.assertNotIn("never-print-key-material", str(result))

    def test_public_and_project_viewer_bucket_grants_are_reported(self):
        self.bucket_policy["bindings"].extend([
            {"role": "roles/storage.legacyObjectReader", "members": ["projectViewer:" + PROJECT]},
            {"role": "roles/storage.objectViewer", "members": ["allUsers"]},
        ])
        self.assertEqual([item["check"] for item in self.result()["checks"] if not item["ok"]], [
            "no_project_viewer_bucket_convenience_grant", "no_public_bucket_iam_grant",
        ])

    def test_resource_identity_mismatch_fails_verification(self):
        self.service["metadata"]["name"] = "unrelated"
        with self.assertRaisesRegex(CHECK.VerificationError, "does not match"):
            self.result()

    def test_bucket_of_another_project_fails_before_reading_its_policy(self):
        self.bucket_metadata["projectNumber"] = "999999999999"
        with self.assertRaisesRegex(CHECK.VerificationError, "ownership"):
            self.result()
        self.assertFalse(any(command[:4] == ("storage", "buckets", "get-iam-policy", "gs://rangle-example-catalog")
                             for command in self.calls))

    def test_bucket_acl_or_public_access_regression_is_reported(self):
        self.bucket_metadata["iamConfiguration"]["uniformBucketLevelAccess"]["enabled"] = False
        self.assertEqual([item["check"] for item in self.result()["checks"] if not item["ok"]], [
            "private_bucket_access_controls",
        ])

    def test_other_project_service_account_fails_verification(self):
        self.args.service_account = ["trainer@other-project.iam.gserviceaccount.com"]
        with self.assertRaisesRegex(CHECK.VerificationError, "outside"):
            self.result()

    def test_gcloud_account_and_project_are_always_explicit_and_output_is_suppressed(self):
        reader = CHECK.GcloudReader(PROJECT, "operator@example.com")
        failed = subprocess.CompletedProcess([], 1, b"", b"DO_NOT_EXPORT_TOKEN")
        with patch.object(CHECK.subprocess, "run", return_value=failed) as run:
            with self.assertRaises(CHECK.VerificationError) as context:
                reader("project IAM", "projects", "get-iam-policy", PROJECT, "--format=json(bindings)")
        self.assertNotIn("DO_NOT_EXPORT_TOKEN", str(context.exception))
        command = run.call_args.args[0]
        self.assertIn("--project=" + PROJECT, command)
        self.assertIn("--account=operator@example.com", command)
        self.assertIn("--quiet", command)
        self.assertLessEqual(run.call_args.kwargs["timeout"], 30)

    def test_gcloud_timeout_and_budget_are_failures(self):
        reader = CHECK.GcloudReader(PROJECT, "operator@example.com")
        with patch.object(CHECK.subprocess, "run", side_effect=subprocess.TimeoutExpired("gcloud", 30)):
            with self.assertRaisesRegex(CHECK.VerificationError, "timed out"):
                reader("project IAM", "projects", "get-iam-policy", PROJECT)
        reader.calls = CHECK.MAX_COMMANDS
        with self.assertRaisesRegex(CHECK.VerificationError, "limit"):
            reader("project IAM", "projects", "get-iam-policy", PROJECT)

    def test_malformed_metadata_fails_without_echoing_its_value(self):
        argv = ["--project=" + PROJECT, "--account=operator@example.com",
                "--region=asia-northeast1", "--service=rangle-stg",
                "--runtime-service-account=" + RUNTIME]
        output = io.StringIO()
        with patch.object(CHECK, "check_cloud", side_effect=TypeError("DO_NOT_EXPORT_VALUE")):
            with patch("sys.stdout", output):
                self.assertEqual(CHECK.main(argv), 2)
        self.assertNotIn("DO_NOT_EXPORT_VALUE", output.getvalue())
        self.assertIn("incomplete", output.getvalue())


if __name__ == "__main__":
    unittest.main()
