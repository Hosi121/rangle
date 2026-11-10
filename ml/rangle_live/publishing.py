"""Existing live publication protocol, using ADC instead of operator credentials."""

import importlib.util
import json
import re
import secrets
import time
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

from .runner import log

# Keep the reviewed validation/identity/CAS algorithm shared with the operator CLI.
_path = Path(__file__).resolve().parents[2] / "scripts" / "publish-live.py"
_spec = importlib.util.spec_from_file_location("rangle_live_publication", _path)
protocol = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(protocol)
PublishError = protocol.PublishError


class ADCStorage(protocol.LiveStorage):
    """Storage JSON requests on an automatically refreshed ADC transport.

    No bucket/project inspection, list, patch, gcloud, or user-token command.
    Initial insert metadata and the existing create/CAS checks stay unchanged.
    """

    def __init__(self, session, deadline):
        self.session, self.deadline = session, deadline

    def request(self, method, url, body=None, content_type=None):
        headers = {"Accept": "application/json"}
        if content_type:
            headers["Content-Type"] = content_type
        # https://docs.cloud.google.com/storage/docs/retry-strategy#idempotency
        conditions = parse_qs(urlsplit(url).query).get("ifGenerationMatch", [])
        retryable = method == "GET" or (
            method == "POST"
            and len(conditions) == 1
            and re.fullmatch(r"0|[1-9][0-9]*", conditions[0]) is not None
        )
        # AuthorizedSession handles ADC refresh; requests itself has no retries.
        try:
            for attempt in range(4):
                limit = self.deadline.remaining(90 if method == "POST" else 30)
                with self.session.request(
                    method,
                    url,
                    data=body,
                    headers=headers,
                    timeout=limit,
                    max_allowed_time=limit,
                    allow_redirects=False,
                    stream=True,
                ) as response:
                    status = response.status_code
                    if status == 412 and method == "POST":
                        return None
                    if status == 200:
                        encoded = bytearray()
                        for block in response.iter_content(64 << 10):
                            encoded.extend(block)
                            if len(encoded) > 1 << 20:
                                raise PublishError(
                                    "cloud metadata response exceeds limit"
                                )
                        value = json.loads(encoded)
                        if not isinstance(value, dict):
                            raise PublishError(
                                "cloud metadata response is not an object"
                            )
                        return value
                    if status != 429 or not retryable or attempt == 3:
                        raise PublishError(f"cloud API {method} failed (HTTP {status})")
                # Same URL, body and generation condition on every attempt. A
                # possibly committed insert returns 412 to the protocol's verifier.
                delay = 1.25 * 2**attempt
                if self.deadline.remaining(delay + 0.01) <= delay:
                    raise TimeoutError("Cloud retry exceeds live deadline")
                log(
                    "cloud_api_retry",
                    operation=method,
                    status=status,
                    attempt=attempt + 1,
                    backoff_seconds=delay,
                )
                time.sleep(delay)
        except (PublishError, TimeoutError):
            raise
        except Exception:  # noqa: BLE001 - never log credential/HTTP exception bodies
            raise PublishError(f"cloud API {method} failed") from None

    def claim_producer(self, bucket, session_id, input_sha256):
        """One durable producer per session, including across container restarts.

        An attempt nonce permits read-verifying our own ambiguous create retry,
        while a new process can never adopt an earlier producer's claim.
        """
        key = f"live/snapshots/{session_id}/producer.json"
        previous, _ = self.read_document(bucket, key)
        if previous is not None:
            raise PublishError(
                "Live session producer already claimed; use a new session id"
            )
        body = protocol.storage.json_bytes(
            {
                "version": 1,
                "session_id": session_id,
                "input_sha256": input_sha256,
                "attempt": secrets.token_hex(16),
                "claimed_at": protocol.now(),
            }
        )
        try:
            return self.create(
                bucket, key, body, "application/json", "private,no-store"
            )
        except PublishError as error:
            if str(error).startswith("existing/created object differs"):
                raise PublishError(
                    "Live session producer already claimed; use a new session id"
                ) from None
            raise


def adc_clients(deadline):
    import google.auth
    from google.auth.transport.requests import AuthorizedSession
    from google.cloud import storage

    try:
        credentials, project = google.auth.default(
            scopes=["https://www.googleapis.com/auth/devstorage.read_write"]
        )
        session = AuthorizedSession(credentials, max_refresh_attempts=1)
        client = storage.Client(project=project, credentials=credentials, _http=session)
        return client, ADCStorage(session, deadline)
    except Exception as error:  # noqa: BLE001 - hide ADC response bodies
        raise PublishError(
            f"ADC initialization failed ({type(error).__name__})"
        ) from None
