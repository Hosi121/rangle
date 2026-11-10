"""Offline release-media fixtures; no DNS, CDN, GCP, or GPU access."""
import contextlib
import gzip
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import struct
import subprocess
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('media_delivery', Path(__file__).with_name('check-media-delivery.py'))
MEDIA = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MEDIA)
ORIGIN = 'https://media.example.test'


def encoded(value):
    return json.dumps(value).encode()


class MediaDelivery(unittest.TestCase):
    def setUp(self):
        self.model = struct.pack('<8sIIIffI', b'RANGL4D1', 1, 56, 3, 25, .08, 0) + bytes(56)
        self.scene = {'version': 1, 'frameCount': 3, 'frameRate': 25, 'temporal': {'url': 'model.rgs'},
                 'camera': {'position': [0, 0, -1], 'lookAt': [0, 0, 0], 'up': [0, 1, 0], 'fovYDegrees': 60},
                 'attribution': {'label': 'Fixture', 'source': 'https://source.example.test/fixture', 'license': 'Fixture only'}}
        self.prefix = '/assets/fixture/r1/full/'
        self.clip = {'id': 'fixture', 'revision': 'r1', 'codec': 'rangle-temporal-rgs1',
                     'manifest_url': ORIGIN + '/manifests/fixture/r1.json',
                     'scene_url': ORIGIN + self.prefix + 'scene.json',
                     'preview_url': ORIGIN + self.prefix + 'preview.webm'}
        self.manifest = {'version': 1, 'clip_id': 'fixture', 'revision': 'r1', 'codec': self.clip['codec'],
                         'representations': [{'id': 'full', 'init_url': self.prefix + 'scene.json',
                          'init_size_bytes': len(encoded(self.scene)), 'segments': [{'index': 0, 'start_seconds': 0,
                           'duration_seconds': .08, 'independent': True, 'url': self.prefix + 'model.rgs',
                           'size_bytes': len(self.model), 'sha256': hashlib.sha256(self.model).hexdigest()}]}]}
        self.bodies = {}
        self.calls = []
        self.status, self.encoding, self.content_type = '200', None, None
        self.omit_gzip = False
        self.refresh()

    def refresh(self):
        self.bodies.update({self.clip['manifest_url']: encoded(self.manifest),
                            self.clip['scene_url']: encoded(self.scene),
                            ORIGIN + self.prefix + 'model.rgs': self.model,
                            self.clip['preview_url']: b'\x1a\x45\xdf\xa3\x87\x42\x82\x84webm\x18\x53\x80\x67\xff\x16\x54\xae\x6b\x8f\xae\x8d\xd7\x81\x01\x83\x81\x01\x86\x85V_VP9\x1f\x43\xb6\x75\x88\xa3\x86\x81\x00\x00\x80ok'})

    def run_curl(self, args, **kwargs):
        self.calls.append(args)
        self.assertEqual(args[:2], ['curl', '--disable'])
        self.assertEqual(args[args.index('--header') + 1],
                         'Accept-Encoding: gzip' if args[-1].endswith('model.splat') else 'Accept-Encoding: identity')
        self.assertNotIn('--location', args)
        self.assertNotIn('--retry', args)
        self.assertFalse(any('Candidate' in arg or 'Authorization' in arg or 'Cookie:' in arg for arg in args))
        self.assertLessEqual(kwargs['timeout'], 20)
        url = args[-1]
        if url not in self.bodies:
            return subprocess.CompletedProcess(args, 22, '404', '')
        body = self.bodies[url]
        kind = 'application/json' if url.endswith('.json') else ('video/webm' if url.endswith('.webm') else 'application/octet-stream')
        headers = 'HTTP/2 200\r\nContent-Type: ' + (self.content_type or kind) + '\r\n'
        encoding = self.encoding or ('gzip' if url.endswith('model.splat') and not self.omit_gzip else None)
        if encoding:
            headers += 'Content-Encoding: ' + encoding + '\r\n'
        Path(args[args.index('--dump-header') + 1]).write_text(headers + '\r\n')
        Path(args[args.index('--output') + 1]).write_bytes(body)
        return subprocess.CompletedProcess(args, 0, self.status, '')

    def check(self):
        media = MEDIA.Media(ORIGIN)
        with patch.object(MEDIA.subprocess, 'run', side_effect=self.run_curl), contextlib.redirect_stdout(io.StringIO()):
            MEDIA.check(self.clip, media)
        return media

    def test_transport_samples_exact_bytes_and_only_first_segment(self):
        # A later segment is deliberately absent: this smoke proves only a sample.
        self.manifest['representations'][0]['segments'].append({'url': '/not-fetched'})
        self.refresh()
        media = self.check()
        self.assertEqual(media.requests, 4)
        self.assertEqual(media.bytes, sum(len(self.bodies[call[-1]]) for call in self.calls))

    def test_segmented_profile_uses_coverage_duration_and_minimum_two_segments(self):
        self.clip['codec'] = self.manifest['codec'] = MEDIA.SEGMENTED_CODEC
        segments = self.manifest['representations'][0]['segments']
        segments[0]['duration_seconds'] = .12  # N/FPS; wire final sample remains .08.
        segments.append(dict(segments[0], index=1, start_seconds=.12,
                             url=self.prefix + 'model-00001.rgs'))
        self.refresh()
        self.assertEqual(self.check().requests, 4)
        # Later model is deliberately absent: only the first segment is sampled.
        for invalid_duration in (.08, .121):
            with self.subTest(duration=invalid_duration):
                segments[0]['duration_seconds'] = invalid_duration
                self.refresh()
                with self.assertRaises(MEDIA.SmokeError):
                    self.check()
        segments[0]['duration_seconds'] = .12
        segments.pop()
        self.refresh()
        with self.assertRaises(MEDIA.SmokeError):
            self.check()

    def test_missing_corrupt_identity_codec_scene_preview_and_size_fail(self):
        cases = ['missing', 'checksum', 'size', 'identity', 'codec', 'scene', 'preview', 'init-size']
        for case in cases:
            with self.subTest(case=case):
                self.setUp()
                if case == 'missing':
                    del self.bodies[ORIGIN + self.prefix + 'model.rgs']
                elif case == 'checksum':
                    self.bodies[ORIGIN + self.prefix + 'model.rgs'] = self.model[:-1] + b'X'
                elif case == 'preview':
                    self.bodies[self.clip['preview_url']] = b'<html>not WebM</html>'
                elif case == 'scene':
                    self.scene['frameCount'] = 4
                    self.refresh()
                else:
                    if case == 'size':
                        self.manifest['representations'][0]['segments'][0]['size_bytes'] += 1
                    elif case == 'identity':
                        self.manifest['revision'] = 'r2'
                    elif case == 'codec':
                        self.manifest['codec'] = 'unknown'
                    else:
                        self.manifest['representations'][0]['init_size_bytes'] += 1
                    self.refresh()
                with self.assertRaises(MEDIA.SmokeError):
                    self.check()

    def generated(self, static=False, legacy=False):
        job = 'c' * 32
        self.prefix = '/generated/' + job + '/'
        self.clip = {'id': 'generated-' + job, 'revision': job, 'scene_url': ORIGIN + self.prefix + 'scene.json'}
        metrics = {'job_id': job, 'mode': 'static' if static else 'dynamic'}
        if static:
            raw = bytes(64)
            self.model = gzip.compress(raw)
            self.scene = {'version': 1, 'frameRate': 1, 'frames': ['model.splat'],
                          'camera': self.scene['camera'], 'attribution': self.scene['attribution']}
            metrics.update(splat_bytes=len(raw), splat_sha256=hashlib.sha256(raw).hexdigest())
        if not legacy:
            metrics.update(model_bytes=len(self.model), model_sha256=hashlib.sha256(self.model).hexdigest())
        self.bodies = {self.clip['scene_url']: encoded(self.scene),
                       ORIGIN + self.prefix + 'metrics.json': encoded(metrics),
                       ORIGIN + self.prefix + ('model.splat' if static else 'model.rgs'): self.model}
        return metrics

    def test_generated_dynamic_and_static_and_legacy_static_proofs(self):
        for static, legacy in ((False, False), (True, False), (True, True)):
            with self.subTest(static=static, legacy=legacy):
                self.setUp()
                self.generated(static, legacy)
                self.assertEqual(self.check().requests, 3)

    def test_scene_missing_degenerate_or_nonfinite_camera_and_attribution_fail(self):
        for change in ('missing-camera', 'zero-up', 'same-position', 'nonfinite', 'bad-fov', 'missing-attribution'):
            with self.subTest(change=change):
                self.setUp()
                if change == 'missing-camera':
                    self.scene.pop('camera')
                elif change == 'missing-attribution':
                    self.scene['attribution']['license'] = ' '
                elif change == 'zero-up':
                    self.scene['camera']['up'] = [0, 0, 0]
                elif change == 'same-position':
                    self.scene['camera']['position'] = [0, 0, 0]
                elif change == 'nonfinite':
                    self.scene['camera']['up'][0] = float('inf')
                else:
                    self.scene['camera']['fovYDegrees'] = 180
                self.manifest['representations'][0]['init_size_bytes'] = len(encoded(self.scene))
                self.refresh()
                with self.assertRaises(MEDIA.SmokeError):
                    self.check()
        # Generated clips share the same pre-initialization checks.
        self.setUp()
        self.generated()
        self.scene.pop('camera')
        self.bodies[self.clip['scene_url']] = encoded(self.scene)
        with self.assertRaises(MEDIA.SmokeError):
            self.check()

    def test_transport_scene_and_static_gzip_response_encoding_are_required(self):
        self.clip.pop('scene_url')
        rep = self.manifest['representations'][0]
        rep.pop('init_url')
        rep.pop('init_size_bytes')
        self.bodies[self.clip['manifest_url']] = encoded(self.manifest)
        with self.assertRaises(MEDIA.SmokeError):
            self.check()
        self.setUp()
        self.generated(static=True)
        self.omit_gzip = True
        with self.assertRaises(MEDIA.SmokeError):
            self.check()

    def test_generated_partial_or_wrong_proofs_and_wrong_job_fail(self):
        for key, value in (('model_sha256', None), ('model_bytes', 100), ('job_id', 'd' * 32)):
            with self.subTest(key=key):
                self.setUp()
                metrics = self.generated()
                metrics[key] = value
                self.bodies[ORIGIN + self.prefix + 'metrics.json'] = encoded(metrics)
                with self.assertRaises(MEDIA.SmokeError):
                    self.check()

    def test_url_origin_credentials_query_traversal_and_redirects_fail(self):
        for url in ('https://app.example.test/x', 'https://user@media.example.test/x',
                    'https://media.example.test/x?q=1', '/assets/../secret', '/assets/%2e%2e/secret'):
            with self.subTest(url=url), self.assertRaises(MEDIA.SmokeError):
                MEDIA.Media(ORIGIN).url(url)
        self.status = '302'
        with self.assertRaises(MEDIA.SmokeError):
            self.check()
        self.assertEqual(len(self.calls), 1)

    def test_request_byte_time_response_type_and_encoding_limits(self):
        for field, value in (('requests', MEDIA.MAX_REQUESTS), ('bytes', MEDIA.MAX_TOTAL), ('deadline', 0)):
            media = MEDIA.Media(ORIGIN)
            setattr(media, field, value)
            with patch.object(MEDIA.subprocess, 'run') as run, self.assertRaises(MEDIA.SmokeError):
                media.fetch(self.clip['manifest_url'], 1024, 'application/json')
            run.assert_not_called()
        for attribute, value in (('content_type', 'text/html'), ('encoding', 'gzip')):
            with self.subTest(attribute=attribute):
                self.setUp()
                setattr(self, attribute, value)
                with self.assertRaises(MEDIA.SmokeError):
                    self.check()
        self.setUp()
        with patch.object(MEDIA.subprocess, 'run', side_effect=self.run_curl):
            with self.assertRaises(MEDIA.SmokeError):
                MEDIA.Media(ORIGIN).fetch(self.clip['manifest_url'], 1, 'application/json')
        with patch.object(MEDIA.subprocess, 'run', side_effect=subprocess.TimeoutExpired('curl', 20)):
            with self.assertRaises(MEDIA.SmokeError):
                MEDIA.Media(ORIGIN).fetch(self.clip['manifest_url'], 1024, 'application/json')

    def test_preview_truncation_and_html_with_fake_magic_fail(self):
        valid = self.bodies[self.clip['preview_url']]
        for invalid in (valid[:-1], b'\x1a\x45\xdf\xa3webm\x18\x53\x80\x67html'):
            with self.subTest(body=invalid), self.assertRaises(MEDIA.SmokeError):
                MEDIA.webm(invalid)

    def test_existing_shared_raw_and_packed_wire_fixtures(self):
        root = Path(__file__).resolve().parents[1] / 'testdata/contracts/v1'
        corpus = json.loads((root / 'cases.json').read_text())
        for case in corpus['cases']:
            if case['profile'] == 'rgs' and case['accept'] and not case.get('recipe'):
                with self.subTest(case=case['id']):
                    MEDIA.temporal((root / case['file']).read_bytes())

    def test_duplicate_json_and_gzip_tail_or_expansion_are_rejected(self):
        for body in (b'{"version":1,"version":1}', b'{"number":NaN}'):
            with self.assertRaises(MEDIA.SmokeError):
                MEDIA.document(body)
        for body, maximum in ((gzip.compress(bytes(32)), 16),
                              (gzip.compress(bytes(32)) + gzip.compress(b''), 32),
                              (gzip.compress(bytes(32))[:-1], 32)):
            with self.assertRaises(MEDIA.SmokeError):
                MEDIA.inflate(body, maximum)


if __name__ == '__main__':
    unittest.main()
