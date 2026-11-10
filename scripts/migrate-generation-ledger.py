#!/usr/bin/env python3
"""Copy only the frozen rangle generation ledger; dry-run unless --apply is set.

Run --prepare-backup first, upload that private snapshot separately, and pass its
printed SHA256 to --apply. Keep source and destination writers paused throughout.
No IAM, database creation, recursive reads, or automatic retry is performed.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


PROJECT = "comin-up"
ACCOUNT = "takuyakataiwa@gmail.com"
SOURCE = "rangle-stg"
TARGET = "rangle-stg-generation"
FORMAT = "rangle-generation-ledger-v1"
COLLECTIONS = {"generation_jobs": 10, "generation_control": 2}
CONTROLS = {"admission", "public_feed"}
TERMINAL = {"succeeded", "failed", "cancelled"}
MAX_BYTES = 16 * 1024 * 1024
MAX_COMMIT_BYTES = 10 * 1024 * 1024


class MigrationError(Exception):
    """A fixed status string safe to print without private records."""


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"),
                      ensure_ascii=False, allow_nan=False).encode("utf-8")


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


def parse_json(data):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise MigrationError("duplicate_json_key")
            result[key] = value
        return result

    def invalid_constant(_):
        raise MigrationError("invalid_json_number")

    try:
        return json.loads(data, object_pairs_hook=unique, parse_constant=invalid_constant)
    except (ValueError, UnicodeError, RecursionError) as error:
        raise MigrationError("invalid_json") from error


def document_root(database):
    if database not in {SOURCE, TARGET}:
        raise MigrationError("database_outside_allowed_scope")
    return "projects/" + PROJECT + "/databases/" + database + "/documents/"


def relative_document(document, database):
    if not isinstance(document, dict):
        raise MigrationError("invalid_document_metadata")
    name = document.get("name", "")
    prefix = document_root(database)
    if not isinstance(name, str) or not name.startswith(prefix):
        raise MigrationError("document_outside_allowed_scope")
    relative = name[len(prefix):]
    pieces = relative.split("/")
    if len(pieces) != 2 or pieces[0] not in COLLECTIONS:
        raise MigrationError("document_outside_allowed_scope")
    collection, identifier = pieces
    if (collection == "generation_jobs" and not re.fullmatch(r"[0-9a-f]{32}", identifier) or
            collection == "generation_control" and identifier not in CONTROLS):
        raise MigrationError("document_identifier_outside_allowed_scope")
    if (set(document) - {"name", "fields", "createTime", "updateTime"} or
            not isinstance(document.get("fields", {}), dict) or
            any(not isinstance(document.get(field), str) or not document[field]
                for field in ("createTime", "updateTime"))):
        raise MigrationError("invalid_document_metadata")
    return relative


def snapshot(documents, database):
    if not isinstance(documents, list):
        raise MigrationError("invalid_document_list")
    result = {}
    counts = dict.fromkeys(COLLECTIONS, 0)
    for document in documents:
        relative = relative_document(document, database)
        if relative in result:
            raise MigrationError("duplicate_document")
        result[relative] = document
        counts[relative.split("/")[0]] += 1
    if any(counts[name] > maximum for name, maximum in COLLECTIONS.items()):
        raise MigrationError("document_limit_exceeded")
    return dict(sorted(result.items()))


def validate_source(source):
    jobs = [document for relative, document in source.items() if relative.startswith("generation_jobs/")]
    for document in jobs:
        state = document.get("fields", {}).get("state", {})
        if not isinstance(state, dict) or set(state) != {"stringValue"} or state["stringValue"] not in TERMINAL:
            raise MigrationError("source_has_nonterminal_job")
    admission = source.get("generation_control/admission")
    if jobs and admission is None:
        raise MigrationError("source_jobs_missing_admission_ledger")
    if admission is not None:
        fields = admission.get("fields", {})
        if fields.get("active") != {"stringValue": ""}:
            raise MigrationError("source_admission_not_idle")
        used = fields.get("used", {})
        if (not isinstance(used, dict) or set(used) != {"integerValue"} or
                not isinstance(used["integerValue"], str) or
                not re.fullmatch(r"(?:[0-9]|10)", used["integerValue"])):
            raise MigrationError("invalid_source_attempt_counter")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, newurl):
        return None


class FirestoreHTTP:
    def __init__(self):
        try:
            result = subprocess.run(
                ["gcloud", "auth", "print-access-token", "--account=" + ACCOUNT,
                 "--project=" + PROJECT, "--quiet"],
                capture_output=True, timeout=30, check=False,
            )
        except (OSError, subprocess.TimeoutExpired) as error:
            raise MigrationError("credential_read_failed") from error
        if result.returncode or len(result.stdout) > 16384:
            raise MigrationError("credential_read_failed")
        self.token = result.stdout.strip().decode("ascii")
        if not self.token or any(character.isspace() for character in self.token):
            raise MigrationError("invalid_credential_response")
        self.opener = urllib.request.build_opener(NoRedirect())
        self.calls = 0
        self.deadline = time.monotonic() + 180

    def __call__(self, method, path, body=None):
        self.calls += 1
        remaining = self.deadline - time.monotonic()
        if self.calls > 20 or remaining <= 0:
            raise MigrationError("rest_request_budget_exceeded")
        allowed_read = any(path.startswith("/v1/" + document_root(database) + collection + "?")
                           for database in (SOURCE, TARGET) for collection in COLLECTIONS)
        allowed_commit = path == "/v1/projects/" + PROJECT + "/databases/" + TARGET + "/documents:commit"
        if not (method == "GET" and allowed_read and body is None or
                method == "POST" and allowed_commit and isinstance(body, dict)):
            raise MigrationError("rest_request_outside_allowed_scope")
        if method == "POST":
            writes = body.get("writes")
            if set(body) != {"writes"} or not isinstance(writes, list) or not 1 <= len(writes) <= 12:
                raise MigrationError("unsafe_commit_body")
            for write in writes:
                if (not isinstance(write, dict) or set(write) != {"update", "currentDocument"} or
                        write["currentDocument"] != {"exists": False} or
                        not isinstance(write["update"], dict) or set(write["update"]) != {"name", "fields"}):
                    raise MigrationError("unsafe_commit_body")
                relative_document({**write["update"], "createTime": "validation", "updateTime": "validation"}, TARGET)
        data = canonical(body) if body is not None else None
        if data is not None and len(data) > MAX_COMMIT_BYTES:
            raise MigrationError("commit_size_limit_exceeded")
        request = urllib.request.Request(
            "https://firestore.googleapis.com" + path, data=data, method=method,
            headers={"Authorization": "Bearer " + self.token, "Content-Type": "application/json",
                     "X-Goog-User-Project": PROJECT},
        )
        try:
            with self.opener.open(request, timeout=min(20, remaining)) as response:
                raw = response.read(MAX_BYTES + 1)
        except urllib.error.HTTPError as error:
            # Never echo bodies or request/exception representations: these may
            # contain private records or the Authorization header.
            raise MigrationError("firestore_http_" + str(error.code)) from error
        except (OSError, urllib.error.URLError) as error:
            raise MigrationError("firestore_transport_failed") from error
        if len(raw) > MAX_BYTES:
            raise MigrationError("rest_response_size_limit_exceeded")
        return parse_json(raw)


def read_snapshot(rest, database):
    documents = []
    for collection, maximum in COLLECTIONS.items():
        query = urllib.parse.urlencode({"pageSize": maximum + 1})
        response = rest("GET", "/v1/" + document_root(database) + collection + "?" + query)
        if (not isinstance(response, dict) or response.get("nextPageToken") or
                not isinstance(response.get("documents", []), list)):
            raise MigrationError("collection_pagination_or_metadata_limit")
        listed = response.get("documents", [])
        if len(listed) > maximum:
            raise MigrationError("document_limit_exceeded")
        for document in listed:
            if not relative_document(document, database).startswith(collection + "/"):
                raise MigrationError("collection_response_scope_mismatch")
        documents.extend(listed)
    return snapshot(documents, database)


def content(source):
    return {relative: document.get("fields", {}) for relative, document in source.items()}


def plan_writes(source, target):
    expected = content(source)
    actual = content(target)
    if any(relative not in expected or canonical(fields) != canonical(expected[relative])
           for relative, fields in actual.items()):
        raise MigrationError("destination_conflict")
    writes = [
        {"update": {"name": document_root(TARGET) + relative, "fields": fields},
         "currentDocument": {"exists": False}}
        for relative, fields in expected.items() if relative not in actual
    ]
    if len(writes) > 12 or len(canonical({"writes": writes})) > MAX_COMMIT_BYTES:
        raise MigrationError("commit_size_limit_exceeded")
    return writes


def open_private_parent(path):
    path = Path(path)
    if not path.is_absolute() or path.name in {"", ".", ".."}:
        raise MigrationError("backup_requires_absolute_private_path")
    descriptor = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    metadata = os.fstat(descriptor)
    if metadata.st_uid != os.getuid() or stat.S_IMODE(metadata.st_mode) & 0o077:
        os.close(descriptor)
        raise MigrationError("backup_parent_must_be_private_and_owned")
    return descriptor, path.name


def create_backup(parent, name, source):
    backup = {"format": FORMAT, "project": PROJECT, "source_database": SOURCE,
              "target_database": TARGET, "source_sha256": digest(source),
              "documents": list(source.values())}
    raw = canonical(backup) + b"\n"
    if len(raw) > MAX_BYTES:
        raise MigrationError("backup_size_limit_exceeded")
    descriptor = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=parent)
    try:
        with os.fdopen(descriptor, "wb") as output:
            os.fchmod(output.fileno(), 0o600)
            output.write(raw)
            output.flush()
            os.fsync(output.fileno())
        os.fsync(parent)
    except BaseException:
        os.unlink(name, dir_fd=parent)
        raise
    return hashlib.sha256(raw).hexdigest()


def load_backup(parent, name, expected_sha256):
    descriptor = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=parent)
    with os.fdopen(descriptor, "rb") as source:
        metadata = os.fstat(source.fileno())
        if (not stat.S_ISREG(metadata.st_mode) or metadata.st_uid != os.getuid() or
                stat.S_IMODE(metadata.st_mode) != 0o600):
            raise MigrationError("backup_must_be_owned_regular_file_mode_0600")
        raw = source.read(MAX_BYTES + 1)
    if len(raw) > MAX_BYTES or hashlib.sha256(raw).hexdigest() != expected_sha256:
        raise MigrationError("backup_digest_mismatch")
    backup = parse_json(raw)
    if (not isinstance(backup, dict) or backup.get("format") != FORMAT or
            backup.get("project") != PROJECT or backup.get("source_database") != SOURCE or
            backup.get("target_database") != TARGET):
        raise MigrationError("backup_identity_mismatch")
    source = snapshot(backup.get("documents"), SOURCE)
    validate_source(source)
    if backup.get("source_sha256") != digest(source):
        raise MigrationError("backup_source_digest_mismatch")
    return source


def migrate(args, rest):
    parent, name = open_private_parent(args.backup)
    try:
        prepared = load_backup(parent, name, args.backup_sha256) if args.apply else None
        source = read_snapshot(rest, SOURCE)
        validate_source(source)
        if prepared is not None and digest(source) != digest(prepared):
            raise MigrationError("source_does_not_match_backup")
        report = {"status": "dry_run", "jobs": sum(name.startswith("generation_jobs/") for name in source),
                  "controls": sum(name.startswith("generation_control/") for name in source),
                  "source_sha256": digest(source), "content_sha256": digest(content(source))}
        if args.prepare_backup:
            if digest(read_snapshot(rest, SOURCE)) != digest(source):
                raise MigrationError("source_changed_before_backup")
            report.update(status="backup_prepared", backup_sha256=create_backup(parent, name, source))
            return report
        target = read_snapshot(rest, TARGET)
        writes = plan_writes(source, target)
        report.update(create_count=len(writes), matching_count=len(target))
        if digest(read_snapshot(rest, SOURCE)) != digest(source):
            raise MigrationError("source_changed_before_commit")
        if not args.apply:
            return report
        if writes:
            rest("POST", "/v1/projects/" + PROJECT + "/databases/" + TARGET + "/documents:commit",
                 {"writes": writes})
        if digest(read_snapshot(rest, SOURCE)) != digest(source):
            raise MigrationError("source_changed_after_commit_do_not_switch_runtime")
        if canonical(content(read_snapshot(rest, TARGET))) != canonical(content(source)):
            raise MigrationError("destination_verification_failed_do_not_switch_runtime")
        report.update(status="migration_verified", backup_sha256=args.backup_sha256)
        return report
    finally:
        os.close(parent)


def arguments(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", choices=[PROJECT], default=PROJECT)
    parser.add_argument("--account", choices=[ACCOUNT], default=ACCOUNT)
    parser.add_argument("--source", choices=[SOURCE], default=SOURCE)
    parser.add_argument("--target", choices=[TARGET], default=TARGET)
    parser.add_argument("--backup", required=True, help="Absolute path in an existing private directory.")
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--dry-run", action="store_true", help="Read metadata/data within the two allowed collections only; create no files or documents (default).")
    modes.add_argument("--prepare-backup", action="store_true", help="Exclusively create a source snapshot with mode0600; no target access or writes.")
    modes.add_argument("--apply", action="store_true", help="Copy missing documents using the matching prepared backup.")
    parser.add_argument("--backup-sha256", help="SHA256 printed by --prepare-backup; required by --apply.")
    args = parser.parse_args(argv)
    if args.apply and not re.fullmatch(r"[0-9a-f]{64}", args.backup_sha256 or ""):
        parser.error("--apply requires --backup-sha256 from the prepared private backup.")
    if not args.apply and args.backup_sha256 is not None:
        parser.error("--backup-sha256 is accepted only with --apply.")
    return args


def main(argv=None):
    args = arguments(argv)
    try:
        report = migrate(args, FirestoreHTTP())
    except MigrationError as error:
        print(json.dumps({"status": str(error)}))
        return 1
    except (OSError, UnicodeError, ValueError, TypeError, AttributeError, RecursionError):
        print(json.dumps({"status": "local_io_or_metadata_validation_failed"}))
        return 1
    print(json.dumps(report, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
