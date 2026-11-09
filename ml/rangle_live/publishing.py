"""Existing live publication protocol, using ADC instead of operator credentials."""

import importlib.util
import json
from pathlib import Path

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
        limit = self.deadline.remaining(90 if method == "POST" else 30)
        # AuthorizedSession handles ADC refresh; requests itself has no retries.
        try:
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
                if response.status_code == 412 and method == "POST":
                    return None
                if response.status_code != 200:
                    raise PublishError(
                        f"cloud API {method} failed (HTTP {response.status_code})"
                    )
                encoded = bytearray()
                for block in response.iter_content(64 << 10):
                    encoded.extend(block)
                    if len(encoded) > 1 << 20:
                        raise PublishError("cloud metadata response exceeds limit")
                value = json.loads(encoded)
                if not isinstance(value, dict):
                    raise PublishError("cloud metadata response is not an object")
                return value
        except PublishError:
            raise
        except Exception:  # noqa: BLE001 - never log credential/HTTP exception bodies
            raise PublishError(f"cloud API {method} failed") from None


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
