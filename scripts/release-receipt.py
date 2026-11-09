#!/usr/bin/env python3
"""Validate a dedicated stg release and export only its allowlisted identity."""

import json
import os
from pathlib import Path
import re
import sys
import tempfile
from urllib.parse import urlsplit

SERVICE = "rangle-stg"
IMAGE_BASE = "asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/server"
REVISION = re.compile(r"rangle-stg-[a-z0-9-]{1,52}")
SHA = r"[0-9a-f]{64}"
CATALOG = re.compile(r"/mnt/catalog/snapshots/(" + SHA + r")\.json")
GENERATION = re.compile(r"/mnt/catalog/generation/(" + SHA + r")\.json")
IMAGE = re.compile(
    r"[a-z0-9]+(?:[.-][a-z0-9]+)*(?::[0-9]{1,5})?"
    r"(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+"
    r"(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}|@sha256:" + SHA + r")?"
)


def read_json(path):
    with Path(path).open("rb") as source:
        data = source.read(8 * 1024 * 1024 + 1)
    if len(data) > 8 * 1024 * 1024:
        raise ValueError("Release metadata exceeds its input limit.")
    document = json.loads(data)
    if not isinstance(document, dict):
        raise ValueError("Release metadata must be an object.")
    return document


def revision_name(value):
    if not isinstance(value, str) or not REVISION.fullmatch(value) or len(value) > 63:
        raise ValueError("Invalid dedicated service revision.")
    return value


def origin(value):
    if not isinstance(value, str) or len(value) > 512:
        return None
    try:
        parsed = urlsplit(value)
    except ValueError:
        return None
    if (parsed.scheme != "https" or not parsed.hostname or parsed.username or
            parsed.password or parsed.path not in ("", "/") or parsed.query or
            parsed.fragment or parsed.netloc != parsed.hostname or
            not re.fullmatch(r"[a-z0-9]+(?:[.-][a-z0-9]+)*", parsed.hostname)):
        return None
    return "https://" + parsed.hostname


def container(document, revision=None):
    if revision is None:
        containers = document.get("spec", {}).get("template", {}).get("spec", {}).get("containers", [])
    else:
        if document.get("metadata", {}).get("name") != revision_name(revision):
            raise ValueError("Revision metadata does not match the requested revision.")
        containers = document.get("spec", {}).get("containers", [])
    if not isinstance(containers, list) or len(containers) != 1 or not isinstance(containers[0], dict):
        raise ValueError("Expected one configured container.")
    return containers[0]


def arguments(item):
    args = item.get("args", [])
    if not isinstance(args, list) or any(not isinstance(arg, str) for arg in args):
        raise ValueError("Invalid container arguments.")
    return args


def argument(args, flag):
    indices = [i for i, value in enumerate(args) if value == flag]
    if len(indices) != 1 or indices[0] + 1 >= len(args):
        return None
    return args[indices[0] + 1]


def identity(item, expected_origin=None):
    args = arguments(item)
    catalog = CATALOG.fullmatch(argument(args, "-catalog") or "")
    generation = GENERATION.fullmatch(argument(args, "-generation-config") or "")
    configured_origin = argument(args, "-asset-origin")
    media_origin = origin(configured_origin)
    image = item.get("image")
    if not isinstance(image, str) or len(image) > 512 or not IMAGE.fullmatch(image):
        image = None
    if expected_origin is not None and (
        not catalog or not argument(args, "-ui-root") or "-asset-root" in args or
        configured_origin != expected_origin or media_origin != expected_origin or
        ("-generation-config" in args and generation is None)
    ):
        raise ValueError("Invalid immutable target catalog, generation config or media origin.")
    return {
        "image": image,
        "image_digest": image if image and re.search(r"@sha256:" + SHA + r"$", image) else None,
        "catalog_sha256": catalog.group(1) if catalog else None,
        "generation_config_sha256": generation.group(1) if generation else None,
        "asset_origin": media_origin,
    }


def traffic(document):
    status = document.get("status", {})
    allocation = {}
    targets = status.get("traffic", [])
    if not isinstance(targets, list) or len(targets) > 1000:
        raise ValueError("Invalid traffic allocation.")
    for target in targets:
        percent = target.get("percent")
        if percent is None:
            percent = 0
        if type(percent) is not int or not 0 <= percent <= 100:
            raise ValueError("Invalid traffic percentage.")
        if percent:
            name = target.get("revisionName")
            if name is None and target.get("latestRevision") is True:
                name = status.get("latestReadyRevisionName")
            name = revision_name(name)
            allocation[name] = allocation.get(name, 0) + percent
    if sum(allocation.values()) != 100:
        raise ValueError("Expected an explicit healthy 100% traffic allocation.")
    return dict(sorted(allocation.items()))


def plan(before, expected_origin):
    if origin(expected_origin) != expected_origin:
        raise ValueError("Invalid approved media origin.")
    ingress = before.get("metadata", {}).get("annotations", {}).get("run.googleapis.com/ingress")
    if ingress != "internal-and-cloud-load-balancing":
        raise ValueError("Apply the LB-only ingress and candidate route before deploying.")
    service_url = before.get("status", {}).get("url", "")
    parsed = urlsplit(service_url)
    if origin(service_url) != service_url or not parsed.hostname.endswith(".run.app"):
        raise ValueError("Expected a pre-existing HTTPS run.app service.")
    identity(container(before), expected_origin)
    return service_url, traffic(before), identity(container(before), expected_origin)["catalog_sha256"]


def previous_revisions(before, directory):
    result = []
    for name in traffic(before):
        document = read_json(Path(directory) / (name + ".json"))
        result.append({"revision": name, **identity(container(document, name))})
    return result


def create_receipt(before, after, candidate, previous, revision, media_origin, env):
    plan(before, media_origin)
    expected_image = env.get("STG_IMAGE", "")
    if not re.fullmatch(re.escape(IMAGE_BASE) + r"@sha256:" + SHA, expected_image):
        raise ValueError("Expected the immutable dedicated image digest.")
    source = env.get("GITHUB_SHA", "")
    run_id, attempt = env.get("GITHUB_RUN_ID", ""), env.get("GITHUB_RUN_ATTEMPT", "")
    if not re.fullmatch(r"[0-9a-f]{40}", source) or not all(re.fullmatch(r"[1-9][0-9]*", value) for value in (run_id, attempt)):
        raise ValueError("Expected a complete source commit and run identity.")
    actual = container(candidate, revision)
    selected = identity(actual, media_origin)
    if selected["image_digest"] != expected_image or arguments(actual) != arguments(container(before)):
        raise ValueError("Candidate revision does not match the requested image and target arguments.")
    confirmed = traffic(after)
    if confirmed != {revision_name(revision): 100}:
        raise ValueError("Public traffic is not 100% on the verified candidate.")
    if sorted(item["revision"] for item in previous) != list(traffic(before)):
        raise ValueError("Missing an actually served previous revision.")
    return {
        "schema_version": 1,
        "project_id": "comin-up", "region": "asia-northeast1", "service": SERVICE,
        "source_commit": source, "run_id": run_id, "run_attempt": attempt,
        "candidate": {"revision": revision, **{key: value for key, value in selected.items() if key != "image"}},
        "confirmed_traffic": confirmed,
        "previous_traffic": traffic(before),
        "previous_revisions": [
            {key: item.get(key) for key in (
                "revision", "image", "image_digest", "catalog_sha256",
                "generation_config_sha256", "asset_origin",
            )} for item in previous
        ],
    }


def save_receipt(receipt, output, summary=None):
    data = json.dumps(receipt, indent=2, sort_keys=True) + "\n"
    if len(data.encode()) > 64 * 1024:
        raise ValueError("Release receipt exceeds its output limit.")
    output = Path(output)
    descriptor, temporary = tempfile.mkstemp(prefix="rangle-release-", suffix=".tmp", dir=output.parent)
    linked = False
    try:
        with os.fdopen(descriptor, "w") as target:
            target.write(data)
            target.flush()
            os.fsync(target.fileno())
        os.link(temporary, output)  # Atomic publication; never replace an existing file.
        linked = True
        if summary:
            with Path(summary).open("a") as target:
                target.write("\nVerified stg release receipt\n\n```json\n" + data + "```\n")
    except BaseException:
        if linked:
            output.unlink()
        raise
    finally:
        Path(temporary).unlink(missing_ok=True)


def main(argv):
    if len(argv) in (3, 4) and argv[0] == "plan":
        before = read_json(argv[1])
        service_url, allocation, catalog_sha = plan(before, argv[2])
        if len(argv) == 4:
            previous_revisions(before, argv[3])
        print(service_url)
        print(",".join(f"{name}={percent}" for name, percent in allocation.items()))
        print(catalog_sha)
    elif len(argv) == 8 and argv[0] == "write":
        _, before_path, previous_dir, after_path, candidate_path, revision, media_origin, output = argv
        before = read_json(before_path)
        receipt = create_receipt(before, read_json(after_path), read_json(candidate_path), previous_revisions(before, previous_dir), revision, media_origin, os.environ)
        save_receipt(receipt, output, os.environ.get("GITHUB_STEP_SUMMARY"))
    else:
        raise ValueError("Usage: release-receipt.py plan BEFORE MEDIA [PREVIOUS_DIR] | write BEFORE PREVIOUS_DIR AFTER CANDIDATE REVISION MEDIA OUTPUT")


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except (ValueError, OSError, KeyError, TypeError) as error:
        raise SystemExit(str(error)) from None
