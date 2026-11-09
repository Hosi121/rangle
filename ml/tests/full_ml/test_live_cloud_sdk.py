"""Use the actual pinned Storage SDK with a local in-memory HTTP transport."""

import base64
import hashlib
import io
import json
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from types import SimpleNamespace

import requests
from google.auth.credentials import AnonymousCredentials
from google.cloud import storage
from rangle_live.cloud import Deadline, download


class StorageSDKTests(unittest.TestCase):
    def test_storage_sdk_chunked_download_accepts_bounded_hashing_sink(self):
        payload = b"actual SDK writes; no network or credential access"
        md5 = base64.b64encode(
            hashlib.md5(payload, usedforsecurity=False).digest()
        ).decode()
        calls = []

        class HTTP:
            is_mtls = False

            def request(self, method, url, **options):
                calls.append((method, url, options))
                response = requests.Response()
                headers = options.get("headers", {})
                media = "/download/" in url or "alt=media" in url
                if media:
                    body = payload
                    response.status_code = (
                        206 if "range" in {k.lower() for k in headers} else 200
                    )
                    response.headers.update(
                        {
                            "content-length": str(len(body)),
                            "content-range": f"bytes 0-{len(body) - 1}/{len(body)}",
                            "x-goog-hash": "md5=" + md5,
                        }
                    )
                else:
                    body = json.dumps(
                        {
                            "name": "datasets/live/source.zip",
                            "size": str(len(payload)),
                            "generation": "12",
                            "md5Hash": md5,
                            "mediaLink": "https://storage.googleapis.com/download/storage/v1/b/input-bucket/o/datasets%2Flive%2Fsource.zip?alt=media&generation=12",
                        }
                    ).encode()
                    response.status_code = 200
                response._content = body
                response.raw = io.BytesIO(body)
                response.headers.setdefault("content-type", "application/json")
                return response

        client = storage.Client(
            project="test-project", credentials=AnonymousCredentials(), _http=HTTP()
        )
        args = SimpleNamespace(
            input_bucket="input-bucket",
            input_key="datasets/live/source.zip",
            input_sha256=hashlib.sha256(payload).hexdigest(),
        )
        deadline = Deadline(
            (datetime.now(timezone.utc) + timedelta(minutes=1)).isoformat()
        )
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "source.zip"
            result = download(client, args, path, deadline)
            self.assertEqual(path.read_bytes(), payload)
            self.assertEqual(result["sha256"], args.input_sha256)
        self.assertGreaterEqual(len(calls), 2)
        self.assertTrue(any("ifGenerationMatch=12" in url for _, url, _ in calls))


if __name__ == "__main__":
    unittest.main()
