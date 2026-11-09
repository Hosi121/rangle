#!/usr/bin/env python3
"""Finite create-only GCS adapter around the rangle-publish verification gate."""

import argparse
import base64
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


class PublishError(Exception):
    """A safe operator-facing failure; never includes HTTP bodies or credentials."""


class Deadline:
    def __init__(self, seconds):
        self.end = time.monotonic() + seconds

    def remaining(self, maximum):
        value = min(maximum, self.end - time.monotonic())
        if value <= 0:
            raise PublishError("publication deadline exceeded")
        return value


def sha256(body):
    return hashlib.sha256(body).hexdigest()


def md5(body):
    # Corruption/resume checksum only; SHA256 is the content identity.
    return base64.b64encode(hashlib.md5(body, usedforsecurity=False).digest()).decode()


def read_file(filename, limit):
    path = Path(filename)
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
        raise PublishError("input must be a bounded regular file")
    with path.open("rb") as stream:
        body = stream.read(limit + 1)
    if len(body) > limit:
        raise PublishError("input exceeds size limit")
    return body


def json_bytes(value):
    return (json.dumps(value, ensure_ascii=False, sort_keys=True,
                       separators=(",", ":"), allow_nan=False) + "\n").encode()


def atomic_receipt(filename, value):
    target = Path(filename)
    body = json_bytes(value)
    if target.exists():
        if read_file(target, 16 << 20) == body:
            return
        raise PublishError("output receipt already contains a different result")
    descriptor, temporary = tempfile.mkstemp(prefix=".rangle-upload-", dir=target.parent)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(body)
            stream.flush()
            os.fsync(stream.fileno())
        try:
            os.link(temporary, target)
        except FileExistsError:
            if read_file(target, 16 << 20) != body:
                raise PublishError("output receipt was created with a different result") from None
    finally:
        os.unlink(temporary)


def access_token(account, deadline):
    try:
        result = subprocess.run(
            ["gcloud", "auth", "print-access-token", "--account=" + account],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
            timeout=deadline.remaining(60), text=True)
    except (OSError, subprocess.TimeoutExpired):
        raise PublishError("short-lived credential command failed") from None
    token = result.stdout.strip()
    if result.returncode or not re.fullmatch(r"[A-Za-z0-9._~+/-]{1,16382}={0,2}", token):
        raise PublishError("short-lived credential command failed")
    return token


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if fp is not None:
            fp.close()
        raise PublishError("cloud API redirects are forbidden")


class GCS:
    """Fixed Google REST endpoints. No SDK, proxy, retries or write/update API."""

    def __init__(self, token, deadline, opener=None):
        self._token = token
        self.deadline = deadline
        self.opener = opener or urllib.request.build_opener(
            urllib.request.ProxyHandler({}), NoRedirect())

    def request(self, method, url, body=None, content_type=None):
        request = urllib.request.Request(url, data=body, method=method, headers={
            "Authorization": "Bearer " + self._token,
            "Accept": "application/json",
        })
        if content_type:
            request.add_header("Content-Type", content_type)
        try:
            with self.opener.open(request, timeout=self.deadline.remaining(
                    90 if method == "POST" else 30)) as response:
                data = response.read((1 << 20) + 1)
                if len(data) > 1 << 20:
                    raise PublishError("cloud metadata response exceeds limit")
                value = json.loads(data)
                if not isinstance(value, dict):
                    raise PublishError("cloud metadata response is not an object")
                return value
        except urllib.error.HTTPError as error:
            status = error.code
            error.close()
            if status == 412 and method == "POST":
                return None
            raise PublishError(f"cloud API {method} failed (HTTP {status})") from None
        except (OSError, http.client.HTTPException, ValueError):
            raise PublishError(f"cloud API {method} failed") from None

    def project(self, project):
        return self.request("GET", "https://cloudresourcemanager.googleapis.com/v1/projects/"
                            + urllib.parse.quote(project, safe=""))

    def bucket(self, bucket):
        return self.request("GET", "https://storage.googleapis.com/storage/v1/b/"
                            + urllib.parse.quote(bucket, safe=""))

    def create(self, bucket, key, body, content_type, cache_control):
        expected_sha, expected_md5 = sha256(body), md5(body)
        metadata = {
            "name": key, "contentType": content_type,
            "cacheControl": cache_control, "md5Hash": expected_md5,
            "metadata": {"sha256": expected_sha, "rangle_source": "rangle-publish-v1"},
        }
        boundary = "rangle_" + secrets.token_hex(16)
        payload = (f"--{boundary}\r\nContent-Type: application/json; charset=UTF-8\r\n\r\n".encode()
                   + json_bytes(metadata)
                   + f"\r\n--{boundary}\r\nContent-Type: {content_type}\r\n\r\n".encode()
                   + body + f"\r\n--{boundary}--\r\n".encode())
        url = ("https://storage.googleapis.com/upload/storage/v1/b/"
               + urllib.parse.quote(bucket, safe="")
               + "/o?uploadType=multipart&ifGenerationMatch=0")
        saved = self.request("POST", url, payload, "multipart/related; boundary=" + boundary)
        if saved is None:
            saved = self.request("GET", "https://storage.googleapis.com/storage/v1/b/"
                                 + urllib.parse.quote(bucket, safe="") + "/o/"
                                 + urllib.parse.quote(key, safe=""))
        # rangle_source is only an ownership hint. Existing approved uploads may
        # have another marker; never mutate it or treat it as approval evidence.
        try:
            matched = (
                saved["name"] == key and int(saved["size"]) == len(body)
                and saved["contentType"] == content_type
                and not saved.get("contentEncoding")
                and saved["cacheControl"] == cache_control
                and saved["metadata"]["sha256"] == expected_sha
                and saved["md5Hash"] == expected_md5
                and re.fullmatch(r"[1-9][0-9]*", saved["generation"]) is not None)
        except (KeyError, TypeError, ValueError):
            matched = False
        if not matched:
            raise PublishError("existing/created object differs from the approved bytes or metadata: " + key)
        return {"bucket": bucket, "key": key, "generation": saved["generation"],
                "bytes": len(body), "sha256": expected_sha, "md5": expected_md5,
                "ownership_hint_present": "rangle_source" in saved.get("metadata", {})}


def run_gate(args, temporary, remote, deadline):
    receipt = Path(temporary) / ("remote.json" if remote else "local.json")
    command = [str(Path(args.gate).resolve()), "-catalog", args.catalog,
               "-approvals", args.approvals, "-root", args.root,
               "-origin", args.origin, "-receipt", str(receipt),
               "-timeout", f"{deadline.remaining(900):.3f}s"]
    if args.previous_catalog:
        command += ["-previous-catalog", args.previous_catalog,
                    "-previous-sha256", args.previous_sha256]
    if remote:
        command += ["-remote"]
    try:
        result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                timeout=deadline.remaining(900), check=False)
    except (OSError, subprocess.TimeoutExpired):
        raise PublishError("verification gate failed to complete") from None
    if result.returncode:
        # The gate has no credentials. Its bounded diagnostics identify a failed
        # asset without reflecting authenticated cloud response bodies.
        detail = result.stderr.decode(errors="replace")[:4096].strip()
        raise PublishError("verification gate rejected snapshot: " + detail)
    body = read_file(receipt, 16 << 20)
    return json.loads(body), body


def check_scope(cloud, args):
    project = cloud.project(args.project)
    number = str(project.get("projectNumber", ""))
    if (project.get("projectId") != args.project or not number.isdigit()
            or project.get("lifecycleState") != "ACTIVE"):
        raise PublishError("project identity is not the requested active project")
    for name in (args.media_bucket, args.catalog_bucket):
        bucket = cloud.bucket(name)
        iam = bucket.get("iamConfiguration", {})
        labels = bucket.get("labels", {})
        if not isinstance(iam, dict) or not isinstance(labels, dict) or not isinstance(iam.get("uniformBucketLevelAccess"), dict):
            raise PublishError("bucket lacks managed access metadata: " + name)
        if (bucket.get("name") != name or str(bucket.get("projectNumber")) != number
                or bucket.get("location") != "ASIA-NORTHEAST1"
                or labels.get("application") != "rangle"
                or labels.get("environment") != args.environment
                or iam.get("publicAccessPrevention") != "enforced"
                or iam.get("uniformBucketLevelAccess", {}).get("enabled") is not True):
            raise PublishError("bucket is outside requested managed Tokyo scope: " + name)
    return number


def media_type(key):
    return {".rgs": "application/octet-stream", ".json": "application/json",
            ".webm": "video/webm", ".webp": "image/webp"}[Path(key).suffix]


def phase(key):
    if key.startswith("manifests/"):
        return 2
    if key.endswith("/scene.json"):
        return 1
    return 0


def snapshot_plan(local, catalog_body, approval_body):
    if (local.get("version") != 1 or local.get("verifier") != "rangle-publish-v1"
            or local.get("scope") != "local"
            or local.get("catalog_sha256") != sha256(catalog_body)
            or local.get("approvals_sha256") != sha256(approval_body)):
        raise PublishError("local gate receipt does not match the frozen inputs")
    files = {}
    for checked in local["checked"]:
        for name, proof in checked["approval"]["files"].items():
            # Receipt comes from the strict Go gate; retain a narrow defensive
            # path boundary here before opening any file or creating a key.
            key = name.removeprefix("/")
            if (not name.startswith(("/assets/", "/manifests/"))
                    or ".." in Path(key).parts or "\\" in key or "%" in key
                    or key in files):
                raise PublishError("invalid/duplicate path in gate receipt")
            files[key] = proof
    return sorted(files.items(), key=lambda item: (phase(item[0]), item[0]))


def validated_body(root, key, proof):
    filename = root / key
    if root not in filename.resolve().parents:
        raise PublishError("asset escapes frozen public root")
    body = read_file(filename, 32 << 20)
    if len(body) != proof["bytes"] or sha256(body) != proof["sha256"]:
        raise PublishError("asset changed after local verification: " + key)
    return body


def publish(args, gate=run_gate, token_provider=access_token, cloud_factory=GCS):
    deadline = Deadline(args.timeout)
    root = Path(args.root).resolve(strict=True)
    output = Path(args.output_receipt)
    parent = output.parent.resolve(strict=True)
    if parent == root or root in parent.parents:
        raise PublishError("output receipt must be outside the public root")
    # Gate outputs are private, never stored in the public root or given tokens.
    with tempfile.TemporaryDirectory(prefix="rangle-publisher-", dir=parent) as temporary:
        local, _ = gate(args, temporary, False, deadline)
        catalog_body = read_file(args.catalog, 16 << 20)
        approval_body = read_file(args.approvals, 16 << 20)
        plan = snapshot_plan(local, catalog_body, approval_body)
        if local.get("origin") != args.origin.rstrip("/"):
            raise PublishError("gate receipt origin differs from the requested origin")
        digest = local["catalog_sha256"]
        if not args.execute:
            return {"status": "verified-local-plan", "dry_run": True,
                    "project": args.project, "media_bucket": args.media_bucket,
                    "catalog_bucket": args.catalog_bucket, "catalog_sha256": digest,
                    "media_files": len(plan), "retained": len(local["retained"]),
                    "snapshot_key": "snapshots/" + digest + ".json"}
        # The first credential command/API call is after successful local gate.
        token = token_provider(args.account, deadline)
        cloud = cloud_factory(token, deadline)
        project_number = check_scope(cloud, args)
        objects = []
        for key, proof in plan:
            body = validated_body(root, key, proof)
            objects.append(cloud.create(args.media_bucket, key, body, media_type(key),
                                        "public,max-age=31536000,immutable,no-transform"))
        # A failed remote gate cannot advertise a partial snapshot in catalog.
        remote, remote_body = gate(args, temporary, True, deadline)
        if (remote.get("scope") != "local-and-remote"
                or {k: v for k, v in remote.items() if k != "scope"}
                != {k: v for k, v in local.items() if k != "scope"}
                or read_file(args.catalog, 16 << 20) != catalog_body
                or read_file(args.approvals, 16 << 20) != approval_body):
            raise PublishError("remote gate/frozen inputs differ from the local snapshot")
        for key, body in [("approvals/" + digest + ".json", approval_body),
                          ("receipts/" + digest + ".json", remote_body),
                          ("snapshots/" + digest + ".json", catalog_body)]:
            objects.append(cloud.create(args.catalog_bucket, key, body,
                                        "application/json", "private,no-store"))
        result = {"version": 1, "status": "verified-snapshot-created", "dry_run": False,
                  "project": args.project, "project_number": project_number,
                  "environment": args.environment, "origin": remote["origin"],
                  "catalog_sha256": digest, "approvals_sha256": sha256(approval_body),
                  "remote_receipt_sha256": sha256(remote_body),
                  "snapshot_uri": "gs://" + args.catalog_bucket + "/snapshots/" + digest + ".json",
                  "objects": objects}
        atomic_receipt(output, result)
        return result


def parser():
    cli = argparse.ArgumentParser(description=__doc__)
    for name in ("gate", "catalog", "approvals", "root", "origin", "project",
                 "media-bucket", "catalog-bucket", "account", "environment", "output-receipt"):
        cli.add_argument("--" + name, required=True)
    cli.add_argument("--previous-catalog")
    cli.add_argument("--previous-sha256")
    cli.add_argument("--timeout", type=int, default=1800, help="total seconds, 1..3600")
    cli.add_argument("--execute", action="store_true", help="explicitly allow create-only cloud writes")
    return cli


def main(argv=None):
    cli = parser()
    args = cli.parse_args(argv)
    if (not 1 <= args.timeout <= 3600 or args.media_bucket == args.catalog_bucket
            or not re.fullmatch(r"[a-z][a-z0-9-]{4,61}[a-z0-9]", args.project)
            or not re.fullmatch(r"[a-z0-9-]{1,63}", args.environment)
            or not re.fullmatch(r"[^@\s]+@[^@\s]+\.[^@\s]+", args.account)
            or any(not re.fullmatch(r"[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]", name)
                   for name in (args.media_bucket, args.catalog_bucket))
            or bool(args.previous_catalog) != bool(args.previous_sha256)):
        cli.error("invalid explicit scope, account, deadline, or previous-catalog pair")
    try:
        result = publish(args)
    except KeyboardInterrupt:
        print("publish-catalog: interrupted; rerun the same frozen inputs to resume", file=sys.stderr)
        return 130
    except (PublishError, OSError, ValueError, KeyError, TypeError) as error:
        # OS paths and verifier diagnostics have no token; never stringify a
        # raw authenticated HTTP exception/response or credential subprocess.
        print("publish-catalog: " + str(error), file=sys.stderr)
        return 1
    print(json.dumps(result, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
