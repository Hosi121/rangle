"""Frozen ledger migration tests with an in-memory REST transport only."""

import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import urllib.error
import urllib.parse


SPEC = importlib.util.spec_from_file_location(
    "migrate_generation_ledger", Path(__file__).with_name("migrate-generation-ledger.py")
)
MIGRATE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MIGRATE)
JOB_ID = "a" * 32
STAMP = "2026-11-10T01:02:03.123456Z"


def document(relative, fields, database=MIGRATE.SOURCE):
    return {"name": MIGRATE.document_root(database) + relative,
            "fields": copy.deepcopy(fields), "createTime": STAMP, "updateTime": STAMP}


def source_documents():
    return [
        document("generation_jobs/" + JOB_ID, {
            "id": {"stringValue": JOB_ID}, "state": {"stringValue": "succeeded"},
            "owner": {"stringValue": "PRIVATE_OWNER_DO_NOT_PRINT"},
            "created_at": {"timestampValue": STAMP},
            "published": {"booleanValue": True},
            "preset_snapshot": {"mapValue": {"fields": {
                "integer": {"integerValue": "9223372036854775807"},
                "fraction": {"doubleValue": 1.25}, "special": {"doubleValue": "NaN"},
                "nothing": {"nullValue": None}, "bytes": {"bytesValue": "AAH/"},
                "where": {"geoPointValue": {"latitude": 35.0, "longitude": 139.0}},
                "reference": {"referenceValue": MIGRATE.document_root(MIGRATE.SOURCE) + "generation_jobs/" + JOB_ID},
                "list": {"arrayValue": {"values": [{"integerValue": "1"}, {"stringValue": "日本語"}]}},
            }}},
        }),
        document("generation_control/admission", {"used": {"integerValue": "7"}, "active": {"stringValue": ""}}),
        document("generation_control/public_feed", {"entries": {"arrayValue": {"values": [
            {"mapValue": {"fields": {"job_id": {"stringValue": JOB_ID},
                                       "metrics": {"mapValue": {"fields": {"bytes": {"integerValue": "1234"}}}}}}}
        ]}}}),
    ]


class FakeFirestore:
    def __init__(self, documents=None):
        self.source = copy.deepcopy(source_documents() if documents is None else documents)
        self.target = []
        self.calls = []
        self.source_round = 0
        self.drift_before = False
        self.drift_after = False
        self.race = False
        self.corrupt_target = False
        self.page_token = False

    def __call__(self, method, path, body=None):
        self.calls.append((method, path, copy.deepcopy(body)))
        parsed = urllib.parse.urlsplit(path)
        if method == "GET":
            pieces = parsed.path.split("/")
            if pieces[:4] != ["", "v1", "projects", MIGRATE.PROJECT] or len(pieces) != 8:
                raise AssertionError("Read outside allowed scope")
            _, _, _, _, _, database, _, collection = pieces
            if database not in {MIGRATE.SOURCE, MIGRATE.TARGET} or collection not in MIGRATE.COLLECTIONS:
                raise AssertionError("Read outside allowed scope")
            query = urllib.parse.parse_qs(parsed.query)
            if query != {"pageSize": [str(MIGRATE.COLLECTIONS[collection] + 1)]}:
                raise AssertionError("Unbounded collection read")
            if database == MIGRATE.SOURCE and collection == "generation_jobs":
                self.source_round += 1
                if self.drift_before and self.source_round == 2:
                    self.source[0]["updateTime"] = "2026-11-10T01:03:00Z"
            documents = self.source if database == MIGRATE.SOURCE else self.target
            prefix = MIGRATE.document_root(database) + collection + "/"
            result = {"documents": copy.deepcopy([doc for doc in documents if doc["name"].startswith(prefix)])}
            if self.page_token:
                result["nextPageToken"] = "NOT_READ_OR_FOLLOWED"
            return result
        if method != "POST" or path != "/v1/projects/comin-up/databases/rangle-stg-generation/documents:commit":
            raise AssertionError("Write outside allowed scope")
        if self.race:
            self.target.append(document("generation_jobs/" + JOB_ID, {"state": {"stringValue": "failed"}}, MIGRATE.TARGET))
        existing = {doc["name"] for doc in self.target}
        for write in body["writes"]:
            if write["currentDocument"] != {"exists": False}:
                raise AssertionError("Unsafe write precondition")
            if write["update"]["name"] in existing:
                raise MIGRATE.MigrationError("firestore_http_409")
            MIGRATE.relative_document({**write["update"], "createTime": STAMP, "updateTime": STAMP}, MIGRATE.TARGET)
        # Apply only after all preconditions pass: mirror the atomic commit API.
        for write in body["writes"]:
            self.target.append({**copy.deepcopy(write["update"]), "createTime": STAMP, "updateTime": STAMP})
        if self.drift_after:
            self.source[0]["updateTime"] = "2026-11-10T01:04:00Z"
        if self.corrupt_target:
            self.target[0]["fields"]["state"] = {"stringValue": "failed"}
        return {"writeResults": [{} for _ in body["writes"]], "commitTime": STAMP}

    def commits(self):
        return [body for method, _, body in self.calls if method == "POST"]


class MigrationTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        os.chmod(self.directory.name, 0o700)
        self.backup = Path(self.directory.name) / "snapshot.json"
        self.args = SimpleNamespace(backup=str(self.backup), backup_sha256=None,
                                    apply=False, prepare_backup=False)
        self.rest = FakeFirestore()

    def prepare(self):
        self.args.prepare_backup = True
        report = MIGRATE.migrate(self.args, self.rest)
        self.args.prepare_backup = False
        self.args.backup_sha256 = report["backup_sha256"]
        self.rest.calls.clear()
        self.rest.source_round = 0
        return report

    def apply(self):
        self.args.apply = True
        return MIGRATE.migrate(self.args, self.rest)

    def test_default_dry_run_has_no_file_or_cloud_write(self):
        report = MIGRATE.migrate(self.args, self.rest)
        self.assertEqual(report["status"], "dry_run")
        self.assertEqual(report["create_count"], 3)
        self.assertFalse(self.backup.exists())
        self.assertEqual(self.rest.target, [])
        self.assertEqual(self.rest.commits(), [])
        self.assertNotIn("PRIVATE_OWNER", json.dumps(report))

    def test_backup_is_exclusive_private_and_does_not_access_target(self):
        report = self.prepare()
        self.assertEqual(stat.S_IMODE(self.backup.stat().st_mode), 0o600)
        raw = self.backup.read_bytes()
        self.assertEqual(hashlib.sha256(raw).hexdigest(), report["backup_sha256"])
        self.assertEqual(json.loads(raw)["documents"], sorted(self.rest.source, key=lambda doc: doc["name"]))
        self.args.prepare_backup = True
        with self.assertRaises(FileExistsError):
            MIGRATE.migrate(self.args, self.rest)
        self.assertEqual(self.backup.read_bytes(), raw)
        self.assertTrue(all("/databases/rangle-stg/" in path for _, path, _ in self.rest.calls))

    def test_native_types_counter_and_public_aggregate_are_copied_exactly(self):
        self.prepare()
        report = self.apply()
        self.assertEqual(report["status"], "migration_verified")
        self.assertEqual(report["jobs"], 1)
        self.assertEqual(report["controls"], 2)
        source = MIGRATE.content(MIGRATE.snapshot(self.rest.source, MIGRATE.SOURCE))
        target = MIGRATE.content(MIGRATE.snapshot(self.rest.target, MIGRATE.TARGET))
        self.assertEqual(MIGRATE.canonical(source), MIGRATE.canonical(target))
        self.assertEqual(target["generation_control/admission"]["used"], {"integerValue": "7"})
        self.assertEqual(len(self.rest.commits()), 1)
        self.assertTrue(all(write["currentDocument"] == {"exists": False} for write in self.rest.commits()[0]["writes"]))
        self.assertNotIn("PRIVATE_OWNER", json.dumps(report))

    def test_repeated_apply_skips_matching_documents_without_writes(self):
        self.prepare()
        self.apply()
        backup = self.backup.read_bytes()
        self.rest.calls.clear()
        report = self.apply()
        self.assertEqual(report["create_count"], 0)
        self.assertEqual(report["matching_count"], 3)
        self.assertEqual(self.rest.commits(), [])
        self.assertEqual(self.backup.read_bytes(), backup)

    def test_conflicting_or_extra_destination_document_fails_closed(self):
        self.prepare()
        for relative in ("generation_jobs/" + JOB_ID, "generation_jobs/" + "b" * 32):
            with self.subTest(relative=relative):
                self.rest.target = [document(relative, {"state": {"stringValue": "failed"}}, MIGRATE.TARGET)]
                before = copy.deepcopy(self.rest.target)
                with self.assertRaisesRegex(MIGRATE.MigrationError, "destination_conflict"):
                    self.apply()
                self.assertEqual(self.rest.target, before)
                self.assertEqual(self.rest.commits(), [])

    def test_creation_race_cannot_overwrite_or_partially_copy(self):
        self.prepare()
        self.rest.race = True
        with self.assertRaisesRegex(MIGRATE.MigrationError, "409"):
            self.apply()
        self.assertEqual(len(self.rest.target), 1)
        self.assertEqual(self.rest.target[0]["fields"]["state"], {"stringValue": "failed"})

    def test_nonterminal_jobs_are_rejected_before_any_backup_or_target_access(self):
        for state in ("submitting", "queued", "running", "cancelling", "unknown"):
            with self.subTest(state=state):
                self.rest = FakeFirestore()
                self.rest.source[0]["fields"]["state"] = {"stringValue": state}
                with self.assertRaisesRegex(MIGRATE.MigrationError, "nonterminal"):
                    MIGRATE.migrate(self.args, self.rest)
                self.assertFalse(self.backup.exists())
                self.assertTrue(all("/databases/rangle-stg/" in path for _, path, _ in self.rest.calls))

    def test_active_slot_or_invalid_native_counter_is_rejected(self):
        admission = self.rest.source[1]["fields"]
        admission["active"] = {"stringValue": JOB_ID}
        with self.assertRaisesRegex(MIGRATE.MigrationError, "not_idle"):
            MIGRATE.migrate(self.args, self.rest)
        admission["active"] = {"stringValue": ""}
        for used in ({"integerValue": "-1"}, {"integerValue": "11"}, {"integerValue": 7},
                     {"doubleValue": 7}, {"stringValue": "7"}, {}):
            with self.subTest(used=used):
                admission["used"] = used
                with self.assertRaisesRegex(MIGRATE.MigrationError, "attempt_counter"):
                    MIGRATE.migrate(self.args, self.rest)
        self.assertFalse(self.backup.exists())
        self.assertEqual(self.rest.commits(), [])

    def test_source_metadata_drift_before_commit_prevents_all_writes(self):
        self.prepare()
        self.rest.drift_before = True
        with self.assertRaisesRegex(MIGRATE.MigrationError, "source_changed_before_commit"):
            self.apply()
        self.assertEqual(self.rest.commits(), [])
        self.assertEqual(self.rest.target, [])

    def test_source_drift_after_commit_fails_without_deleting_copied_records(self):
        self.prepare()
        self.rest.drift_after = True
        with self.assertRaisesRegex(MIGRATE.MigrationError, "source_changed_after_commit_do_not_switch"):
            self.apply()
        self.assertEqual(len(self.rest.target), 3)
        self.assertEqual(len(self.rest.commits()), 1)

    def test_target_verification_detects_semantic_difference(self):
        self.prepare()
        self.rest.corrupt_target = True
        with self.assertRaisesRegex(MIGRATE.MigrationError, "destination_verification_failed"):
            self.apply()

    def test_backup_digest_and_live_source_match_are_required(self):
        self.prepare()
        self.args.backup_sha256 = "0" * 64
        with self.assertRaisesRegex(MIGRATE.MigrationError, "backup_digest_mismatch"):
            self.apply()
        self.assertEqual(self.rest.calls, [])
        self.args.backup_sha256 = hashlib.sha256(self.backup.read_bytes()).hexdigest()
        self.rest.source[1]["fields"]["used"] = {"integerValue": "8"}
        with self.assertRaisesRegex(MIGRATE.MigrationError, "source_does_not_match_backup"):
            self.apply()
        self.assertEqual(self.rest.commits(), [])

    def test_empty_source_is_valid_and_does_not_fabricate_a_ledger(self):
        self.rest = FakeFirestore([])
        self.prepare()
        report = self.apply()
        self.assertEqual((report["jobs"], report["controls"], report["create_count"]), (0, 0, 0))
        self.assertEqual(self.rest.commits(), [])
        self.assertEqual(self.rest.target, [])

    def test_full_ten_attempt_ledger_fits_one_twelve_document_commit(self):
        terminal = ["succeeded", "failed", "cancelled"]
        jobs = [document("generation_jobs/" + format(index, "032x"),
                         {"state": {"stringValue": terminal[index % 3]}}) for index in range(10)]
        controls = source_documents()[1:]
        controls[0]["fields"]["used"] = {"integerValue": "10"}
        self.rest = FakeFirestore(jobs + controls)
        self.prepare()
        self.assertEqual(self.apply()["create_count"], 12)
        self.assertEqual(len(self.rest.commits()), 1)

    def test_jobs_without_admission_cannot_reset_the_budget(self):
        self.rest.source = [self.rest.source[0]]
        with self.assertRaisesRegex(MIGRATE.MigrationError, "missing_admission"):
            MIGRATE.migrate(self.args, self.rest)
        self.assertFalse(self.backup.exists())
        self.assertEqual(self.rest.commits(), [])

    def test_job_limit_pagination_and_unapproved_control_are_rejected(self):
        self.rest = FakeFirestore([document("generation_jobs/" + format(index, "032x"),
                                           {"state": {"stringValue": "failed"}}) for index in range(11)])
        with self.assertRaisesRegex(MIGRATE.MigrationError, "limit"):
            MIGRATE.migrate(self.args, self.rest)
        self.rest = FakeFirestore()
        self.rest.page_token = True
        with self.assertRaisesRegex(MIGRATE.MigrationError, "pagination"):
            MIGRATE.migrate(self.args, self.rest)
        self.rest = FakeFirestore([document("generation_control/accounts", {})])
        with self.assertRaisesRegex(MIGRATE.MigrationError, "identifier_outside"):
            MIGRATE.migrate(self.args, self.rest)

    def test_scope_rejects_invalid_ids_and_recursive_paths(self):
        for relative in ("generation_jobs/" + "A" * 32, "generation_jobs/short", "accounts/abc",
                         "generation_jobs/" + JOB_ID + "/private/child"):
            with self.subTest(relative=relative):
                with self.assertRaises(MIGRATE.MigrationError):
                    MIGRATE.snapshot([document(relative, {})], MIGRATE.SOURCE)

    def test_private_directory_and_backup_file_permissions_are_required(self):
        os.chmod(self.directory.name, 0o755)
        with self.assertRaisesRegex(MIGRATE.MigrationError, "parent_must_be_private"):
            MIGRATE.migrate(self.args, self.rest)
        self.assertEqual(self.rest.calls, [])
        os.chmod(self.directory.name, 0o700)
        self.prepare()
        os.chmod(self.backup, 0o644)
        with self.assertRaisesRegex(MIGRATE.MigrationError, "mode_0600"):
            self.apply()

    def test_credentials_are_captured_and_never_added_to_argv(self):
        response = subprocess.CompletedProcess([], 0, b"PRIVATE_TEST_TOKEN\n", b"")
        with patch.object(MIGRATE.subprocess, "run", return_value=response) as run:
            client = MIGRATE.FirestoreHTTP()
        self.assertEqual(client.token, "PRIVATE_TEST_TOKEN")
        command = run.call_args.args[0]
        self.assertIn("--account=" + MIGRATE.ACCOUNT, command)
        self.assertIn("--project=" + MIGRATE.PROJECT, command)
        self.assertNotIn("PRIVATE_TEST_TOKEN", str(command))
        self.assertTrue(run.call_args.kwargs["capture_output"])

    def test_transport_rejects_unrelated_reads_or_unsafe_writes_before_network(self):
        response = subprocess.CompletedProcess([], 0, b"PRIVATE_TEST_TOKEN\n", b"")
        with patch.object(MIGRATE.subprocess, "run", return_value=response):
            client = MIGRATE.FirestoreHTTP()
        with patch.object(client.opener, "open") as network:
            with self.assertRaises(MIGRATE.MigrationError):
                client("GET", "/v1/" + MIGRATE.document_root(MIGRATE.SOURCE) + "accounts?pageSize=1")
            with self.assertRaises(MIGRATE.MigrationError):
                client("POST", "/v1/projects/comin-up/databases/rangle-stg-generation/documents:commit",
                       {"writes": [{"delete": MIGRATE.document_root(MIGRATE.TARGET) + "generation_control/admission"}]})
            self.assertFalse(network.called)
        self.assertIsNone(MIGRATE.NoRedirect().redirect_request(None, None, 302, "", {}, "https://example.com"))

    def test_transport_errors_do_not_expose_private_response_or_credentials(self):
        response = subprocess.CompletedProcess([], 0, b"PRIVATE_TEST_TOKEN\n", b"")
        with patch.object(MIGRATE.subprocess, "run", return_value=response):
            client = MIGRATE.FirestoreHTTP()
        error = urllib.error.HTTPError("https://firestore.googleapis.com", 403,
                                       "PRIVATE_TEST_TOKEN", {}, io.BytesIO(b"PRIVATE_RECORD"))
        with patch.object(client.opener, "open", side_effect=error):
            with self.assertRaises(MIGRATE.MigrationError) as context:
                client("GET", "/v1/" + MIGRATE.document_root(MIGRATE.SOURCE) + "generation_jobs?pageSize=11")
        self.assertEqual(str(context.exception), "firestore_http_403")

    def test_cli_defaults_are_strict_and_apply_requires_digest(self):
        args = MIGRATE.arguments(["--backup=" + str(self.backup)])
        self.assertEqual((args.project, args.account, args.source, args.target),
                         (MIGRATE.PROJECT, MIGRATE.ACCOUNT, MIGRATE.SOURCE, MIGRATE.TARGET))
        with patch("sys.stderr", io.StringIO()):
            for flags in (["--apply"], ["--project=unrelated"], ["--account=unrelated@example.com"]):
                with self.subTest(flags=flags):
                    with self.assertRaises(SystemExit):
                        MIGRATE.arguments(["--backup=" + str(self.backup), *flags])


if __name__ == "__main__":
    unittest.main()
