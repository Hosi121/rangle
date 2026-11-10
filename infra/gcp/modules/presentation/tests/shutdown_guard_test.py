#!/usr/bin/env python3
"""Offline fault injection into rendered Run shutdown and its continuation.

This executes only the sequential/try/switch/call subset used on these paths.
HTTP and GPU cancellation are stubs; it does not validate the Workflows compiler.
Run with TERRAFORM_BIN pointing to the repository's pinned Terraform CLI.
"""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
from types import SimpleNamespace
import unittest


MODULE = Path(__file__).resolve().parents[1]
SERVICE = "projects/rangle-example/locations/asia-northeast1/services/rangle-stg"
MANAGER = SERVICE + "-generation"
OPERATION = "projects/rangle-example/locations/asia-northeast1/operations/delete-1"
COMPUTE = "https://compute.googleapis.com/compute/v1/"
FIRESTORE = "https://firestore.googleapis.com/v1/"
DATABASES = ["projects/rangle-example/databases/rangle-stg",
             "projects/rangle-example/databases/rangle-stg-generation"]
GLOBAL = "projects/rangle-example/global/"
BACKENDS = {
    GLOBAL + "backendServices/rangle-stg-app": ("setSecurityPolicy", "securityPolicy", "app"),
    GLOBAL + "backendServices/rangle-stg-candidate": ("setSecurityPolicy", "securityPolicy", "app"),
    GLOBAL + "backendBuckets/rangle-stg-media": ("setEdgeSecurityPolicy", "edgeSecurityPolicy", "media"),
    GLOBAL + "backendBuckets/rangle-stg-live-index": ("setEdgeSecurityPolicy", "edgeSecurityPolicy", "media"),
}


def render(backup_databases=None):
    variables = dict(
        deadline="2026-11-14T15:00:00Z", subscription="projects/rangle-example/subscriptions/budget",
        budget_display_name="presentation", budget_currency="JPY", stop_amount=15000,
        project_id="rangle-example", edge_prefix="rangle-stg", service=SERVICE,
        operation_prefix=OPERATION.rsplit("/", 1)[0] + "/", deployer="projects/rangle-example/serviceAccounts/deploy",
        receipt_bucket="private-inputs", scheduler="projects/rangle-example/locations/asia-northeast1/jobs/guard",
        generation_enabled=True, generation_sync_enabled=False, generation_sync_job="unused",
        generation_manager_enabled=True, generation_manager_service=MANAGER,
    )
    generation = f'templatefile({json.dumps(str(MODULE / "generation-shutdown.yaml.tftpl"))}, {{project_id="rangle-example", project_number="123456789012", region="asia-northeast1"}})'
    backup = f'templatefile({json.dumps(str(MODULE / "backup-shutdown.yaml.tftpl"))}, {{database_paths={json.dumps(DATABASES if backup_databases is None else backup_databases)}}})'
    expression = f'jsonencode(yamldecode(templatefile({json.dumps(str(MODULE / "shutdown.yaml.tftpl"))}, merge({json.dumps(variables)}, {{generation_functions={generation}, backup_functions={backup}}}))))'
    with tempfile.TemporaryDirectory() as directory:
        result = subprocess.run(
            [os.environ.get("TERRAFORM_BIN", "terraform"), "console"], input=expression + "\n",
            cwd=directory, text=True, capture_output=True, check=True,
            env={**os.environ, "CHECKPOINT_DISABLE": "1", "TF_IN_AUTOMATION": "1",
                 "TF_DATA_DIR": str(Path(directory) / ".terraform")},
        )
    return json.loads(json.loads(result.stdout))


class Failure(Exception):
    def __init__(self, value):
        self.value = value
        super().__init__(value)


class Jump(Exception):
    pass


class Returned(Exception):
    pass


class Object(dict):
    def __getattr__(self, name):
        try:
            return self[name]
        except KeyError:
            raise Failure({"message": f"missing response field: {name}"}) from None


def objects(value):
    if isinstance(value, dict):
        return Object({key: objects(item) for key, item in value.items()})
    if isinstance(value, list):
        return [objects(item) for item in value]
    return value


class ShutdownPath:
    def __init__(self, workflow, http, cancel):
        self.workflow, self.http, self.cancel = workflow, http, cancel
        self.state = {}
        self.remaining_steps = 5000
        self.builtins = dict(
            null=None, true=True, false=False, len=len,
            get_type=lambda value: "list" if isinstance(value, list) else "map" if isinstance(value, dict) else "other",
            map=SimpleNamespace(get=lambda value, key: value.get(key)),
            list=SimpleNamespace(concat=lambda values, item: values + [item]),
            text=SimpleNamespace(match_regex=lambda value, pattern: re.search(pattern, value) is not None),
            default=lambda value, fallback: fallback if value is None else value,
            json=SimpleNamespace(encode_to_string=json.dumps),
            sys=SimpleNamespace(now=lambda: 0, get_env=lambda name: "offline-execution"),
            time=SimpleNamespace(format=lambda value: "2026-11-10T00:00:00Z"),
        )

    def value(self, value):
        if isinstance(value, str) and value.startswith("${") and value.endswith("}"):
            return eval(value[2:-1], {"__builtins__": {}}, {**self.builtins, **self.state})
        if isinstance(value, dict):
            return {key: self.value(item) for key, item in value.items()}
        if isinstance(value, list):
            return [self.value(item) for item in value]
        return value

    def steps(self, steps):
        labels = [next(iter(step)) for step in steps]
        index = 0
        while index < len(steps):
            self.remaining_steps -= 1
            if self.remaining_steps < 0:
                raise AssertionError("Shutdown test path exceeded its offline step limit")
            step = steps[index][labels[index]]
            try:
                self.step(step)
                if "next" in step:
                    raise Jump(step["next"])
            except Jump as jump:
                if jump.args[0] not in labels:
                    raise
                index = labels.index(jump.args[0])
                continue
            index += 1

    def step(self, step):
        if "try" in step:
            try:
                self.step(step["try"])
            except Failure as error:
                self.state[step["except"]["as"]] = objects(error.value)
                self.steps(step["except"]["steps"])
        elif "steps" in step:
            self.steps(step["steps"])
        elif "assign" in step:
            for assignment in step["assign"]:
                for name, value in assignment.items():
                    self.state[name] = objects(self.value(value))
        elif "for" in step:
            loop = step["for"]
            for item in self.value(loop["in"]):
                self.state[loop["value"]] = objects(item)
                self.steps(loop["steps"])
        elif "call" in step:
            name, arguments = step["call"], self.value(step.get("args", {}))
            if name.startswith("http."):
                result = self.http(name, arguments)
            elif name == "cancel_generation_jobs":
                result = self.cancel()
            elif name == "sys.sleep":
                result = None
            else:
                saved = self.state
                self.state = arguments.copy()
                try:
                    self.steps(self.workflow[name]["steps"])
                except Returned as returned:
                    result = returned.args[0]
                finally:
                    self.state = saved
            if "result" in step:
                self.state[step["result"]] = objects(result)
        elif "switch" in step:
            for branch in step["switch"]:
                if self.value(branch["condition"]):
                    self.steps(branch.get("steps", []))
                    if "next" in branch:
                        raise Jump(branch["next"])
                    break
        elif "raise" in step:
            raise Failure(self.value(step["raise"]))
        elif "return" in step:
            raise Returned(self.value(step["return"]))
        else:
            raise AssertionError(f"Unsupported test-path step: {step}")


class GuardFailureTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = render()

    def exercise(self, initial_error=None, operation=OPERATION, operation_error=None, cancel_error=None,
                 failure=True, start_phase="stop_owned_service", manager_error=None,
                 manager_present=False, manager_operation=OPERATION, compute_errors=None,
                 foreign_policy=False, compute_absent=False, compute_operation="cleanup-1",
                 active_ingress=False, backup_schedules=None, backup_errors=None,
                 backup_stuck=False, backup_pagination=False, backup_verify_error=None):
        events, receipts = [], []
        service_reads, manager_reads = 0, 0
        compute_errors = compute_errors or {}
        backup_errors = backup_errors or {}
        backup_schedules = {database: list((backup_schedules or {}).get(database, [])) for database in DATABASES}
        backup_lists = dict.fromkeys(DATABASES, 0)

        def http(method, arguments):
            nonlocal service_reads, manager_reads
            url = arguments["url"]
            events.append((method, url))
            if method == "http.get" and url.endswith(SERVICE):
                service_reads += 1
                if service_reads == 1:
                    if initial_error:
                        raise Failure({"code": initial_error, "message": "Run inspection failed"})
                    return {"body": {}}
                raise Failure({"code": 404})
            if method == "http.delete" and url.endswith(SERVICE):
                return {"body": {"name": operation}} if operation is not None else {"body": {}}
            if method == "http.get" and url.endswith(OPERATION):
                if operation_error:
                    raise Failure({"code": operation_error})
                return {"body": {"done": True}}
            if method == "http.get" and url.endswith(MANAGER):
                manager_reads += 1
                if manager_reads == 1 and manager_error:
                    raise Failure({"code": manager_error, "message": "Manager inspection failed"})
                if not manager_present or manager_reads > 1:
                    raise Failure({"code": 404})
                return {"body": {}}
            if method == "http.delete" and url.endswith(MANAGER):
                return {"body": {"name": manager_operation}}
            if url.startswith(COMPUTE):
                resource = url.removeprefix(COMPUTE)
                if (method, resource) in compute_errors:
                    raise Failure({"code": compute_errors[method, resource]})
                if "/forwardingRules/" in resource:
                    if active_ingress:
                        return {"body": {}}
                    raise Failure({"code": 404})
                if method == "http.get" and resource in BACKENDS:
                    if compute_absent:
                        raise Failure({"code": 404})
                    _, field, policy = BACKENDS[resource]
                    name = "foreign" if foreign_policy else f"rangle-stg-{policy}-security"
                    return {"body": {field: COMPUTE + GLOBAL + "securityPolicies/" + name}}
                if method == "http.post":
                    backend, action = resource.rsplit("/", 1)
                    self.assertEqual(action, BACKENDS[backend][0])
                    self.assertEqual(arguments["body"], {"securityPolicy": ""})
                    return {"body": {"name": compute_operation}}
                if method == "http.get" and "/operations/" in resource:
                    return {"body": {"status": "DONE"}}
                if "/securityPolicies/" in resource:
                    self.assertIn(resource, [GLOBAL + "securityPolicies/rangle-stg-app-security",
                                             GLOBAL + "securityPolicies/rangle-stg-media-security"])
                    if method == "http.get":
                        if compute_absent:
                            raise Failure({"code": 404})
                        return {"body": {}}
                    if method == "http.delete":
                        return {"body": {"name": compute_operation}}
            if url.startswith(FIRESTORE):
                resource = url.removeprefix(FIRESTORE)
                self.assertEqual(arguments["timeout"], 10)
                database = next((path for path in DATABASES if resource.startswith(path + "/backupSchedules")), None)
                self.assertIsNotNone(database, "Foreign backup database reached HTTP")
                if (method, resource) in backup_errors:
                    if method == "http.delete" and backup_errors[method, resource] == 404:
                        backup_schedules[database] = [schedule for schedule in backup_schedules[database] if schedule["name"] != resource]
                    raise Failure({"code": backup_errors[method, resource]})
                if method == "http.get":
                    self.assertEqual(resource, database + "/backupSchedules")
                    backup_lists[database] += 1
                    if backup_verify_error and backup_lists[database] > 1:
                        raise Failure({"code": backup_verify_error})
                    body = {"backupSchedules": backup_schedules[database]}
                    if backup_pagination:
                        body["nextPageToken"] = "unexpected-page"
                    return {"body": body}
                if method == "http.delete":
                    schedule = next(item for item in backup_schedules[database] if item["name"] == resource)
                    self.assertEqual(schedule["dailyRecurrence"], {})
                    self.assertEqual(schedule["retention"], "604800s")
                    self.assertNotIn("weeklyRecurrence", schedule)
                    if not backup_stuck:
                        backup_schedules[database] = [item for item in backup_schedules[database] if item["name"] != resource]
                    return {"body": {}}
            if method == "http.post" and "storage.googleapis.com/" in url:
                receipts.append((arguments["query"]["name"], json.loads(arguments["body"])))
                return {}
            if method == "http.post" and (":acknowledge" in url or ":pause" in url):
                return {}
            raise AssertionError(f"Unexpected HTTP request: {method} {url}")

        def cancel():
            events.append(("cancel", "owned-generation"))
            if cancel_error:
                raise Failure({"code": cancel_error, "message": "GPU cancellation failed"})
            return {"empty_sweeps": 4}

        path = ShutdownPath(self.workflow, http, cancel)
        main = self.workflow["main"]["steps"]
        path.steps(main[:1])
        path.state.update(stop=True, reason="budget", ack_ids=["unacknowledged-budget-message"])
        # Each case starts at its relevant phase; all later phases execute.
        start = next(i for i, step in enumerate(main) if start_phase in step)
        with self.assertRaises(Failure if failure else Returned):
            path.steps(main[start:])
        self.assertEqual(len(receipts), 1)
        if failure:
            self.assertEqual(receipts[0][0], "guard/failures/offline-execution.json")
            self.assertTrue(receipts[0][1]["errors"])
        else:
            self.assertEqual(receipts[0][0], "guard/presentation-stop.json")
        self.assertLess(next(i for i, event in enumerate(events) if event[0] == "cancel"),
                        next(i for i, event in enumerate(events) if "storage.googleapis.com/" in event[1]))
        if failure:
            self.assertFalse(any(":acknowledge" in url or ":pause" in url for _, url in events))
        return events, receipts[0][1]

    def test_confirmed_absence_preserves_success_and_acknowledgment(self):
        for status in (None, 404):
            with self.subTest(status=status):
                events, receipt = self.exercise(initial_error=status, failure=False)
                self.assertTrue(any(":acknowledge" in url for _, url in events))
                self.assertTrue(any(":pause" in url for _, url in events))
                self.assertEqual(receipt["generation"], {"empty_sweeps": 4})
                self.assertEqual(any(method == "http.delete" for method, _ in events), status is None)

    def test_initial_inspection_errors_skip_delete_and_still_cancel(self):
        for status in (403, 503):
            with self.subTest(status=status):
                events, receipt = self.exercise(initial_error=status)
                self.assertFalse(any(method == "http.delete" for method, _ in events))
                self.assertEqual(receipt["errors"][0]["code"], status)
                self.assertEqual(receipt["generation"], {"empty_sweeps": 4})

    def test_foreign_operation_is_never_polled_and_still_cancels(self):
        events, receipt = self.exercise(operation="projects/foreign/locations/elsewhere/operations/delete-1")
        self.assertFalse(any("operations/" in url for _, url in events))
        self.assertIn("Unexpected Cloud Run operation resource name", receipt["errors"])

    def test_operation_http_error_and_missing_name_still_cancel(self):
        for arguments in ({"operation_error": 503}, {"operation": None}):
            with self.subTest(arguments=arguments):
                self.exercise(**arguments)

    def test_run_and_cancellation_errors_share_failure_receipt(self):
        _, receipt = self.exercise(initial_error=403, cancel_error=503)
        self.assertEqual([error["code"] for error in receipt["errors"]], [403, 503])
        self.assertIsNone(receipt["generation"])

    def test_manager_inspection_failures_still_stop_public_and_gpu(self):
        for status in (403, 503):
            with self.subTest(status=status):
                events, receipt = self.exercise(start_phase="stop_owned_generation_manager", manager_error=status)
                self.assertFalse(any(method == "http.delete" and url.endswith(MANAGER) for method, url in events))
                self.assertTrue(any(method == "http.delete" and url.endswith(SERVICE) for method, url in events))
                self.assertEqual(receipt["errors"][0]["code"], status)

    def test_manager_absence_and_successful_deletion_are_idempotent(self):
        for present in (False, True):
            with self.subTest(present=present):
                events, _ = self.exercise(start_phase="stop_owned_generation_manager", manager_present=present, failure=False)
                manager_get = next(i for i, (_, url) in enumerate(events) if url.endswith(MANAGER))
                public_get = next(i for i, (_, url) in enumerate(events) if url.endswith(SERVICE))
                self.assertLess(manager_get, public_get)
                self.assertEqual(any(method == "http.delete" and url.endswith(MANAGER) for method, url in events), present)

    def test_manager_foreign_operation_is_not_polled_and_public_still_stops(self):
        events, receipt = self.exercise(start_phase="stop_owned_generation_manager", manager_present=True,
                                       manager_operation="projects/foreign/locations/elsewhere/operations/foreign-1")
        self.assertFalse(any("foreign-1" in url for _, url in events))
        self.assertTrue(any(method == "http.delete" and url.endswith(SERVICE) for method, url in events))
        self.assertIn("Unexpected Cloud Run operation resource name", receipt["errors"])

    def test_security_cleanup_detaches_all_owned_backends_then_deletes_policies(self):
        events, _ = self.exercise(start_phase="cleanup_owned_security", failure=False)
        mutations = [(method, url.removeprefix(COMPUTE)) for method, url in events
                     if url.startswith(COMPUTE) and method in ("http.post", "http.delete")]
        self.assertEqual(mutations[:4], [("http.post", path + "/" + action) for path, (action, _, _) in BACKENDS.items()])
        self.assertEqual(mutations[4:], [("http.delete", GLOBAL + "securityPolicies/rangle-stg-app-security"),
                                        ("http.delete", GLOBAL + "securityPolicies/rangle-stg-media-security")])

    def test_security_404_is_idempotent_without_mutations(self):
        events, _ = self.exercise(start_phase="cleanup_owned_security", failure=False, compute_absent=True)
        self.assertFalse(any(method != "http.get" and url.startswith(COMPUTE) for method, url in events))

    def test_cleanup_failures_continue_remaining_targets_public_and_gpu(self):
        cases = [
            {("http.post", GLOBAL + "backendServices/rangle-stg-app/setSecurityPolicy"): 503},
            {("http.delete", GLOBAL + "securityPolicies/rangle-stg-app-security"): 409},
        ]
        for errors in cases:
            with self.subTest(errors=errors):
                events, _ = self.exercise(start_phase="cleanup_owned_security", compute_errors=errors)
                self.assertTrue(any(method == "http.delete" and url.endswith("securityPolicies/rangle-stg-media-security") for method, url in events))
                self.assertTrue(any(method == "http.delete" and url.endswith(SERVICE) for method, url in events))

    def test_unknown_ingress_preserves_policies_and_still_stops_public(self):
        events, _ = self.exercise(start_phase="cleanup_owned_security", compute_errors={
            ("http.get", GLOBAL + "forwardingRules/rangle-stg-http"): 503,
        })
        self.assertFalse(any(url.startswith(COMPUTE) and method != "http.get" for method, url in events))
        self.assertTrue(any(method == "http.delete" and url.endswith(SERVICE) for method, url in events))

    def test_live_ingress_preserves_policies_and_still_stops_public(self):
        events, _ = self.exercise(start_phase="cleanup_owned_security", active_ingress=True)
        self.assertFalse(any(url.startswith(COMPUTE) and method != "http.get" for method, url in events))
        self.assertTrue(any(method == "http.delete" and url.endswith(SERVICE) for method, url in events))

    def test_foreign_attached_policy_is_not_detached(self):
        events, _ = self.exercise(start_phase="cleanup_owned_security", foreign_policy=True)
        self.assertFalse(any(url.startswith(COMPUTE) and method == "http.post" for method, url in events))

    def test_foreign_compute_operation_is_never_polled(self):
        events, _ = self.exercise(start_phase="cleanup_owned_security", compute_operation="projects/foreign/global/operations/evil")
        self.assertFalse(any("operations/evil" in url for _, url in events))
        self.assertTrue(any(method == "http.delete" and url.endswith(SERVICE) for method, url in events))

    def test_backup_cleanup_stops_only_owned_daily_schedules_after_core_stops(self):
        schedules = {database: [dict(name=database + "/backupSchedules/daily-1", dailyRecurrence={}, retention="604800s"),
                                dict(name=database + "/backupSchedules/weekly-1", weeklyRecurrence={"day": "MONDAY"}, retention="604800s")]
                     for database in DATABASES}
        events, _ = self.exercise(backup_schedules=schedules, failure=False)
        deleted = [url for method, url in events if method == "http.delete" and url.startswith(FIRESTORE)]
        self.assertEqual(deleted, [FIRESTORE + database + "/backupSchedules/daily-1" for database in DATABASES])
        cancel = next(i for i, (method, _) in enumerate(events) if method == "cancel")
        first_list = next(i for i, (_, url) in enumerate(events) if url.startswith(FIRESTORE))
        self.assertLess(cancel, first_list)
        self.assertTrue(any(":acknowledge" in url for _, url in events))

    def test_backup_list_and_delete_failures_preserve_retry_and_continue_other_database(self):
        schedules = {database: [dict(name=database + "/backupSchedules/daily-1", dailyRecurrence={}, retention="604800s")]
                     for database in DATABASES}
        for method, resource in [("http.get", DATABASES[0] + "/backupSchedules"),
                                 ("http.delete", DATABASES[0] + "/backupSchedules/daily-1")]:
            with self.subTest(method=method):
                events, _ = self.exercise(backup_schedules=schedules, backup_errors={(method, resource): 503})
                self.assertTrue(any(event == ("http.delete", FIRESTORE + DATABASES[1] + "/backupSchedules/daily-1") for event in events))
                self.assertTrue(any(event == ("http.delete", "https://run.googleapis.com/v2/" + SERVICE) for event in events))

    def test_backup_missing_database_or_schedule_is_idempotent(self):
        schedules = {DATABASES[1]: [dict(name=DATABASES[1] + "/backupSchedules/daily-1", dailyRecurrence={}, retention="604800s")]}
        self.exercise(backup_schedules=schedules, failure=False, backup_errors={
            ("http.get", DATABASES[0] + "/backupSchedules"): 404,
            ("http.delete", DATABASES[1] + "/backupSchedules/daily-1"): 404,
        })

    def test_foreign_schedule_name_is_preserved_and_blocks_acknowledgment(self):
        for name in ["projects/foreign/databases/rangle-stg/backupSchedules/daily-1",
                     DATABASES[1] + "/backupSchedules/daily-1",
                     DATABASES[0] + "/backupSchedules/daily-1/extra"]:
            with self.subTest(name=name):
                schedules = {DATABASES[0]: [dict(name=name, dailyRecurrence={}, retention="604800s")]}
                events, _ = self.exercise(backup_schedules=schedules)
                self.assertFalse(any(method == "http.delete" and url.startswith(FIRESTORE) for method, url in events))

    def test_different_retention_and_weekly_schedules_are_preserved(self):
        schedules = {DATABASES[0]: [dict(name=DATABASES[0] + "/backupSchedules/other-daily", dailyRecurrence={}, retention="1209600s"),
                                    dict(name=DATABASES[0] + "/backupSchedules/weekly-1", weeklyRecurrence={"day": "MONDAY"}, retention="604800s")]}
        events, _ = self.exercise(backup_schedules=schedules, failure=False)
        self.assertFalse(any(method == "http.delete" and url.startswith(FIRESTORE) for method, url in events))

    def test_backup_unconfirmed_deletion_and_listing_bounds_block_acknowledgment(self):
        daily = dict(name=DATABASES[0] + "/backupSchedules/daily-1", dailyRecurrence={}, retention="604800s")
        self.exercise(backup_schedules={DATABASES[0]: [daily]}, backup_stuck=True)
        for schedules, pagination in [({DATABASES[0]: [daily] * 3}, False), ({}, True)]:
            with self.subTest(pagination=pagination):
                events, _ = self.exercise(backup_schedules=schedules, backup_pagination=pagination)
                self.assertFalse(any(method == "http.delete" and url.startswith(FIRESTORE) for method, url in events))

    def test_backup_verification_http_failure_blocks_acknowledgment_after_deletion(self):
        daily = dict(name=DATABASES[0] + "/backupSchedules/daily-1", dailyRecurrence={}, retention="604800s")
        events, _ = self.exercise(backup_schedules={DATABASES[0]: [daily]}, backup_verify_error=503)
        self.assertTrue(any(method == "http.delete" and url.startswith(FIRESTORE) for method, url in events))

    def test_backup_helpers_reject_foreign_database_or_expired_window_without_http(self):
        def unexpected(*args):
            self.fail(f"Invalid target/window caused HTTP: {args}")
        for database, cutoff in [("projects/foreign/databases/other", 60), (DATABASES[0], 0)]:
            path = ShutdownPath(self.workflow, unexpected, unexpected)
            path.state.update(database_path=database, cutoff=cutoff)
            with self.assertRaises(Failure):
                path.steps(self.workflow["list_owned_backup_schedules"]["steps"])
        path = ShutdownPath(self.workflow, unexpected, unexpected)
        path.state.update(database_path=DATABASES[0], cutoff=0,
                          schedule=objects(dict(name=DATABASES[0] + "/backupSchedules/daily-1", dailyRecurrence={}, retention="604800s")))
        with self.assertRaises(Failure):
            path.steps(self.workflow["delete_owned_backup_schedule"]["steps"])

    def test_disabled_foundation_performs_no_backup_api_calls(self):
        def unexpected(*args):
            self.fail(f"Disabled database caused a call: {args}")
        workflow = render(backup_databases=[])
        path = ShutdownPath(workflow, unexpected, unexpected)
        with self.assertRaises(Returned):
            path.steps(workflow["cleanup_owned_backup_schedules"]["steps"])

    def test_helpers_reject_foreign_targets_without_http(self):
        def unexpected(*args):
            self.fail(f"Foreign target caused a call: {args}")
        for name, argument in [("delete_owned_run_service", {"service_path": "projects/foreign/locations/elsewhere/services/other"}),
                               ("delete_compute_resource", {"resource_path": GLOBAL + "securityPolicies/other"}),
                               ("detach_owned_security_policy", {"resource_path": GLOBAL + "backendServices/other"})]:
            with self.subTest(helper=name):
                path = ShutdownPath(self.workflow, unexpected, unexpected)
                path.state.update(argument)
                with self.assertRaises(Failure):
                    path.steps(self.workflow[name]["steps"])


if __name__ == "__main__":
    unittest.main()
