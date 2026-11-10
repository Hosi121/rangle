"""Exercise deployment transitions with stub gcloud/curl/date; never contact GCP."""
import json
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('deploy-stg.sh')
RECEIPT_SPEC = importlib.util.spec_from_file_location('release_receipt', SCRIPT.with_name('release-receipt.py'))
RECEIPT = importlib.util.module_from_spec(RECEIPT_SPEC)
RECEIPT_SPEC.loader.exec_module(RECEIPT)
FAKE = r'''#!/usr/bin/env python3
import hashlib, json, os, struct, sys
from pathlib import Path
root = Path(os.environ['STUB_STATE'])
name = Path(sys.argv[0]).name
args = sys.argv[1:]
assert 'GENERATION_MANAGER_ID_TOKEN' not in os.environ, 'manager ID token leaked into a subprocess environment'
if name == 'sleep':
    sys.exit(0)
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
    if url.startswith('https://ci-manager-candidate---rangle-stg-generation-'):
        assert args[args.index('--header') + 1] == '@-'
        assert sys.stdin.read() == 'Authorization: Bearer DO_NOT_EXPORT_MANAGER_ID_TOKEN\n'
        assert url.endswith('/health')
        if os.environ.get('FAIL_MANAGER_HEALTH'):
            sys.exit(22)
        revision = 'rangle-stg-generation-00003-abc'
        if os.environ.get('BAD_MANAGER_REVISION_HEADER'):
            revision = 'rangle-stg-generation-old-a'
        header = 'HTTP/2 200\r\n'
        if not os.environ.get('MISSING_MANAGER_REVISION_HEADER'):
            header += 'X-Rangle-Revision: ' + revision + '\r\n'
        Path(args[args.index('--dump-header') + 1]).write_text(header + '\r\n')
        Path(args[args.index('--output') + 1]).write_text('ok\n')
        print('204' if os.environ.get('BAD_MANAGER_STATUS') else '200', end='')
        sys.exit(0)
    if url.startswith('https://media.replay.example.test/'):
        assert 'X-Rangle-Candidate: ci-candidate' not in args
        assert not any('Authorization:' in arg or 'Cookie:' in arg for arg in args)
        public = (root / 'promoted').exists()
        if os.environ.get('FAIL_MEDIA_PUBLIC' if public else 'FAIL_MEDIA_CANDIDATE'):
            sys.exit(22)
        model = struct.pack('<8sIIIffI', b'RANGL4D1', 1, 56, 3, 25, .08, 0) + bytes(56)
        scene = {'version': 1, 'frameCount': 3, 'frameRate': 25, 'temporal': {'url': 'model.rgs'},
                 'camera': {'position': [0, 0, -1], 'lookAt': [0, 0, 0], 'up': [0, 1, 0], 'fovYDegrees': 60},
                 'attribution': {'label': 'Fixture', 'source': 'https://source.example.test/fixture', 'license': 'Fixture only'}}
        generated = os.environ.get('GENERATED_API')
        prefix = '/generated/' + 'c' * 32 + '/' if generated else '/assets/vru/v1/full/'
        content_type = 'application/json'
        if '/manifests/' in url:
            body = json.dumps({'version': 1, 'clip_id': 'vru', 'revision': 'v1', 'codec': 'rangle-temporal-rgs1',
                'representations': [{'id': 'full', 'init_url': prefix + 'scene.json',
                    'init_size_bytes': len(json.dumps(scene).encode()), 'segments': [{'index': 0,
                    'start_seconds': 0, 'duration_seconds': .08, 'independent': True,
                    'url': prefix + 'model.rgs', 'size_bytes': len(model), 'sha256': hashlib.sha256(model).hexdigest()}]}]}).encode()
            if os.environ.get('BAD_MEDIA_MANIFEST'):
                body = body.replace(b'"revision": "v1"', b'"revision": "wrong"')
        elif url.endswith('/scene.json'):
            body = json.dumps(scene).encode()
        elif url.endswith('/metrics.json'):
            body = json.dumps({'job_id': 'c' * 32, 'mode': 'dynamic', 'model_bytes': len(model),
                               'model_sha256': hashlib.sha256(model).hexdigest()}).encode()
        elif url.endswith('/model.rgs'):
            body, content_type = model, 'application/octet-stream'
            if os.environ.get('CORRUPT_MEDIA_MODEL'):
                body = body[:-1] + b'X'
        else:
            body, content_type = b'\x1a\x45\xdf\xa3\x87\x42\x82\x84webm\x18\x53\x80\x67\xff\x16\x54\xae\x6b\x8f\xae\x8d\xd7\x81\x01\x83\x81\x01\x86\x85V_VP9\x1f\x43\xb6\x75\x88\xa3\x86\x81\x00\x00\x80ok', 'video/webm'
            if os.environ.get('BAD_MEDIA_PREVIEW'):
                body = b'<html>missing preview</html>'
        Path(args[args.index('--dump-header') + 1]).write_text('HTTP/2 200\r\nContent-Type: ' + content_type + '\r\n\r\n')
        Path(args[args.index('--output') + 1]).write_bytes(body)
        print('200', end='')
        sys.exit(0)
    candidate = 'X-Rangle-Candidate: ci-candidate' in args
    if '--write-out' in args:
        assert url.endswith('.run.app/health')
        if os.environ.get('DIRECT_NETWORK_FAILURE'):
            sys.exit(60)
        key = 'OPEN_TAG_INGRESS' if 'ci-candidate---' in url else 'OPEN_DEFAULT_INGRESS'
        print('200' if os.environ.get(key) else '404', end='')
        sys.exit(0)
    if os.environ.get('FAIL_CANDIDATE') and candidate or os.environ.get('FAIL_POST') and not candidate:
        sys.exit(22)
    if os.environ.get('FAIL_CANONICAL_TLS') and not candidate and url.startswith('https://app.replay.example.test'):
        sys.exit(60)
    if '/health' in url:
        body = 'ok\n'
    elif '/api/v1/live' in url:
        body = json.dumps({'version': 1, 'sessions': []})
        if os.environ.get('BAD_LIVE_CANDIDATE') and candidate or os.environ.get('BAD_LIVE_PUBLIC') and not candidate:
            body = '<html>broken live endpoint</html>'
    elif '/api/' in url:
        clip = {'id': 'vru', 'revision': 'v1', 'title': 'VRU', 'sport': 'basketball',
          'codec': 'synthetic-transport-v1' if os.environ.get('SYNTHETIC_API') else 'rangle-temporal-rgs1',
          'manifest_url': 'https://media.replay.example.test/manifests/vru/v1.json',
          'preview_url': 'https://media.replay.example.test/previews/vru.webm'}
        if os.environ.get('GENERATED_API'):
            clip.pop('codec')
            clip.pop('manifest_url')
            clip.update(id='generated-' + 'c' * 32, revision='c' * 32,
                        scene_url='https://media.replay.example.test/generated/' + 'c' * 32 + '/scene.json')
            if os.environ.get('BAD_GENERATED_ID'):
                clip['id'] = 'unrelated'
            if os.environ.get('BAD_GENERATED_REVISION'):
                clip['revision'] = 'd' * 32
            if os.environ.get('BAD_GENERATED_SCENE'):
                clip['scene_url'] = 'https://media.replay.example.test/generated/' + 'd' * 32 + '/scene.json'
            if os.environ.get('PARTIAL_TRANSPORT'):
                clip['codec'] = 'rangle-temporal-rgs1'
            if os.environ.get('NULL_TRANSPORT'):
                clip.update(codec=None, manifest_url=None)
        body = json.dumps({'clips': [clip]})
    elif '/demo/' in url:
        body = '<html><base href="/demo/"><script src="flutter_bootstrap.js"></script></html>'
    else:
        body = '<html>rangle</html>'
    if '--dump-header' in args:
        header = 'HTTP/2 200\r\n'
        if not os.environ.get('MISSING_REVISION_HEADER'):
            revision = 'rangle-stg-00003-abc'
            if (os.environ.get('BAD_CANDIDATE_REVISION') and candidate or
                os.environ.get('BAD_PUBLIC_REVISION') and not candidate):
                revision = 'rangle-stg-old-a'
            if os.environ.get('STALE_CANDIDATE_ONCE') and candidate and url.endswith('/health'):
                if not (root / 'stale-probe').exists():
                    (root / 'stale-probe').touch()
                    revision = 'rangle-stg-old-a'
            header += 'X-Rangle-Revision: ' + revision + '\r\n'
        if not os.environ.get('MISSING_CATALOG_HEADER'):
            sha = 'd' * 64
            if (os.environ.get('BAD_CANDIDATE_SHA') and candidate or
                os.environ.get('BAD_PUBLIC_SHA') and not candidate or
                os.environ.get('BAD_CANONICAL_SHA') and not candidate and url.startswith('https://app.')):
                sha = 'f' * 64
            header += 'X-Rangle-Catalog-SHA256: ' + sha + '\r\n'
        Path(args[args.index('--dump-header') + 1]).write_text(header + '\r\n')
    Path(args[args.index('--output') + 1]).write_text(body)
    sys.exit(0)
assert name == 'gcloud'
assert '--project=comin-up' in args and '--region=asia-northeast1' in args
image_base = 'asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/server'
manager_service = 'rangle-stg-generation'
manager_url = 'https://rangle-stg-generation-113704194486.asia-northeast1.run.app'
manager_revision = manager_service + '-00003-abc'
if args[:3] == ['run', 'revisions', 'describe'] and args[3].startswith(manager_service + '-') or args[:2] == ['run', 'services'] and args[3] == manager_service:
    if os.environ.get('MISSING_MANAGER'):
        sys.exit(1)
    manager_args = ['-listen', '0.0.0.0:8080', '-generation-manager',
        '-catalog', '/mnt/catalog/snapshots/' + 'd' * 64 + '.json',
        '-generation-config', '/mnt/catalog/generation/' + 'e' * 64 + '.json',
        '-generation-manager-audience', manager_url,
        '-generation-caller-service-account', 'rangle-stg-runtime@comin-up.iam.gserviceaccount.com',
        '-generation-sync-audience', manager_url,
        '-generation-cleanup-url-map', 'rangle-stg-https']
    if os.environ.get('BAD_MANAGER_ARGS'):
        manager_args += ['-firestore-project', 'comin-up']
    if os.environ.get('BAD_MANAGER_CATALOG'):
        manager_args[4] = '/mnt/catalog/snapshots/' + 'f' * 64 + '.json'
    if os.environ.get('BAD_MANAGER_AUDIENCE'):
        manager_args[8] = 'https://unrelated.run.app'
    spec = {'serviceAccountName': 'rangle-stg-generation@comin-up.iam.gserviceaccount.com', 'containers': [
        {'image': image_base + '@sha256:' + 'a' * 64, 'args': manager_args}]}
    if os.environ.get('BAD_MANAGER_IDENTITY'):
        spec['serviceAccountName'] = 'rangle-stg-runtime@comin-up.iam.gserviceaccount.com'
    if os.environ.get('BAD_MANAGER_IMAGE'):
        spec['containers'][0]['image'] = image_base + ':latest'
    if os.environ.get('BAD_MANAGER_ENV'):
        spec['containers'][0]['env'] = [{'name': 'SECRET', 'value': 'DO_NOT_EXPORT_MANAGER_ENV'}]
    if args[:3] == ['run', 'revisions', 'describe']:
        revision = args[3]
        if revision == manager_revision:
            spec['containers'][0]['image'] = os.environ['STG_IMAGE']
            if os.environ.get('DRIFT_MANAGER_ACTUAL_ARGS'):
                spec['containers'][0]['args'] = manager_args + ['-unexpected', 'changed']
        else:
            assert revision in (manager_service + '-old-a', manager_service + '-old-b')
        print(json.dumps({'metadata': {'name': revision}, 'spec': spec}))
        sys.exit(0)
    if args[2] == 'get-iam-policy':
        members = ['serviceAccount:' + account + '@comin-up.iam.gserviceaccount.com' for account in
            ('rangle-stg-runtime', 'rangle-stg-generation-sync', 'rangle-stg-deploy')]
        if os.environ.get('PUBLIC_MANAGER_IAM'):
            members.append('allUsers')
        print(json.dumps({'bindings': [{'role': 'roles/run.invoker', 'members': members}]}))
        sys.exit(0)
    status = {'url': manager_url, 'latestReadyRevisionName': manager_service + '-old-a',
        'traffic': [{'revisionName': manager_service + '-old-a', 'percent': 80},
                    {'revisionName': manager_service + '-old-b', 'percent': 20}]}
    if (root / 'manager-deployed').exists():
        status['latestCreatedRevisionName'] = manager_revision
        spec['containers'][0]['image'] = os.environ['STG_IMAGE']
    if (root / 'manager-promoted').exists():
        status['traffic'] = [{'revisionName': manager_revision, 'percent': 100}]
    if (root / 'manager-tagged').exists():
        status['traffic'].append({'tag': 'ci-manager-candidate', 'revisionName': manager_revision,
            'url': manager_url.replace('https://', 'https://ci-manager-candidate---')})
    doc = {'metadata': {'name': manager_service, 'annotations': {'run.googleapis.com/ingress': 'all',
            'private': 'DO_NOT_EXPORT_MANAGER_METADATA'}}, 'status': status, 'spec': {'template': {'spec': spec}}}
    if os.environ.get('DISABLED_MANAGER_IAM'):
        doc['metadata']['annotations']['run.googleapis.com/invoker-iam-disabled'] = 'true'
    if args[2] == 'update':
        assert '--no-traffic' in args
        assert not any(x.startswith(('--args', '--set-env-vars', '--service-account', '--clear-', '--allow-unauthenticated')) for x in args)
        (root / 'manager-deployed').touch()
        status['latestCreatedRevisionName'] = manager_revision
        spec['containers'][0]['image'] = os.environ['STG_IMAGE']
    elif args[2] == 'update-traffic':
        if '--update-tags=ci-manager-candidate=' + manager_revision in args:
            (root / 'manager-tagged').touch()
        if '--to-revisions=' + manager_revision + '=100' in args:
            (root / 'manager-promoted').touch()
            if os.environ.get('FAIL_MANAGER_PROMOTION'):
                sys.exit(1)
        if '--to-revisions=rangle-stg-generation-old-a=80,rangle-stg-generation-old-b=20' in args:
            (root / 'manager-promoted').unlink(missing_ok=True)
        if '--remove-tags=ci-manager-candidate' in args:
            (root / 'manager-tagged').unlink(missing_ok=True)
            if os.environ.get('FAIL_MANAGER_CLEANUP'):
                sys.exit(1)
    print(json.dumps(doc))
    sys.exit(0)
target_args = ['-ui-root', '/app/web', '-catalog', '/mnt/catalog/snapshots/' + 'd' * 64 + '.json',
               '-asset-origin', 'https://media.replay.example.test',
               '-private-input', 'gs://DO_NOT_EXPORT_INPUT/private']
if os.environ.get('WITH_GENERATION'):
    target_args += ['-generation-config', '/mnt/catalog/generation/' + 'e' * 64 + '.json']
if os.environ.get('WITH_REMOTE_GENERATION'):
    target_args += ['-generation-manager-url', manager_url,
                   '-firestore-project', 'comin-up', '-firestore-database', 'rangle-stg']
secret_env = [{'name': 'PRIVATE_TOKEN', 'value': 'DO_NOT_EXPORT_TOKEN'}]
if args[:3] == ['run', 'revisions', 'describe']:
    revision = args[3]
    if os.environ.get('FAIL_PREVIOUS_READ') and revision == 'rangle-stg-old-b':
        sys.exit(1)
    if revision == 'rangle-stg-old-a':
        item = {'image': image_base + ':legacy-a', 'args': [
            '-catalog', '/mnt/catalog/snapshots/' + '1' * 64 + '.json',
            '-asset-origin', 'https://media.previous.example.test']}
        if os.environ.get('UNKNOWN_PREVIOUS'):
            item = {'image': 'DO_NOT_EXPORT_CREDENTIAL:secret@host/path',
                    'args': ['-catalog', 'gs://DO_NOT_EXPORT_INPUT/legacy.json']}
    elif revision == 'rangle-stg-old-b':
        item = {'image': image_base + '@sha256:' + 'c' * 64, 'args': [
            '-catalog', '/mnt/catalog/snapshots/' + '2' * 64 + '.json',
            '-asset-origin', 'https://media.previous.example.test']}
    else:
        assert revision == 'rangle-stg-00003-abc'
        item = {'image': os.environ['STG_IMAGE'], 'args': target_args}
        if os.environ.get('WRONG_ACTUAL_IMAGE'):
            item['image'] = image_base + '@sha256:' + 'f' * 64
        if os.environ.get('DRIFT_ACTUAL_ARGS'):
            item['args'] = target_args + ['-unexpected', 'changed']
    item['env'] = secret_env
    if os.environ.get('WRONG_PREVIOUS_NAME') and revision.startswith('rangle-stg-old-'):
        revision = 'rangle-stg-unrelated'
    print(json.dumps({'metadata': {'name': revision}, 'spec': {'containers': [item]}}))
    sys.exit(0)
assert args[:2] == ['run', 'services']
status = {'url': 'https://rangle-stg-test.run.app', 'latestReadyRevisionName': 'rangle-stg-old-a',
          'traffic': [{'revisionName': 'rangle-stg-old-a', 'percent': 80}, {'revisionName': 'rangle-stg-old-b', 'percent': 20}]}
if (root / 'deployed').exists():
    status['latestCreatedRevisionName'] = 'rangle-stg-00003-abc'
if (root / 'promoted').exists():
    status['traffic'] = [{'revisionName': 'rangle-stg-00003-abc', 'percent': 100}]
    status['latestReadyRevisionName'] = 'rangle-stg-00003-abc'
    if args[2] == 'describe':
        if os.environ.get('FAIL_AFTER_READ'):
            sys.exit(1)
        if os.environ.get('BAD_AFTER_TRAFFIC'):
            status['traffic'] = [{'revisionName': 'rangle-stg-old-a', 'percent': 80},
                                 {'revisionName': 'rangle-stg-00003-abc', 'percent': 20}]
        if os.environ.get('FAIL_RECEIPT_OUTPUT'):
            Path(os.environ['RELEASE_RECEIPT_PATH']).mkdir()
if (root / 'tagged').exists():
    status['traffic'].append({'tag': 'ci-candidate', 'revisionName': 'rangle-stg-00003-abc',
                             'url': 'https://ci-candidate---rangle-stg-test.run.app'})
doc = {'metadata': {'annotations': {'run.googleapis.com/ingress':
        'all' if os.environ.get('BAD_INGRESS') else 'internal-and-cloud-load-balancing'}},
       'status': status, 'spec': {'template': {'spec': {'containers': [
    {'args': target_args, 'image': os.environ['STG_IMAGE'], 'env': secret_env}]}}}}
if os.environ.get('BAD_RUNTIME'):
    doc['spec']['template']['spec']['containers'][0]['args'] = []
if os.environ.get('BAD_CATALOG_PATH'):
    target_args[3] = '/mnt/catalog/snapshots/not-a-sha.json'
if os.environ.get('BAD_RUNTIME_ORIGIN'):
    target_args[5] += '/'
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
    if '--update-tags=ci-candidate=rangle-stg-00003-abc' in args:
        (root / 'tagged').touch()
        if os.environ.get('FAIL_TAG'):
            sys.exit(1)
        status['traffic'].append({'tag': 'ci-candidate', 'revisionName': 'rangle-stg-00003-abc',
                                 'url': 'https://ci-candidate---rangle-stg-test.run.app'})
    if '--to-revisions=rangle-stg-00003-abc=100' in args:
        (root / 'promoted').touch()
        if os.environ.get('FAIL_PROMOTION'):
            sys.exit(1)
    if '--to-revisions=rangle-stg-old-a=80,rangle-stg-old-b=20' in args:
        (root / 'promoted').unlink(missing_ok=True)
        if os.environ.get('FAIL_PUBLIC_ROLLBACK'):
            sys.exit(1)
    if '--remove-tags=ci-candidate' in args:
        (root / 'tagged').unlink(missing_ok=True)
print(json.dumps(status['traffic'] if args[2] == 'update-traffic' else doc))
'''


class Deployment(unittest.TestCase):
    def run_case(self, extra=None, command='deploy', inspect=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bindir = root / 'bin'
            bindir.mkdir()
            for name in ('gcloud', 'curl', 'date', 'sleep'):
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
                       RUNNER_TEMP=str(root), RELEASE_RECEIPT_PATH=str(root / 'rangle-release-receipt.json'),
                       GITHUB_STEP_SUMMARY=str(root / 'summary.md'),
                       STG_IMAGE='asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/server@sha256:' + 'b' * 64)
            env.update(extra or {})
            if env.get('WITH_REMOTE_GENERATION') and 'GENERATION_MANAGER_ID_TOKEN' not in (extra or {}):
                env['GENERATION_MANAGER_ID_TOKEN'] = 'DO_NOT_EXPORT_MANAGER_ID_TOKEN'
            if env.get('BAD_SUMMARY'):
                env['GITHUB_STEP_SUMMARY'] = str(root)
            invocation = (['bash', str(SCRIPT.with_name('check-deployment.sh')), 'https://ci-candidate---rangle-stg-test.run.app']
                          if command == 'check' else ['bash', str(SCRIPT), command])
            result = subprocess.run(invocation, env=env, capture_output=True, text=True, timeout=20)
            calls = [json.loads(line) for line in (root / 'calls').read_text().splitlines()] if (root / 'calls').exists() else []
            if inspect:
                receipt = root / 'rangle-release-receipt.json'
                summary = root / 'summary.md'
                return result, calls, json.loads(receipt.read_text()) if receipt.is_file() else None, summary.read_text() if summary.is_file() else ''
            return result, calls

    @staticmethod
    def traffic(calls):
        return [arg for call in calls if call[0] == 'gcloud' for arg in call if arg.startswith('--to-revisions=')]

    def test_success_updates_only_image_then_promotes_and_removes_own_tag(self):
        result, calls = self.run_case()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.traffic(calls), ['--to-revisions=rangle-stg-00003-abc=100'])
        self.assertEqual(sum(call[1:4] == ['run', 'services', 'update'] for call in calls), 1)
        self.assertTrue(any('--update-tags=ci-candidate=rangle-stg-00003-abc' in call for call in calls))
        self.assertTrue(any('--remove-tags=ci-candidate' in call for call in calls))
        probed_urls = [call[-1] for call in calls if call[0] == 'curl']
        self.assertIn('https://rangle-stg-test.run.app/health', probed_urls)
        self.assertIn('https://app.replay.example.test/health', probed_urls)
        direct = [call for call in calls if call[0] == 'curl' and '.run.app/' in call[-1]]
        self.assertEqual(len(direct), 2)
        self.assertTrue(all('--write-out' in call for call in direct))
        probes = [call for call in calls if call[0] == 'curl' and '--dump-header' in call and call[-1].startswith('https://app.')]
        self.assertTrue(all(call[-1].startswith('https://app.replay.example.test/') for call in probes))
        self.assertEqual(sum('X-Rangle-Candidate: ci-candidate' in call for call in probes), 5)

    def test_remote_manager_is_authenticated_verified_and_promoted_before_public(self):
        result, calls, receipt, summary = self.run_case({'WITH_REMOTE_GENERATION': '1'}, inspect=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.traffic(calls), ['--to-revisions=rangle-stg-generation-00003-abc=100',
                                             '--to-revisions=rangle-stg-00003-abc=100'])
        updates = [call[4] for call in calls if call[1:4] == ['run', 'services', 'update']]
        self.assertEqual(updates, ['rangle-stg-generation', 'rangle-stg'])
        probes = [call for call in calls if call[0] == 'curl' and 'ci-manager-candidate---' in call[-1]]
        self.assertEqual(len(probes), 1)
        self.assertEqual(probes[0][probes[0].index('--header') + 1], '@-')
        self.assertTrue(probes[0][-1].endswith('/health'))
        self.assertTrue(any('--remove-tags=ci-manager-candidate' in call for call in calls))
        manager = receipt['generation_manager']
        self.assertEqual(manager['candidate']['revision'], 'rangle-stg-generation-00003-abc')
        self.assertEqual(manager['candidate']['generation_config_sha256'], 'e' * 64)
        self.assertEqual(manager['confirmed_traffic'], {'rangle-stg-generation-00003-abc': 100})
        exported = json.dumps(receipt) + summary + result.stdout + result.stderr + json.dumps(calls)
        for secret in ('DO_NOT_EXPORT_MANAGER', 'GENERATION_MANAGER_ID_TOKEN', 'Bearer DO_NOT_EXPORT', 'PRIVATE_TOKEN'):
            self.assertNotIn(secret, exported)

    def test_remote_missing_or_mismatched_manager_fails_before_any_service_mutation(self):
        for key in ('MISSING_MANAGER', 'BAD_MANAGER_ARGS', 'BAD_MANAGER_CATALOG', 'BAD_MANAGER_AUDIENCE',
                    'BAD_MANAGER_IDENTITY', 'BAD_MANAGER_IMAGE', 'BAD_MANAGER_ENV', 'PUBLIC_MANAGER_IAM',
                    'DISABLED_MANAGER_IAM'):
            with self.subTest(key=key):
                result, calls, receipt, _ = self.run_case({'WITH_REMOTE_GENERATION': '1', key: '1'}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(call[1:4] == ['run', 'services', 'update'] for call in calls))
                self.assertEqual(self.traffic(calls), [])
                self.assertIsNone(receipt)

    def test_remote_requires_token_and_never_downgrades_to_legacy(self):
        for change in ({'GENERATION_MANAGER_ID_TOKEN': ''}, {'WITH_GENERATION': '1'}):
            with self.subTest(change=change):
                result, calls, receipt, _ = self.run_case({'WITH_REMOTE_GENERATION': '1', **change}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(call[1:4] == ['run', 'services', 'update'] for call in calls))
                self.assertIsNone(receipt)

    def test_manager_health_auth_status_and_revision_are_required_before_public_update(self):
        for key in ('FAIL_MANAGER_HEALTH', 'BAD_MANAGER_STATUS', 'BAD_MANAGER_REVISION_HEADER',
                    'MISSING_MANAGER_REVISION_HEADER'):
            with self.subTest(key=key):
                result, calls, receipt, _ = self.run_case({'WITH_REMOTE_GENERATION': '1', key: '1'}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.traffic(calls), [])
                updates = [call[4] for call in calls if call[1:4] == ['run', 'services', 'update']]
                self.assertEqual(updates, ['rangle-stg-generation'])
                self.assertTrue(any('--remove-tags=ci-manager-candidate' in call for call in calls))
                self.assertFalse(any('--remove-tags=ci-candidate' in call for call in calls))
                self.assertIsNone(receipt)

    def test_any_public_failure_restores_both_manager_and_public_allocations(self):
        for key in ('FAIL_CANDIDATE', 'BAD_PUBLIC_REVISION', 'FAIL_RECEIPT_OUTPUT', 'BAD_SUMMARY'):
            with self.subTest(key=key):
                result, calls, receipt, _ = self.run_case({'WITH_REMOTE_GENERATION': '1', key: '1'}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.traffic(calls)[-2:], [
                    '--to-revisions=rangle-stg-old-a=80,rangle-stg-old-b=20',
                    '--to-revisions=rangle-stg-generation-old-a=80,rangle-stg-generation-old-b=20'])
                self.assertTrue(any('--remove-tags=ci-manager-candidate' in call for call in calls))
                self.assertTrue(any('--remove-tags=ci-candidate' in call for call in calls))
                self.assertIsNone(receipt)

    def test_partial_manager_promotion_and_actual_revision_drift_roll_back_before_public_update(self):
        for key in ('FAIL_MANAGER_PROMOTION', 'DRIFT_MANAGER_ACTUAL_ARGS'):
            with self.subTest(key=key):
                result, calls, receipt, _ = self.run_case({'WITH_REMOTE_GENERATION': '1', key: '1'}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                updates = [call[4] for call in calls if call[1:4] == ['run', 'services', 'update']]
                self.assertEqual(updates, ['rangle-stg-generation'])
                self.assertEqual(self.traffic(calls)[-2:], [
                    '--to-revisions=rangle-stg-old-a=80,rangle-stg-old-b=20',
                    '--to-revisions=rangle-stg-generation-old-a=80,rangle-stg-generation-old-b=20'])
                self.assertIsNone(receipt)

    def test_legacy_generation_bootstrap_never_reads_or_updates_manager(self):
        result, calls = self.run_case({'WITH_GENERATION': '1'})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any('rangle-stg-generation' in call for call in calls))

    def test_cleanup_failure_or_public_rollback_error_still_restores_manager(self):
        for change in ({'FAIL_MANAGER_CLEANUP': '1'}, {'FAIL_POST': '1', 'FAIL_PUBLIC_ROLLBACK': '1'}):
            with self.subTest(change=change):
                result, calls, receipt, _ = self.run_case({'WITH_REMOTE_GENERATION': '1', **change}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.traffic(calls)[-2:], [
                    '--to-revisions=rangle-stg-old-a=80,rangle-stg-old-b=20',
                    '--to-revisions=rangle-stg-generation-old-a=80,rangle-stg-generation-old-b=20'])
                removals = [arg for call in calls for arg in call if arg.startswith('--remove-tags=')]
                self.assertEqual(set(removals), {'--remove-tags=ci-candidate', '--remove-tags=ci-manager-candidate'})
                self.assertIsNone(receipt)

    def test_direct_ingress_must_be_closed_and_network_errors_are_not_rejection(self):
        for key in ('OPEN_DEFAULT_INGRESS', 'OPEN_TAG_INGRESS', 'DIRECT_NETWORK_FAILURE', 'BAD_INGRESS'):
            with self.subTest(key=key):
                result, calls, receipt, _ = self.run_case({key: '1'}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.traffic(calls), [])
                self.assertIsNone(receipt)
                if key != 'OPEN_TAG_INGRESS':
                    self.assertFalse(any(call[1:4] == ['run', 'services', 'update'] for call in calls))

    def test_candidate_http_revision_mismatch_blocks_promotion_even_with_matching_catalog(self):
        for key in ('BAD_CANDIDATE_REVISION', 'MISSING_REVISION_HEADER'):
            with self.subTest(key=key):
                result, calls, receipt, _ = self.run_case({key: '1'}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.traffic(calls), [])
                self.assertIsNone(receipt)

    def test_candidate_waits_for_tag_routing_to_converge(self):
        result, calls = self.run_case({'STALE_CANDIDATE_ONCE': '1'})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.traffic(calls), ['--to-revisions=rangle-stg-00003-abc=100'])
        probes = [call for call in calls if call[0] == 'curl' and 'X-Rangle-Candidate: ci-candidate' in call]
        self.assertEqual(len(probes), 10)

    def test_public_http_revision_mismatch_restores_previous_split(self):
        result, calls, receipt, _ = self.run_case({'BAD_PUBLIC_REVISION': '1'}, inspect=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.traffic(calls), ['--to-revisions=rangle-stg-00003-abc=100',
                                             '--to-revisions=rangle-stg-old-a=80,rangle-stg-old-b=20'])
        self.assertIsNone(receipt)

    def test_receipt_uses_served_revisions_not_the_desired_template_and_excludes_secrets(self):
        result, calls, receipt, summary = self.run_case({'WITH_GENERATION': '1'}, inspect=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(receipt['source_commit'], 'a' * 40)
        self.assertEqual((receipt['run_id'], receipt['run_attempt']), ('123', '1'))
        self.assertEqual(receipt['candidate']['catalog_sha256'], 'd' * 64)
        self.assertEqual(receipt['candidate']['generation_config_sha256'], 'e' * 64)
        self.assertEqual(receipt['candidate']['asset_origin'], 'https://media.replay.example.test')
        self.assertEqual(receipt['confirmed_traffic'], {'rangle-stg-00003-abc': 100})
        self.assertEqual(receipt['previous_traffic'], {'rangle-stg-old-a': 80, 'rangle-stg-old-b': 20})
        previous = receipt['previous_revisions']
        self.assertEqual([item['catalog_sha256'] for item in previous], ['1' * 64, '2' * 64])
        self.assertTrue(previous[0]['image'].endswith(':legacy-a'))
        self.assertIsNone(previous[0]['image_digest'])
        self.assertTrue(previous[1]['image_digest'].endswith('@sha256:' + 'c' * 64))
        self.assertEqual([call[4] for call in calls if call[1:4] == ['run', 'revisions', 'describe']],
                         ['rangle-stg-old-a', 'rangle-stg-old-b', 'rangle-stg-00003-abc'])
        exported = json.dumps(receipt)
        self.assertLess(len(exported), 8192)
        self.assertIn(json.dumps(receipt, indent=2, sort_keys=True), summary)
        for secret in ('DO_NOT_EXPORT', 'gs://', 'PRIVATE_TOKEN', 'containers', 'workloadIdentityPools'):
            self.assertNotIn(secret, exported + summary)

    def test_unknown_previous_fields_are_null_instead_of_invented(self):
        result, _, receipt, summary = self.run_case({'UNKNOWN_PREVIOUS': '1'}, inspect=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        previous = receipt['previous_revisions'][0]
        for key in ('image', 'image_digest', 'catalog_sha256', 'generation_config_sha256', 'asset_origin'):
            self.assertIsNone(previous[key])
        self.assertIsNone(receipt['candidate']['generation_config_sha256'])
        self.assertNotIn('DO_NOT_EXPORT', json.dumps(receipt) + summary)

    def test_previous_revision_metadata_failure_stops_before_update(self):
        for key in ('FAIL_PREVIOUS_READ', 'WRONG_PREVIOUS_NAME', 'BAD_CATALOG_PATH', 'BAD_RUNTIME_ORIGIN'):
            with self.subTest(key=key):
                result, calls, receipt, _ = self.run_case({key: '1'}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(call[1:4] == ['run', 'services', 'update'] for call in calls))
                self.assertEqual(self.traffic(calls), [])
                self.assertIsNone(receipt)

    def test_candidate_catalog_mismatch_never_promotes(self):
        for key in ('BAD_CANDIDATE_SHA', 'MISSING_CATALOG_HEADER'):
            with self.subTest(key=key):
                result, calls, receipt, _ = self.run_case({key: '1'}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.traffic(calls), [])
                self.assertIsNone(receipt)
                self.assertTrue(any('--remove-tags=ci-candidate' in call for call in calls))

    def test_broken_live_index_blocks_candidate_or_restores_previous_traffic(self):
        for variable in ('BAD_LIVE_CANDIDATE', 'BAD_LIVE_PUBLIC'):
            with self.subTest(variable=variable):
                result, calls, receipt, _ = self.run_case({variable: '1'}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIsNone(receipt)
                updates = [row for row in calls if row[:4] == ['gcloud', 'run', 'services', 'update-traffic']]
                promoted = any('--to-revisions=rangle-stg-00003-abc=100' in row for row in updates)
                self.assertEqual(promoted, variable == 'BAD_LIVE_PUBLIC')
                if promoted:
                    self.assertTrue(any('--to-revisions=rangle-stg-old-a=80,rangle-stg-old-b=20' in row for row in updates))
                self.assertTrue(any('--remove-tags=ci-candidate' in call for call in calls))

    def test_public_catalog_mismatch_restores_the_previous_split(self):
        for key in ('BAD_PUBLIC_SHA', 'BAD_CANONICAL_SHA'):
            with self.subTest(key=key):
                result, calls, receipt, _ = self.run_case({key: '1'}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.traffic(calls)[-1], '--to-revisions=rangle-stg-old-a=80,rangle-stg-old-b=20')
                self.assertIsNone(receipt)

    def test_receipt_validation_or_storage_failure_rolls_back_after_promotion(self):
        for key in ('FAIL_AFTER_READ', 'BAD_AFTER_TRAFFIC', 'WRONG_ACTUAL_IMAGE',
                    'DRIFT_ACTUAL_ARGS', 'FAIL_RECEIPT_OUTPUT', 'BAD_SUMMARY'):
            with self.subTest(key=key):
                result, calls, receipt, _ = self.run_case({key: '1'}, inspect=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.traffic(calls), ['--to-revisions=rangle-stg-00003-abc=100',
                                                     '--to-revisions=rangle-stg-old-a=80,rangle-stg-old-b=20'])
                self.assertIsNone(receipt)
                self.assertTrue(any('--remove-tags=ci-candidate' in call for call in calls))

    def test_receipt_path_is_fixed_before_any_cloud_call(self):
        for change in ({'RELEASE_RECEIPT_PATH': '/tmp/unrelated-receipt.json'}, {'GITHUB_STEP_SUMMARY': ''}):
            with self.subTest(change=change):
                result, calls = self.run_case(change)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(calls, [])

    def test_standalone_smoke_keeps_optional_header_compatibility(self):
        result, calls = self.run_case({'MISSING_CATALOG_HEADER': '1'}, command='check')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any(call[0] == 'gcloud' for call in calls))
        result, calls = self.run_case({'EXPECTED_CATALOG_SHA256': 'invalid'}, command='check')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, [])

    def test_candidate_failure_keeps_previous_traffic(self):
        result, calls = self.run_case({'FAIL_CANDIDATE': '1'})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.traffic(calls), [])
        self.assertTrue(any('--remove-tags=ci-candidate' in call for call in calls))

    def test_media_failure_blocks_candidate_or_restores_previous_traffic(self):
        for flag in ('FAIL_MEDIA_CANDIDATE', 'CORRUPT_MEDIA_MODEL', 'BAD_MEDIA_MANIFEST', 'BAD_MEDIA_PREVIEW'):
            with self.subTest(flag=flag):
                result, calls = self.run_case({flag: '1'})
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.traffic(calls), [])
        result, calls = self.run_case({'FAIL_MEDIA_PUBLIC': '1'})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.traffic(calls), ['--to-revisions=rangle-stg-00003-abc=100',
                                             '--to-revisions=rangle-stg-old-a=80,rangle-stg-old-b=20'])

    def test_successful_generated_result_can_be_first_without_fake_transport_fields(self):
        result, calls = self.run_case({'GENERATED_API': '1'})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.traffic(calls), ['--to-revisions=rangle-stg-00003-abc=100'])

    def test_generated_result_identity_scene_and_transport_shape_must_agree(self):
        for key in ('BAD_GENERATED_ID', 'BAD_GENERATED_REVISION', 'BAD_GENERATED_SCENE',
                    'PARTIAL_TRANSPORT', 'NULL_TRANSPORT'):
            with self.subTest(key=key):
                result, calls = self.run_case({'GENERATED_API': '1', key: '1'})
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.traffic(calls), [])

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
        self.assertTrue(any('--remove-tags=ci-candidate' in call for call in calls))

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
        self.assertTrue(any('--remove-tags=ci-candidate' in call for call in calls))

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
        self.assertTrue(any('--remove-tags=ci-candidate' in call for call in calls))


class ReceiptStorage(unittest.TestCase):
    def test_manager_cleanup_is_optional_pinned_and_forbidden_at_public_runtime(self):
        base = ['-generation-manager', '-catalog', '/mnt/catalog/snapshots/' + 'd' * 64 + '.json',
                '-generation-config', '/mnt/catalog/generation/' + 'e' * 64 + '.json',
                '-generation-manager-audience', RECEIPT.MANAGER_URL,
                '-generation-caller-service-account', RECEIPT.RUNTIME_SA]
        for value in (None, 'rangle-stg-https', 'other-project-map', 'https://untrusted.test'):
            args = base + (['-generation-cleanup-url-map', value] if value is not None else [])
            document = {'spec': {'template': {'spec': {'serviceAccountName': RECEIPT.MANAGER_SA,
                'containers': [{'image': RECEIPT.IMAGE_BASE + '@sha256:' + 'a' * 64, 'args': args}]}}}}
            if value in (None, 'rangle-stg-https'):
                self.assertEqual(RECEIPT.manager_identity(document)['catalog_sha256'], 'd' * 64)
            else:
                with self.assertRaises(ValueError):
                    RECEIPT.manager_identity(document)
        public = {'args': ['-generation-manager-url', RECEIPT.MANAGER_URL,
                           '-generation-cleanup-url-map', 'rangle-stg-https',
                           '-firestore-project', 'comin-up', '-firestore-database', 'rangle-stg']}
        with self.assertRaises(ValueError):
            RECEIPT.generation_remote(public)

    def test_latest_and_tagged_traffic_resolve_to_concrete_serving_revisions(self):
        document = {'status': {'latestReadyRevisionName': 'rangle-stg-old-a', 'traffic': [
            {'latestRevision': True, 'percent': 80},
            {'revisionName': 'rangle-stg-old-b', 'percent': 20},
            {'tag': 'unrelated-tag', 'percent': None},
        ]}}
        self.assertEqual(RECEIPT.traffic(document), {'rangle-stg-old-a': 80, 'rangle-stg-old-b': 20})
        for percent in (True, '100', -1, 101):
            with self.subTest(percent=percent), self.assertRaises(ValueError):
                RECEIPT.traffic({'status': {'traffic': [{'revisionName': 'rangle-stg-old-a', 'percent': percent}]}})

    def test_atomic_output_does_not_replace_existing_receipts_and_cleans_partial_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            output = root / 'receipt.json'
            output.write_text('existing')
            with self.assertRaises(FileExistsError):
                RECEIPT.save_receipt({'schema_version': 1}, output)
            self.assertEqual(output.read_text(), 'existing')
            self.assertEqual(list(root.glob('*.tmp')), [])
            output.unlink()
            with self.assertRaises(IsADirectoryError):
                RECEIPT.save_receipt({'schema_version': 1}, output, root)
            self.assertFalse(output.exists())
            self.assertEqual(list(root.glob('*.tmp')), [])


def load_tests(loader, tests, pattern):
    spec = importlib.util.spec_from_file_location('media_delivery_tests', SCRIPT.with_name('check-media-delivery.test.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    tests.addTests(loader.loadTestsFromModule(module))
    return tests


if __name__ == '__main__':
    unittest.main()
