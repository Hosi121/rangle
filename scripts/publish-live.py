#!/usr/bin/env python3
"""Publish verified live snapshots: immutable media first, one CAS index last."""

import argparse
import copy
from datetime import datetime, timezone
import importlib.util
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request

# Reuse the existing bounded, create-only authenticated storage adapter.
spec = importlib.util.spec_from_file_location("catalog_publisher", Path(__file__).with_name("publish-catalog.py"))
storage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(storage)
PublishError = storage.PublishError
IMMUTABLE = ["sequence", "start_seconds", "end_seconds", "model_sha256", "model_bytes", "captured_at", "training_seconds", "mean_packed_psnr", "mean_packed_ssim"]


def now():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def verify(args, index, root=None):
    with tempfile.TemporaryDirectory(prefix="rangle-live-check-") as temporary:
        source = Path(temporary) / "index.json"
        source.write_bytes(storage.json_bytes(index))
        command = [args.gate, "-index", str(source), "-origin", args.origin]
        if root:
            command += ["-root", str(root)]
        result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
        if result.returncode:
            raise PublishError("live verification gate rejected index or assets: " + result.stderr.decode(errors="replace")[:1000])


class LiveStorage(storage.GCS):
    def object_url(self, bucket, key):
        return ("https://storage.googleapis.com/storage/v1/b/" + urllib.parse.quote(bucket, safe="")
                + "/o/" + urllib.parse.quote(key, safe=""))

    def read_document(self, bucket, key):
        url = self.object_url(bucket, key)
        try:
            metadata = self.request("GET", url)
        except PublishError as error:
            if str(error) == "cloud API GET failed (HTTP 404)":
                return None, "0"
            raise
        generation = metadata["generation"]
        document = self.request("GET", url + "?alt=media&generation=" + generation)
        encoded = storage.json_bytes(document)
        if (metadata.get("name") != key or metadata.get("contentType") != "application/json"
                or metadata.get("contentEncoding") or int(metadata.get("size", -1)) != len(encoded)
                or metadata.get("md5Hash") != storage.md5(encoded)
                or metadata.get("metadata", {}).get("sha256") != storage.sha256(encoded)):
            raise PublishError("existing live pointer is not the publisher's verified canonical JSON")
        return document, generation

    def index(self, bucket):
        document, generation = self.read_document(bucket, "live/index.json")
        return document or {"version": 1, "sessions": []}, generation

    def commit(self, bucket, document, generation, key="live/index.json", cache_control="private,no-store"):
        body = storage.json_bytes(document)
        metadata = {"name": key, "contentType": "application/json", "cacheControl": cache_control,
                    "md5Hash": storage.md5(body), "metadata": {"sha256": storage.sha256(body), "rangle_source": "rangle-live-v1"}}
        boundary = "rangle_live_" + secrets.token_hex(16)
        payload = (f"--{boundary}\r\nContent-Type: application/json\r\n\r\n".encode()
                   + storage.json_bytes(metadata) + f"\r\n--{boundary}\r\nContent-Type: application/json\r\n\r\n".encode()
                   + body + f"\r\n--{boundary}--\r\n".encode())
        url = ("https://storage.googleapis.com/upload/storage/v1/b/" + urllib.parse.quote(bucket, safe="")
               + "/o?uploadType=multipart&ifGenerationMatch=" + generation)
        result = self.request("POST", url, payload, "multipart/related; boundary=" + boundary)
        if result is None:
            raise PublishError("live index changed concurrently; rerun without overwriting another producer")
        if (result.get("name") != key or result.get("md5Hash") != storage.md5(body)
                or result.get("metadata", {}).get("sha256") != storage.sha256(body)
                or result.get("cacheControl") != cache_control or result.get("contentEncoding")
                or result.get("contentType") != "application/json" or int(result.get("size", -1)) != len(body)):
            raise PublishError("live index write metadata differs from verified bytes")
        return result["generation"]


def remote_file(origin, key, expected, content_type, deadline):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), storage.NoRedirect())
    request = urllib.request.Request(origin + "/" + key, headers={"Cache-Control": "no-cache"})
    try:
        with opener.open(request, timeout=deadline.remaining(30)) as response:
            body = response.read(len(expected) + 1)
            if (response.status != 200 or body != expected or response.headers.get("Content-Encoding")
                    or response.headers.get_content_type() != content_type):
                raise PublishError("remote live bytes or headers differ from approved asset")
    except (OSError, ValueError):
        raise PublishError("remote live asset verification failed") from None


def publish_once(args, document, cloud, deadline, remote=remote_file):
    verify(args, document, args.root)
    if len(document["sessions"]) != 1:
        raise PublishError("one live producer must own exactly one session")
    local = document["sessions"][0]
    if local["source"]["url"] != args.approved_source:
        raise PublishError("source differs from the operator-approved dataset")
    previous, generation = cloud.index(args.catalog_bucket)
    verify(args, previous)
    prior = next((s for s in previous["sessions"] if s["id"] == local["id"]), None)
    known = {s["sequence"]: s for s in prior["segments"]} if prior else {}
    public_key = "live-index/" + local["id"] + ".json"
    public, public_generation = cloud.read_document(args.media_bucket, public_key)
    if public is not None:
        verify(args, {"version": 1, "sessions": [public]})
    for recorded in (prior, public):
        if recorded is None:
            continue
        if (recorded["started_at"] != local["started_at"] or recorded["source"] != local["source"] or recorded["title"] != local["title"]):
            raise PublishError("live session identity changed; use a new session id")
        if recorded["segments"] and (not local["segments"] or local["segments"][-1]["sequence"] < recorded["segments"][-1]["sequence"]):
            raise PublishError("live session sequence would move backwards")
        if (local["latest_input_time_seconds"] < recorded["latest_input_time_seconds"]
                or local["dropped_windows"] < recorded["dropped_windows"]
                or recorded["state"] in ("ended", "failed") and local["state"] != recorded["state"]):
            raise PublishError("live session progress or terminal state would move backwards")
        for item in recorded["segments"]:
            other = known.get(item["sequence"])
            if other and (any(item[k] != other[k] for k in IMMUTABLE) or item["scene_url"] != other["scene_url"]):
                raise PublishError("public and private live segment proofs differ")
            known[item["sequence"]] = item
    promoted = copy.deepcopy(local)
    objects = []
    for segment in promoted["segments"]:
        path = urllib.parse.urlsplit(segment["scene_url"])
        if path.scheme or path.netloc or not path.path.startswith("/live-assets/"):
            raise PublishError("producer must reference its local live-assets")
        directory = path.path.removeprefix("/").removesuffix("scene.json")
        old = known.get(segment["sequence"])
        public_url = args.origin + "/" + directory.replace("live-assets/", "live/", 1) + "scene.json"
        if old:
            if any(old[key] != segment[key] for key in IMMUTABLE) or old["scene_url"] != public_url:
                raise PublishError("existing live segment identity changed")
            segment["available_at"] = old["available_at"]
        else:
            for name, limit, content_type in (("model.rgs", 32 << 20, "application/octet-stream"), ("scene.json", 64 << 10, "application/json")):
                body = storage.read_file(Path(args.root) / directory / name, limit)
                if name == "model.rgs" and (len(body) != segment["model_bytes"] or storage.sha256(body) != segment["model_sha256"]):
                    raise PublishError("live model changed after verification")
                key = directory.replace("live-assets/", "live/", 1) + name
                objects.append(cloud.create(args.media_bucket, key, body, content_type, "public,max-age=31536000,immutable,no-transform"))
                remote(args.origin, key, body, content_type, deadline)
            segment["available_at"] = now()
        segment["scene_url"] = public_url
    if prior:
        promoted["updated_at"] = prior["updated_at"]
        if promoted == prior:
            return {"version": 1, "session_id": local["id"], "state": prior["state"],
                    "index_generation": generation, "latest_sequence": prior["segments"][-1]["sequence"] if prior["segments"] else 0,
                    "reused": True}
    promoted["updated_at"] = now()
    combined = {"version": 1, "sessions": [s for s in previous["sessions"] if s["id"] != local["id"]] + [promoted]}
    verify(args, combined)
    encoded = storage.json_bytes(combined)
    digest = storage.sha256(encoded)
    # Archive is immutable. The mutable discovery pointer is the final CAS write.
    cloud.create(args.catalog_bucket, "live/snapshots/" + local["id"] + "/" + digest + ".json", encoded, "application/json", "private,no-store")
    # Established viewers poll this tiny CDN object, rather than Cloud Run.
    cloud.commit(args.media_bucket, promoted, public_generation, key=public_key,
                 cache_control="public,max-age=1,must-revalidate,no-transform")
    saved = cloud.commit(args.catalog_bucket, combined, generation)
    receipt = {"version": 1, "session_id": local["id"], "state": promoted["state"], "index_sha256": digest,
               "index_generation": saved, "previous_generation": generation, "media": objects,
               "latest_sequence": promoted["segments"][-1]["sequence"] if promoted["segments"] else 0,
               "published_at": promoted["updated_at"]}
    receipt_path = Path(args.receipts) / (local["id"] + "-" + saved + ".json")
    storage.atomic_receipt(receipt_path, receipt)
    return receipt


def parser():
    cli = argparse.ArgumentParser(description=__doc__)
    for name in ("gate", "index", "root", "receipts", "origin", "project", "media-bucket", "catalog-bucket", "account", "environment", "approved-source"):
        cli.add_argument("--" + name, required=True)
    cli.add_argument("--execute", action="store_true")
    cli.add_argument("--follow", action="store_true")
    cli.add_argument("--max-seconds", type=int, default=600)
    return cli


def main():
    args = parser().parse_args()
    deadline = storage.Deadline(args.max_seconds)
    if not 1 <= args.max_seconds <= 1800 or args.media_bucket == args.catalog_bucket:
        raise PublishError("invalid finite scope")
    origin = urllib.parse.urlsplit(args.origin)
    if origin.scheme != "https" or not origin.hostname or origin.netloc != origin.hostname or origin.path or origin.query or origin.fragment:
        raise PublishError("media origin must be an explicit HTTPS origin")
    root = Path(args.root).resolve(strict=True)
    receipts = Path(args.receipts).resolve(strict=True)
    if receipts == root or root in receipts.parents:
        raise PublishError("private receipts must be outside the public output root")
    seen = None
    cloud = None
    while True:
        raw = storage.read_file(args.index, 512 << 10)
        digest = storage.sha256(raw)
        if digest != seen:
            document = json.loads(raw)
            verify(args, document, root)
            if len(document["sessions"]) != 1 or document["sessions"][0]["source"]["url"] != args.approved_source:
                raise PublishError("one producer session from the approved dataset is required")
            if not args.execute:
                print(json.dumps({"status": "verified-local-live-plan", "dry_run": True, "index_sha256": digest}))
                return
            if cloud is None:
                cloud = LiveStorage(storage.access_token(args.account, deadline), deadline)
                storage.check_scope(cloud, args)
            receipt = publish_once(args, document, cloud, deadline)
            print(json.dumps({"event": "live_snapshot_published", **{k: v for k, v in receipt.items() if k != "media"}}), flush=True)
            seen = digest
            if not args.follow or document["sessions"][0]["state"] in ("ended", "failed"):
                return
        time.sleep(deadline.remaining(1))


if __name__ == "__main__":
    try:
        main()
    except (PublishError, OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        print("publish-live:", str(error)[:1200], file=sys.stderr)
        sys.exit(1)
