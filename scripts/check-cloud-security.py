#!/usr/bin/env python3
"""Read only the selected project's IAM and dedicated rangle metadata."""

import argparse
import json
import re
import subprocess
import sys
import time


MAX_OUTPUT_BYTES = 8 * 1024 * 1024
MAX_COMMANDS = 40
BASIC_ROLES = {"roles/owner", "roles/editor", "roles/viewer"}


class VerificationError(Exception):
    pass


class GcloudReader:
    def __init__(self, project, account):
        self.project = project
        self.account = account
        self.deadline = time.monotonic() + 180
        self.calls = 0

    def __call__(self, label, *command):
        self.calls += 1
        remaining = self.deadline - time.monotonic()
        if self.calls > MAX_COMMANDS or remaining <= 0:
            raise VerificationError("Metadata verification exceeded its command or time limit.")
        try:
            result = subprocess.run(
                ["gcloud", *command, "--project=" + self.project,
                 "--account=" + self.account, "--quiet"],
                capture_output=True, timeout=min(30, remaining), check=False,
            )
        except (OSError, subprocess.TimeoutExpired) as error:
            raise VerificationError(label + ": gcloud unavailable or read timed out.") from error
        if result.returncode:
            # Do not echo stderr: credential helpers and SDK plugins can emit
            # credentials or private application configuration in diagnostics.
            raise VerificationError(
                label + ": gcloud metadata read failed (exit " + str(result.returncode) +
                "); check account permissions, API availability and authentication."
            )
        if len(result.stdout) > MAX_OUTPUT_BYTES:
            raise VerificationError(label + ": metadata exceeds the output limit.")
        try:
            return json.loads(result.stdout)
        except (ValueError, UnicodeDecodeError) as error:
            raise VerificationError(label + ": expected JSON metadata.") from error


def check_cloud(args, read):
    project = read("project identity", "projects", "describe", args.project,
                   "--format=json(projectId,projectNumber)")
    if (not isinstance(project, dict) or project.get("projectId") != args.project or
            not re.fullmatch(r"[0-9]+", str(project.get("projectNumber", "")))):
        raise VerificationError("Project metadata does not match the explicitly selected project.")
    default_sa = str(project["projectNumber"]) + "-compute@developer.gserviceaccount.com"
    results = []

    def record(name, ok, **details):
        results.append({"check": name, "ok": ok, **details})

    policy = read("project IAM", "projects", "get-iam-policy", args.project,
                  "--format=json(bindings)")
    if not isinstance(policy, dict) or not isinstance(policy.get("bindings", []), list):
        raise VerificationError("Invalid project IAM metadata.")
    roles = sorted({binding.get("role") for binding in policy.get("bindings", [])
                    if binding.get("role") in BASIC_ROLES and
                    "serviceAccount:" + default_sa in binding.get("members", [])})
    record("default_compute_has_no_basic_project_role", not roles,
           service_account=default_sa, roles=roles)

    service = read(
        "Cloud Run service", "run", "services", "describe", args.service,
        "--platform=managed", "--region=" + args.region,
        "--format=json(metadata.name,metadata.annotations,spec.template.spec.serviceAccountName)",
    )
    if not isinstance(service, dict) or service.get("metadata", {}).get("name") != args.service:
        raise VerificationError("Cloud Run metadata does not match the selected service.")
    runtime_sa = service.get("spec", {}).get("template", {}).get("spec", {}).get("serviceAccountName")
    record("dedicated_runtime_identity", runtime_sa == args.runtime_service_account,
           service=args.service, expected=args.runtime_service_account, actual=runtime_sa)
    ingress = service.get("metadata", {}).get("annotations", {}).get("run.googleapis.com/ingress")
    record("load_balancer_only_ingress", ingress == "internal-and-cloud-load-balancing",
           service=args.service, ingress=ingress)

    service_accounts = sorted({default_sa, args.runtime_service_account, *args.service_account})
    for service_account in service_accounts:
        if service_account != default_sa and not service_account.endswith("@" + args.project + ".iam.gserviceaccount.com"):
            raise VerificationError("Service account is outside the explicitly selected project.")
        keys = read(
            "service account key metadata", "iam", "service-accounts", "keys", "list",
            "--iam-account=" + service_account, "--managed-by=user",
            "--format=json(name,keyType,disabled,validAfterTime,validBeforeTime)",
        )
        if not isinstance(keys, list):
            raise VerificationError("Invalid service account key metadata.")
        record("no_user_managed_service_account_keys", not keys,
               service_account=service_account, key_count=len(keys))

    for bucket in sorted(set(args.bucket)):
        bucket_metadata = read(
            "bucket metadata", "storage", "buckets", "describe", "gs://" + bucket, "--raw",
            "--format=json(projectNumber,iamConfiguration.publicAccessPrevention,iamConfiguration.uniformBucketLevelAccess.enabled)",
        )
        if (not isinstance(bucket_metadata, dict) or
                str(bucket_metadata.get("projectNumber")) != str(project["projectNumber"])):
            raise VerificationError("Bucket is outside the explicitly selected project or its ownership cannot be verified.")
        bucket_iam = bucket_metadata.get("iamConfiguration", {})
        record("private_bucket_access_controls",
               bucket_iam.get("publicAccessPrevention") == "enforced" and
               bucket_iam.get("uniformBucketLevelAccess", {}).get("enabled") is True,
               bucket=bucket)
        bucket_policy = read("bucket IAM", "storage", "buckets", "get-iam-policy", "gs://" + bucket,
                             "--format=json(bindings)")
        if not isinstance(bucket_policy, dict) or not isinstance(bucket_policy.get("bindings", []), list):
            raise VerificationError("Invalid bucket IAM metadata.")
        viewer_roles = sorted({binding.get("role") for binding in bucket_policy.get("bindings", [])
                               if "projectViewer:" + args.project in binding.get("members", [])})
        public_roles = sorted({binding.get("role") for binding in bucket_policy.get("bindings", [])
                               if {"allUsers", "allAuthenticatedUsers"}.intersection(binding.get("members", []))})
        record("no_project_viewer_bucket_convenience_grant", not viewer_roles,
               bucket=bucket, roles=viewer_roles)
        record("no_public_bucket_iam_grant", not public_roles, bucket=bucket, roles=public_roles)

    return {"project": args.project, "region": args.region, "service": args.service,
            "ok": all(result["ok"] for result in results), "checks": results}


def arguments(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", required=True)
    parser.add_argument("--account", required=True)
    parser.add_argument("--region", required=True)
    parser.add_argument("--service", required=True)
    parser.add_argument("--runtime-service-account", required=True)
    parser.add_argument("--service-account", action="append", default=[],
                        help="Additional dedicated SA to check for user-managed keys (repeatable, max 16).")
    parser.add_argument("--bucket", action="append", default=[],
                        help="Explicit owned private bucket to check IAM metadata (repeatable, max 8).")
    args = parser.parse_args(argv)
    patterns = {
        "project": r"[a-z][a-z0-9-]{4,28}[a-z0-9]",
        "account": r"[A-Za-z0-9_.+-]+@[A-Za-z0-9.-]+",
        "region": r"[a-z]+(?:-[a-z]+)+[0-9]",
        "service": r"[a-z][a-z0-9-]{0,61}[a-z0-9]",
        "runtime_service_account": r"[a-z][a-z0-9-]+@[a-z][a-z0-9-]+\.iam\.gserviceaccount\.com",
    }
    if any(not re.fullmatch(pattern, getattr(args, field)) for field, pattern in patterns.items()):
        parser.error("Use explicit project, user account, region, service and dedicated runtime SA identifiers.")
    if len(args.service_account) > 16 or len(args.bucket) > 8:
        parser.error("At most 16 additional service accounts and 8 explicit buckets are supported.")
    if any(not re.fullmatch(patterns["runtime_service_account"], value) for value in args.service_account):
        parser.error("Additional service accounts must be dedicated project service account emails.")
    if any(not re.fullmatch(r"[a-z0-9][a-z0-9._-]{1,220}[a-z0-9]", value) for value in args.bucket):
        parser.error("Bucket arguments must be bucket names without gs:// or object paths.")
    return args


def main(argv=None):
    args = arguments(argv)
    try:
        report = check_cloud(args, GcloudReader(args.project, args.account))
    except VerificationError as error:
        print(json.dumps({"project": args.project, "ok": False, "error": str(error)}))
        return 2
    except (AttributeError, KeyError, TypeError):
        print(json.dumps({"project": args.project, "ok": False,
                          "error": "Unexpected metadata shape; verification is incomplete."}))
        return 2
    print(json.dumps(report, indent=2))
    return 0 if report["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
