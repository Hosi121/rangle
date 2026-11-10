"""Cloud-stub contract tests; no credentials, network, or cloud resources."""

import argparse
import copy
from email import policy
from email.parser import BytesParser
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import urllib.error
import urllib.parse

SPEC = importlib.util.spec_from_file_location("publisher", Path(__file__).with_name("publish-catalog.py"))
publisher = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(publisher)
TOKEN = "short-lived-test-token-never-record"
PUBLIC = "public,max-age=31536000,immutable,no-transform"


class CloudStub:
    def __init__(self, events):
        self.events = events
        self.objects = {}
        self.bodies = {}
        self.posts = 0
        self.interrupt_at = None
        self.metadata_change = None
        self.project = {"projectId": "comin-up", "projectNumber": "113704194486", "lifecycleState": "ACTIVE"}
        self.buckets = {
            name: {"name": name, "projectNumber": "113704194486", "location": "ASIA-NORTHEAST1",
                   "labels": {"application": "rangle", "environment": "presentation-stg"},
                   "iamConfiguration": {"publicAccessPrevention": "enforced",
                                        "uniformBucketLevelAccess": {"enabled": True}}}
            for name in ("rangle-stg-media", "rangle-stg-catalog")}

    def open(self, request, timeout):
        if request.get_header("Authorization") != "Bearer " + TOKEN or not 0 < timeout <= 90:
            raise AssertionError("incorrect credential/deadline boundary")
        parsed = urllib.parse.urlparse(request.full_url)
        method = request.get_method()
        if method == "GET" and parsed.hostname == "cloudresourcemanager.googleapis.com":
            self.events.append(("cloud", "project"))
            return io.BytesIO(publisher.json_bytes(self.project))
        if parsed.hostname != "storage.googleapis.com":
            raise AssertionError("unexpected cloud endpoint")
        parts = parsed.path.split("/")
        if method == "GET":
            bucket = urllib.parse.unquote(parts[4])
            if len(parts) == 5:
                self.events.append(("cloud", "bucket", bucket))
                return io.BytesIO(publisher.json_bytes(self.buckets[bucket]))
            key = urllib.parse.unquote("/".join(parts[6:]))
            self.events.append(("cloud", "resume", bucket, key))
            saved = copy.deepcopy(self.objects[bucket, key])
            if self.metadata_change:
                self.metadata_change(saved)
            return io.BytesIO(publisher.json_bytes(saved))
        if method != "POST" or parsed.path.split("/")[1] != "upload":
            raise AssertionError("only insert and exact metadata GET are allowed")
        if urllib.parse.parse_qs(parsed.query) != {"uploadType": ["multipart"], "ifGenerationMatch": ["0"]}:
            raise AssertionError("missing create-only precondition")
        bucket = urllib.parse.unquote(parts[5])
        content_type = request.get_header("Content-type")
        message = BytesParser(policy=policy.default).parsebytes(
            b"Content-Type: " + content_type.encode() + b"\r\nMIME-Version: 1.0\r\n\r\n" + request.data)
        metadata_part, data_part = list(message.iter_parts())
        metadata = json.loads(metadata_part.get_payload(decode=True))
        body = data_part.get_payload(decode=True)
        key = metadata["name"]
        self.events.append(("cloud", "insert", bucket, key))
        self.posts += 1
        if self.interrupt_at == self.posts:
            raise urllib.error.URLError("simulated interrupted upload")
        if (bucket, key) in self.objects:
            raise urllib.error.HTTPError(request.full_url, 412, "exists", {}, io.BytesIO(b"private response"))
        if metadata["md5Hash"] != publisher.md5(body):
            raise AssertionError("upload lacks exact server-validated MD5")
        saved = {**metadata, "size": str(len(body)), "generation": str(100 + len(self.objects))}
        self.objects[bucket, key] = saved
        self.bodies[bucket, key] = body
        return io.BytesIO(publisher.json_bytes(saved))


class PublicationFixture:
    def __init__(self, directory):
        base = Path(directory)
        root = base / "public"
        root.mkdir()
        self.events = []
        self.cloud = CloudStub(self.events)
        self.args = argparse.Namespace(
            gate="/private/rangle-publish", catalog=str(base / "catalog.json"),
            approvals=str(base / "approvals.json"), root=str(root),
            origin="https://media.example.com", project="comin-up",
            media_bucket="rangle-stg-media", catalog_bucket="rangle-stg-catalog",
            account="operator@example.com", environment="presentation-stg",
            output_receipt=str(base / "upload-receipt.json"), previous_catalog=None,
            previous_sha256=None, timeout=60, execute=True)
        prefix = "/assets/clip-a/rev-a/full/"
        self.files = {prefix + name: body for name, body in (
            ("model.rgs", b"approved model"), ("metrics.json", b"{}"),
            ("preview.webm", b"approved preview"), ("scene.json", b"approved scene"))}
        self.files["/manifests/clip-a/rev-a.json"] = b"approved manifest"
        for name, body in self.files.items():
            filename = root / name.lstrip("/")
            filename.parent.mkdir(parents=True, exist_ok=True)
            filename.write_bytes(body)
        catalog_body = b'{"version":1,"clips":[]}'
        approvals_body = b'{"version":1,"clips":[],"retained":[]}'
        Path(self.args.catalog).write_bytes(catalog_body)
        Path(self.args.approvals).write_bytes(approvals_body)
        self.receipt = {
            "version": 1, "verifier": "rangle-publish-v1", "scope": "local",
            "origin": self.args.origin, "catalog_sha256": publisher.sha256(catalog_body),
            "approvals_sha256": publisher.sha256(approvals_body),
            "checked": [{"approval": {"files": {
                name: {"bytes": len(body), "sha256": publisher.sha256(body)}
                for name, body in self.files.items()}}}], "retained": [], "removed": []}
        self.local_failure = False
        self.remote_failure = False
        self.remote_change = None

    def gate(self, args, temporary, remote, deadline):
        self.events.append(("gate", "remote" if remote else "local"))
        if (remote and self.remote_failure) or (not remote and self.local_failure):
            raise publisher.PublishError("verification gate rejected snapshot")
        result = copy.deepcopy(self.receipt)
        if remote:
            result["scope"] = "local-and-remote"
            if self.remote_change:
                self.remote_change(result)
        return result, publisher.json_bytes(result)

    def token(self, account, deadline):
        self.events.append(("credential", account))
        return TOKEN

    def run(self):
        return publisher.publish(
            self.args, gate=self.gate, token_provider=self.token,
            cloud_factory=lambda token, deadline: publisher.GCS(token, deadline, self.cloud))


class PublisherTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.f = PublicationFixture(self.temporary.name)

    def test_dry_run_verifies_locally_without_credentials_or_api(self):
        self.f.args.execute = False
        result = self.f.run()
        self.assertTrue(result["dry_run"])
        self.assertEqual(result["status"], "verified-local-plan")
        self.assertEqual(self.f.events, [("gate", "local")])
        self.assertFalse(Path(self.f.args.output_receipt).exists())

    def test_local_gate_failure_precedes_any_credential_or_cloud_call(self):
        self.f.local_failure = True
        with self.assertRaises(publisher.PublishError):
            self.f.run()
        self.assertEqual(self.f.events, [("gate", "local")])
        self.assertEqual(self.f.cloud.posts, 0)

    def test_create_order_remote_barrier_and_catalog_snapshot_last(self):
        result = self.f.run()
        self.assertEqual(self.f.events[0][0], "gate")
        self.assertEqual(self.f.events[1][0], "credential")
        inserts = [event for event in self.f.events if event[:2] == ("cloud", "insert")]
        media = [event[3] for event in inserts if event[2] == self.f.args.media_bucket]
        self.assertEqual([publisher.phase(key) for key in media], [0, 0, 0, 1, 2])
        private = [event[3] for event in inserts if event[2] == self.f.args.catalog_bucket]
        digest = result["catalog_sha256"]
        self.assertEqual(private, [f"approvals/{digest}.json", f"receipts/{digest}.json", f"snapshots/{digest}.json"])
        remote_index = self.f.events.index(("gate", "remote"))
        private_index = next(i for i, event in enumerate(self.f.events)
                             if event[:3] == ("cloud", "insert", self.f.args.catalog_bucket))
        self.assertLess(remote_index, private_index)
        snapshot = self.f.cloud.bodies[self.f.args.catalog_bucket, private[-1]]
        self.assertEqual(snapshot, Path(self.f.args.catalog).read_bytes())
        for key in private:
            self.assertEqual(self.f.cloud.objects[self.f.args.catalog_bucket, key]["cacheControl"], "private,no-store")
        self.assertEqual(json.loads(Path(self.f.args.output_receipt).read_bytes()), result)
        self.assertNotIn(TOKEN, json.dumps(result))

    def test_interruption_at_media_or_private_steps_resumes_without_overwrite(self):
        for interruption in (2, 6, 7, 8):
            with self.subTest(interruption=interruption), tempfile.TemporaryDirectory() as directory:
                f = PublicationFixture(directory)
                f.cloud.interrupt_at = interruption
                with self.assertRaises(publisher.PublishError):
                    f.run()
                self.assertFalse(any(key.startswith("snapshots/") for _, key in f.cloud.objects))
                self.assertFalse(Path(f.args.output_receipt).exists())
                previous = copy.deepcopy(f.cloud.objects)
                f.cloud.interrupt_at = None
                result = f.run()
                for key, metadata in previous.items():
                    self.assertEqual(f.cloud.objects[key], metadata)
                self.assertEqual(len(f.cloud.objects), len(f.files) + 3)
                self.assertEqual(result, f.run())

    def test_legacy_source_hint_is_preserved_and_not_an_approval_proof(self):
        self.f.run()
        for (bucket, key), metadata in self.f.cloud.objects.items():
            if bucket == self.f.args.media_bucket:
                metadata["metadata"]["rangle_source"] = "local-batch-20261109"
        Path(self.f.args.output_receipt).unlink()
        self.f.run()
        for (bucket, key), metadata in self.f.cloud.objects.items():
            if bucket == self.f.args.media_bucket:
                self.assertEqual(metadata["metadata"]["rangle_source"], "local-batch-20261109")

    def test_remote_failure_or_changed_input_never_creates_catalog_objects(self):
        for mode in ("failure", "receipt", "input"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                f = PublicationFixture(directory)
                if mode == "failure":
                    f.remote_failure = True
                elif mode == "receipt":
                    f.remote_change = lambda receipt: receipt.update(catalog_sha256="0" * 64)
                else:
                    f.remote_change = lambda receipt: Path(f.args.approvals).write_bytes(b"changed")
                with self.assertRaises(publisher.PublishError):
                    f.run()
                self.assertTrue(f.cloud.objects)
                self.assertTrue(all(bucket == f.args.media_bucket for bucket, _ in f.cloud.objects))
                self.assertFalse(Path(f.args.output_receipt).exists())

    def test_wrong_managed_scope_fails_before_first_cloud_write(self):
        changes = [
            lambda c: c.project.update(projectId="wrong-project"),
            lambda c: c.project.update(lifecycleState="DELETE_REQUESTED"),
            lambda c: c.buckets["rangle-stg-media"].update(projectNumber="9999"),
            lambda c: c.buckets["rangle-stg-media"].update(location="US"),
            lambda c: c.buckets["rangle-stg-catalog"]["labels"].update(application="other"),
            lambda c: c.buckets["rangle-stg-catalog"]["labels"].update(environment="main"),
            lambda c: c.buckets["rangle-stg-catalog"]["iamConfiguration"].update(publicAccessPrevention="inherited"),
            lambda c: c.buckets["rangle-stg-media"]["iamConfiguration"]["uniformBucketLevelAccess"].update(enabled=False),
        ]
        for change in changes:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as directory:
                f = PublicationFixture(directory)
                change(f.cloud)
                with self.assertRaises(publisher.PublishError):
                    f.run()
                self.assertEqual(f.cloud.posts, 0)

    def test_412_requires_actual_size_sha_md5_and_distribution_metadata(self):
        changes = [
            lambda obj: obj.update(size="999"),
            lambda obj: obj["metadata"].update(sha256="0" * 64),
            lambda obj: obj.update(md5Hash="bad"),
            lambda obj: obj.update(contentType="text/html"),
            lambda obj: obj.update(cacheControl="private,no-store"),
            lambda obj: obj.update(contentEncoding="gzip"),
            lambda obj: obj.update(generation="0"),
            lambda obj: obj.update(name="another-key"),
        ]
        for change in changes:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as directory:
                f = PublicationFixture(directory)
                f.run()
                before = copy.deepcopy(f.cloud.objects)
                f.cloud.metadata_change = change
                with self.assertRaises(publisher.PublishError):
                    f.run()
                self.assertEqual(f.cloud.objects, before)

    def test_post_gate_asset_change_is_rejected(self):
        original = self.f.token

        def token(account, deadline):
            (Path(self.f.args.root) / "assets/clip-a/rev-a/full/model.rgs").write_bytes(b"tampered")
            return original(account, deadline)

        with self.assertRaises(publisher.PublishError):
            publisher.publish(self.f.args, gate=self.f.gate, token_provider=token,
                              cloud_factory=lambda token, deadline: publisher.GCS(token, deadline, self.f.cloud))
        self.assertFalse(any(key.endswith("model.rgs") for _, key in self.f.cloud.objects))
        self.assertFalse(any(key.startswith("snapshots/") for _, key in self.f.cloud.objects))

    def test_unrelated_objects_are_never_modified_or_deleted(self):
        key = (self.f.args.media_bucket, "unrelated/private.npz")
        self.f.cloud.objects[key] = {"opaque": "must remain unchanged"}
        self.f.run()
        self.assertEqual(self.f.cloud.objects[key], {"opaque": "must remain unchanged"})
        self.assertFalse(any(event[-1] == key[1] for event in self.f.events))

    def test_output_receipt_cannot_contaminate_public_root(self):
        self.f.args.output_receipt = str(Path(self.f.args.root) / "private-upload-receipt.json")
        with self.assertRaises(publisher.PublishError):
            self.f.run()
        self.assertEqual(self.f.events, [])

    def test_credentials_and_http_error_bodies_are_not_reflected(self):
        with mock.patch.object(publisher.subprocess, "run", return_value=argparse.Namespace(
                returncode=1, stdout=TOKEN, stderr=TOKEN)):
            with self.assertRaises(publisher.PublishError) as caught:
                publisher.access_token("operator@example.com", publisher.Deadline(1))
        self.assertNotIn(TOKEN, str(caught.exception))

        class Forbidden:
            def open(self, request, timeout):
                raise urllib.error.HTTPError(request.full_url, 403, TOKEN, {}, io.BytesIO(TOKEN.encode()))

        with self.assertRaises(publisher.PublishError) as caught:
            publisher.GCS(TOKEN, publisher.Deadline(1), Forbidden()).bucket("bucket")
        self.assertNotIn(TOKEN, str(caught.exception))
        with self.assertRaises(publisher.PublishError):
            publisher.NoRedirect().redirect_request(None, None, 302, "redirect", {}, "https://untrusted.example")


if __name__ == "__main__":
    unittest.main(verbosity=2)
