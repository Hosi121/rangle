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
MANAGER = "rangle-stg-generation"
MANAGER_URL = "https://rangle-stg-generation-113704194486.asia-northeast1.run.app"
MANAGER_SA = "rangle-stg-generation@comin-up.iam.gserviceaccount.com"
RUNTIME_SA = "rangle-stg-runtime@comin-up.iam.gserviceaccount.com"
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


def revision_name(value, service=SERVICE):
    if not isinstance(value, str) or not re.fullmatch(re.escape(service) + r"-[a-z0-9-]+", value) or len(value) > 63:
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


def container(document, revision=None, service=SERVICE):
    if revision is None:
        containers = document.get("spec", {}).get("template", {}).get("spec", {}).get("containers", [])
    else:
        if document.get("metadata", {}).get("name") != revision_name(revision, service):
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


def generation_remote(item):
    args = arguments(item)
    if any(arg == '-generation-manager-url' or arg.startswith('-generation-manager-url=') for arg in args):
        if argument(args, '-generation-manager-url') != MANAGER_URL or any(
            arg.split('=', 1)[0] in {'-generation-config', '-generation-sync-audience', '-generation-manager',
                                     '-generation-manager-audience', '-generation-caller-service-account',
                                     '-generation-cleanup-url-map'} for arg in args
        ):
            raise ValueError('Remote generation requires only the canonical manager URL on the public service.')
        if argument(args, '-firestore-project') != 'comin-up' or argument(args, '-firestore-database') != 'rangle-stg':
            raise ValueError('Remote generation requires the isolated public community database.')
        return MANAGER_URL
    return None


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
    remote = generation_remote(item) if expected_origin is not None else None
    return {
        "image": image,
        "image_digest": image if image and re.search(r"@sha256:" + SHA + r"$", image) else None,
        "catalog_sha256": catalog.group(1) if catalog else None,
        "generation_config_sha256": generation.group(1) if generation else None,
        "asset_origin": media_origin,
        "generation_manager_url": remote,
    }


def traffic(document, service=SERVICE):
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
            name = revision_name(name, service)
            allocation[name] = allocation.get(name, 0) + percent
    if sum(allocation.values()) != 100:
        raise ValueError("Expected an explicit healthy 100% traffic allocation.")
    return dict(sorted(allocation.items()))


def manager_identity(document, revision=None, expected_catalog=None):
    item = container(document, revision, MANAGER)
    spec = document.get('spec', {}) if revision else document.get('spec', {}).get('template', {}).get('spec', {})
    args = arguments(item)
    paired = {'-listen', '-catalog', '-generation-config', '-generation-manager-audience',
              '-generation-caller-service-account', '-generation-sync-audience', '-generation-cleanup-url-map'}
    values = {}
    index = 0
    while index < len(args):
        flag = args[index]
        if flag == '-generation-manager' and flag not in values:
            values[flag] = True
            index += 1
        elif flag in paired and flag not in values and index + 1 < len(args):
            values[flag] = args[index + 1]
            index += 2
        else:
            raise ValueError('Manager arguments contain an unknown, duplicate or malformed option.')
    catalog = CATALOG.fullmatch(values.get('-catalog', ''))
    generation = GENERATION.fullmatch(values.get('-generation-config', ''))
    image = item.get('image', '')
    if (spec.get('serviceAccountName') != MANAGER_SA or item.get('env') or
            item.get('command', []) not in ([], ['/app/rangle-server']) or
            not re.fullmatch(re.escape(IMAGE_BASE) + r'@sha256:' + SHA, image) or
            values.get('-generation-manager') is not True or not catalog or not generation or
            values.get('-generation-manager-audience') != MANAGER_URL or
            values.get('-generation-caller-service-account') != RUNTIME_SA or
            values.get('-generation-sync-audience') not in (None, MANAGER_URL) or
            values.get('-generation-cleanup-url-map') not in (None, 'rangle-stg-https') or
            values.get('-listen', '0.0.0.0:8080') != '0.0.0.0:8080' or
            expected_catalog is not None and catalog.group(1) != expected_catalog):
        raise ValueError('Manager identity, immutable image, catalog or bounded configuration does not match the contract.')
    return {'image_digest': image, 'catalog_sha256': catalog.group(1),
            'generation_config_sha256': generation.group(1)}


def manager_plan(public, manager, policy, previous_dir=None):
    public_item = container(public)
    if generation_remote(public_item) != MANAGER_URL:
        raise ValueError('A manager rollout requires a remote public template.')
    annotations = manager.get('metadata', {}).get('annotations', {})
    if (manager.get('metadata', {}).get('name') != MANAGER or
            annotations.get('run.googleapis.com/ingress') != 'all' or
            annotations.get('run.googleapis.com/invoker-iam-disabled') == 'true'):
        raise ValueError('The pre-existing manager must retain its dedicated identity and IAM authentication.')
    url = manager.get('status', {}).get('url', '')
    parsed = urlsplit(url)
    if origin(url) != url or not parsed.hostname.startswith(MANAGER + '-') or not parsed.hostname.endswith('.run.app'):
        raise ValueError('The manager service URL is invalid.')
    allowed = {'serviceAccount:' + account for account in (
        RUNTIME_SA, 'rangle-stg-generation-sync@comin-up.iam.gserviceaccount.com',
        'rangle-stg-deploy@comin-up.iam.gserviceaccount.com')}
    invokers = set()
    for binding in policy.get('bindings', []):
        if set(binding.get('members', [])) & {'allUsers', 'allAuthenticatedUsers'}:
            raise ValueError('Manager IAM must not contain public principals.')
        if binding.get('role') == 'roles/run.invoker':
            members = binding.get('members', [])
            if not isinstance(members, list) or not set(members) <= allowed:
                raise ValueError('Manager invokers contain an unapproved or public principal.')
            invokers.update(members)
    if not {'serviceAccount:' + RUNTIME_SA, 'serviceAccount:rangle-stg-deploy@comin-up.iam.gserviceaccount.com'} <= invokers:
        raise ValueError('Manager requires the explicit runtime and deployment health invokers.')
    catalog = CATALOG.fullmatch(argument(arguments(public_item), '-catalog') or '')
    if not catalog:
        raise ValueError('The public catalog is not immutable.')
    selected = manager_identity(manager, expected_catalog=catalog.group(1))
    allocation = traffic(manager, MANAGER)
    if previous_dir is not None:
        for name in allocation:
            manager_identity(read_json(Path(previous_dir) / (name + '.json')), name)
    return url, allocation, selected


def manager_candidate(candidate, before, env):
    revision = revision_name(candidate.get('status', {}).get('latestCreatedRevisionName'), MANAGER)
    actual = container(candidate)
    selected = manager_identity(candidate)
    if selected['image_digest'] != env.get('STG_IMAGE') or arguments(actual) != arguments(container(before)):
        raise ValueError('Manager candidate does not match the selected image and immutable arguments.')
    return revision


def manager_tag(document, revision):
    revision_name(revision, MANAGER)
    service_url = document.get('status', {}).get('url', '')
    expected = 'https://ci-manager-candidate---' + urlsplit(service_url).netloc
    for target in document.get('status', {}).get('traffic', []):
        if target.get('tag') == 'ci-manager-candidate' and target.get('revisionName') == revision and target.get('url') == expected:
            return expected
    raise ValueError('The dedicated manager candidate tag and URL were not ready.')


def manager_receipt(directory, public, env):
    directory = Path(directory)
    before, after = read_json(directory / 'before.json'), read_json(directory / 'after.json')
    manager_plan(public, before, read_json(directory / 'policy.json'), directory / 'previous')
    candidate = read_json(directory / 'verified-revision.json')
    revision = candidate.get('metadata', {}).get('name')
    selected = manager_identity(candidate, revision)
    if selected['image_digest'] != env.get('STG_IMAGE') or arguments(container(candidate, revision, MANAGER)) != arguments(container(before)):
        raise ValueError('Verified manager revision does not match the selected image and immutable arguments.')
    confirmed = traffic(after, MANAGER)
    if confirmed != {revision_name(revision, MANAGER): 100}:
        raise ValueError('Manager traffic is not 100% on the verified candidate.')
    previous = [{'revision': name, **manager_identity(read_json(directory / 'previous' / (name + '.json')), name)}
                for name in traffic(before, MANAGER)]
    return {'service': MANAGER, 'candidate': {'revision': revision, **selected},
            'previous_traffic': traffic(before, MANAGER), 'previous_revisions': previous,
            'confirmed_traffic': confirmed}


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


def create_receipt(before, after, candidate, previous, revision, media_origin, env, manager_dir=None):
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
    receipt = {
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
    if generation_remote(container(before)):
        if manager_dir is None:
            raise ValueError('Remote releases require verified manager metadata.')
        receipt['generation_manager'] = manager_receipt(manager_dir, before, env)
    elif manager_dir is not None:
        raise ValueError('Unexpected manager metadata for a legacy release.')
    return receipt


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
    if len(argv) == 2 and argv[0] == 'manager-mode':
        print('remote' if generation_remote(container(read_json(argv[1]))) else 'legacy')
    elif len(argv) in (4, 5) and argv[0] == 'manager-plan':
        url, allocation, selected = manager_plan(read_json(argv[1]), read_json(argv[2]), read_json(argv[3]), argv[4] if len(argv) == 5 else None)
        print(url)
        print(','.join(f'{name}={percent}' for name, percent in allocation.items()))
        print(selected['generation_config_sha256'])
    elif len(argv) == 3 and argv[0] == 'manager-candidate':
        print(manager_candidate(read_json(argv[1]), read_json(argv[2]), os.environ))
    elif len(argv) == 3 and argv[0] == 'manager-tag':
        print(manager_tag(read_json(argv[1]), argv[2]))
    elif len(argv) == 3 and argv[0] == 'manager-verify':
        manager_receipt(argv[1], read_json(argv[2]), os.environ)
    elif len(argv) in (3, 4) and argv[0] == "plan":
        before = read_json(argv[1])
        service_url, allocation, catalog_sha = plan(before, argv[2])
        if len(argv) == 4:
            previous_revisions(before, argv[3])
        print(service_url)
        print(",".join(f"{name}={percent}" for name, percent in allocation.items()))
        print(catalog_sha)
    elif len(argv) in (8, 9) and argv[0] == "write":
        _, before_path, previous_dir, after_path, candidate_path, revision, media_origin, output = argv[:8]
        before = read_json(before_path)
        receipt = create_receipt(before, read_json(after_path), read_json(candidate_path), previous_revisions(before, previous_dir), revision, media_origin, os.environ, argv[8] if len(argv) == 9 else None)
        save_receipt(receipt, output, os.environ.get("GITHUB_STEP_SUMMARY"))
    else:
        raise ValueError("Usage: release-receipt.py plan BEFORE MEDIA [PREVIOUS_DIR] | write BEFORE PREVIOUS_DIR AFTER CANDIDATE REVISION MEDIA OUTPUT")


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except (ValueError, OSError, KeyError, TypeError) as error:
        raise SystemExit(str(error)) from None
