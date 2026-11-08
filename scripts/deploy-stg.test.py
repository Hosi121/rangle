"""Exercise deployment transitions with stub gcloud/curl/date; never contact GCP."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('deploy-stg.sh')
FAKE = r'''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ['STUB_STATE'])
name = Path(sys.argv[0]).name
args = sys.argv[1:]
if name == 'date':
    deadline = 1794668400
    if '-d' in args:
        print(deadline)
    else:
        count = int((root / 'clock').read_text()) + 1 if (root / 'clock').exists() else 1
        (root / 'clock').write_text(str(count))
        print(deadline + 1 if os.environ.get('EXPIRE_AT') and count >= int(os.environ['EXPIRE_AT']) else deadline - 3600)
    sys.exit(0)
with (root / 'calls').open('a') as f:
    f.write(json.dumps([name] + args) + '\n')
if name == 'curl':
    url = args[-1]
    candidate = 'candidate-' in url
    if os.environ.get('FAIL_CANDIDATE') and candidate or os.environ.get('FAIL_POST') and not candidate:
        sys.exit(22)
    if os.environ.get('FAIL_CANONICAL_TLS') and url.startswith('https://app.replay.example.test'):
        sys.exit(60)
    if '/health' in url:
        body = 'ok\n'
    elif '/api/' in url:
        body = json.dumps({'clips': [{'id': 'vru', 'revision': 'v1', 'title': 'VRU', 'sport': 'basketball',
          'codec': 'synthetic-transport-v1' if os.environ.get('SYNTHETIC_API') else 'splat-v1',
          'manifest_url': 'https://media.replay.example.test/manifests/vru/v1.json',
          'preview_url': 'https://media.replay.example.test/previews/vru.webm'}]})
    elif '/demo/' in url:
        body = '<html><base href="/demo/"><script src="flutter_bootstrap.js"></script></html>'
    else:
        body = '<html>rangle</html>'
    Path(args[args.index('--output') + 1]).write_text(body)
    sys.exit(0)
assert name == 'gcloud'
assert '--project=comin-up' in args and '--region=asia-northeast1' in args
assert args[:2] == ['run', 'services']
status = {'url': 'https://rangle-stg-test.run.app', 'latestReadyRevisionName': 'rangle-stg-old-a',
          'traffic': [{'revisionName': 'rangle-stg-old-a', 'percent': 80}, {'revisionName': 'rangle-stg-old-b', 'percent': 20}]}
if (root / 'deployed').exists():
    status['latestCreatedRevisionName'] = 'rangle-stg-00003-abc'
if (root / 'tagged').exists():
    status['traffic'].append({'tag': 'candidate-123-1', 'revisionName': 'rangle-stg-00003-abc',
                             'url': 'https://candidate-123-1---rangle-stg-test.run.app'})
doc = {'status': status, 'spec': {'template': {'spec': {'containers': [{'args': [
    '-ui-root', '/app/web', '-catalog', '/mnt/catalog/snapshots/hash.json',
    '-asset-origin', 'https://media.replay.example.test'], 'image': os.environ['STG_IMAGE']}]}}}}
if os.environ.get('BAD_RUNTIME'):
    doc['spec']['template']['spec']['containers'][0]['args'] = []
if args[2] == 'update':
    assert '--no-traffic' in args
    assert not any(x.startswith(('--tag=', '--revision-suffix=')) for x in args)
    assert not any(x.startswith(('--args', '--set-env-vars', '--service-account', '--clear-', '--allow-unauthenticated')) for x in args)
    (root / 'deployed').touch()
    status['latestCreatedRevisionName'] = 'rangle-stg-00003-abc'
    if os.environ.get('WRONG_IMAGE'):
        doc['spec']['template']['spec']['containers'][0]['image'] = 'unexpected:latest'
    if os.environ.get('MISSING_REVISION'):
        status.pop('latestCreatedRevisionName')
elif args[2] == 'update-traffic':
    if '--update-tags=candidate-123-1=rangle-stg-00003-abc' in args:
        (root / 'tagged').touch()
        if os.environ.get('FAIL_TAG'):
            sys.exit(1)
        status['traffic'].append({'tag': 'candidate-123-1', 'revisionName': 'rangle-stg-00003-abc',
                                 'url': 'https://candidate-123-1---rangle-stg-test.run.app'})
    if '--to-revisions=rangle-stg-00003-abc=100' in args and os.environ.get('FAIL_PROMOTION'):
        sys.exit(1)
print(json.dumps(doc))
'''


class Deployment(unittest.TestCase):
    def run_case(self, extra=None, command='deploy'):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bindir = root / 'bin'
            bindir.mkdir()
            for name in ('gcloud', 'curl', 'date'):
                tool = bindir / name
                tool.write_text(FAKE)
                tool.chmod(0o755)
            env = dict(os.environ, PATH=str(bindir) + os.pathsep + os.environ['PATH'], STUB_STATE=str(root),
                       GITHUB_REPOSITORY='Hosi121/rangle', GITHUB_REPOSITORY_ID='1448803636',
                       GITHUB_REPOSITORY_OWNER_ID='128687243', GITHUB_REF='refs/heads/stg',
                       GITHUB_WORKFLOW_REF='Hosi121/rangle/.github/workflows/deploy-stg.yml@refs/heads/stg',
                       GITHUB_EVENT_NAME='push', GITHUB_SHA='a' * 40, GITHUB_RUN_ID='123', GITHUB_RUN_ATTEMPT='1',
                       GCP_DEPLOY_SERVICE_ACCOUNT='rangle-stg-deploy@comin-up.iam.gserviceaccount.com',
                       GCP_WORKLOAD_IDENTITY_PROVIDER='projects/113704194486/locations/global/workloadIdentityPools/github/providers/stg',
                       RANGLE_STG_DOMAIN='replay.example.test',
                       STG_IMAGE='asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/server@sha256:' + 'b' * 64)
            env.pop('GITHUB_STEP_SUMMARY', None)
            env.update(extra or {})
            result = subprocess.run(['bash', str(SCRIPT), command], env=env, capture_output=True, text=True, timeout=20)
            calls = [json.loads(line) for line in (root / 'calls').read_text().splitlines()] if (root / 'calls').exists() else []
            return result, calls

    @staticmethod
    def traffic(calls):
        return [arg for call in calls if call[0] == 'gcloud' for arg in call if arg.startswith('--to-revisions=')]

    def test_success_updates_only_image_then_promotes_and_removes_own_tag(self):
        result, calls = self.run_case()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.traffic(calls), ['--to-revisions=rangle-stg-00003-abc=100'])
        self.assertEqual(sum(call[1:4] == ['run', 'services', 'update'] for call in calls), 1)
        self.assertTrue(any('--update-tags=candidate-123-1=rangle-stg-00003-abc' in call for call in calls))
        self.assertTrue(any('--remove-tags=candidate-123-1' in call for call in calls))
        probed_urls = [call[-1] for call in calls if call[0] == 'curl']
        self.assertIn('https://rangle-stg-test.run.app/health', probed_urls)
        self.assertIn('https://app.replay.example.test/health', probed_urls)

    def test_candidate_failure_keeps_previous_traffic(self):
        result, calls = self.run_case({'FAIL_CANDIDATE': '1'})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.traffic(calls), [])
        self.assertTrue(any('--remove-tags=candidate-123-1' in call for call in calls))

    def test_post_promotion_failure_restores_exact_previous_split(self):
        result, calls = self.run_case({'FAIL_POST': '1'})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.traffic(calls), ['--to-revisions=rangle-stg-00003-abc=100',
                                             '--to-revisions=rangle-stg-old-a=80,rangle-stg-old-b=20'])

    def test_partial_promotion_error_also_rolls_back(self):
        result, calls = self.run_case({'FAIL_PROMOTION': '1'})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.traffic(calls)[-1], '--to-revisions=rangle-stg-old-a=80,rangle-stg-old-b=20')

    def test_partial_tag_failure_cleans_only_own_tag_without_promotion(self):
        result, calls = self.run_case({'FAIL_TAG': '1'})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.traffic(calls), [])
        self.assertTrue(any('--remove-tags=candidate-123-1' in call for call in calls))

    def test_unexpected_candidate_cannot_be_tagged_or_promoted(self):
        for change in ({'WRONG_IMAGE': '1'}, {'MISSING_REVISION': '1'}):
            with self.subTest(change=change):
                result, calls = self.run_case(change)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(call[1:4] == ['run', 'services', 'update-traffic'] for call in calls))

    def test_deadline_blocks_before_any_cloud_call(self):
        result, calls = self.run_case({'EXPIRE_AT': '1'})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, [])

    def test_expiry_after_candidate_blocks_promotion_but_cleans_tag(self):
        result, calls = self.run_case({'EXPIRE_AT': '3'})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.traffic(calls), [])
        self.assertTrue(any('--remove-tags=candidate-123-1' in call for call in calls))

    def test_untrusted_events_identity_and_mutable_images_never_call_gcloud(self):
        for change in ({'GITHUB_EVENT_NAME': 'pull_request'}, {'GITHUB_REF': 'refs/heads/main'},
                       {'GITHUB_REPOSITORY_ID': '1'}, {'GCP_DEPLOY_SERVICE_ACCOUNT': ''},
                       {'GCP_WORKLOAD_IDENTITY_PROVIDER': ''}, {'STG_IMAGE': 'server:latest'}):
            with self.subTest(change=change):
                result, calls = self.run_case(change)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(calls, [])

    def test_wrong_runtime_is_not_overwritten(self):
        result, calls = self.run_case({'BAD_RUNTIME': '1'})
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(call[1:4] == ['run', 'services', 'update'] for call in calls))

    def test_synthetic_api_cannot_promote(self):
        result, calls = self.run_case({'SYNTHETIC_API': '1'})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.traffic(calls), [])

    def test_invalid_domain_stops_before_credentials_or_cloud(self):
        invalid = ['', 'https://replay.example.test', 'user@replay.example.test',
                   'replay.example.test:443', 'replay.example.test/path',
                   'replay.example.test?x=1', 'replay.example.test#fragment',
                   'localhost', 'app.localhost', 'localhost.localdomain',
                   '127.0.0.1', '127.1', '[::1]', '::1', '2130706433',
                   '*.example.test', 'replay.example.test\n', 'replay.\texample.test',
                   'replay.example.test.', '-replay.example.test', 'replay_.example.test',
                   'a' * 64 + '.example.test', '.'.join(['a' * 63] * 3 + ['test' * 15])]
        for domain in invalid:
            with self.subTest(domain=domain):
                result, calls = self.run_case({'RANGLE_STG_DOMAIN': domain}, command='guard')
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('RANGLE_STG_DOMAIN', result.stderr)
                self.assertEqual(calls, [])

    def test_canonical_tls_failure_after_promotion_restores_previous_split(self):
        result, calls = self.run_case({'FAIL_CANONICAL_TLS': '1'})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.traffic(calls), ['--to-revisions=rangle-stg-00003-abc=100',
                                             '--to-revisions=rangle-stg-old-a=80,rangle-stg-old-b=20'])
        self.assertTrue(any('--remove-tags=candidate-123-1' in call for call in calls))


if __name__ == '__main__':
    unittest.main()
