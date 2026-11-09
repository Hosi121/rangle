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
OPERATION = "projects/rangle-example/locations/asia-northeast1/operations/delete-1"


def render():
    variables = dict(
        deadline="2026-11-14T15:00:00Z", subscription="projects/rangle-example/subscriptions/budget",
        budget_display_name="presentation", budget_currency="JPY", stop_amount=15000,
        project_id="rangle-example", edge_prefix="rangle-stg", service=SERVICE,
        operation_prefix=OPERATION.rsplit("/", 1)[0] + "/", deployer="projects/rangle-example/serviceAccounts/deploy",
        receipt_bucket="private-inputs", scheduler="projects/rangle-example/locations/asia-northeast1/jobs/guard",
        generation_enabled=True, generation_sync_enabled=False, generation_sync_job="unused",
    )
    generation = f'templatefile({json.dumps(str(MODULE / "generation-shutdown.yaml.tftpl"))}, {{project_id="rangle-example", project_number="123456789012", region="asia-northeast1"}})'
    expression = f'jsonencode(yamldecode(templatefile({json.dumps(str(MODULE / "shutdown.yaml.tftpl"))}, merge({json.dumps(variables)}, {{generation_functions={generation}}}))))'
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
        self.remaining_steps = 500
        self.builtins = dict(
            null=None, true=True, false=False, len=len,
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
        elif "call" in step:
            name, arguments = step["call"], self.value(step.get("args", {}))
            if name.startswith("http."):
                result = self.http(name, arguments)
            elif name == "cancel_generation_jobs":
                result = self.cancel()
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

    def exercise(self, initial_error=None, operation=OPERATION, operation_error=None, cancel_error=None, failure=True):
        events, receipts = [], []
        service_reads = 0

        def http(method, arguments):
            nonlocal service_reads
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
        # Start at the Run phase; IAM/Compute deletion is outside this regression's scope.
        start = next(i for i, step in enumerate(main) if "stop_owned_service" in step or "inspect_before_delete" in step)
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


if __name__ == "__main__":
    unittest.main()
