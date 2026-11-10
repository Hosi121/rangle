#!/usr/bin/env python3
"""Finite release sample, not the provenance/publication gate or a render test.

Each candidate/public stage samples one API clip and its first transport segment
(or generated model). Media requests never inherit application routing headers.
"""
import argparse
import hashlib
import json
import math
from pathlib import Path
import re
import struct
import subprocess
import tempfile
import time
from urllib.parse import urljoin, urlsplit
import zlib

MAX_REQUESTS = 8
MAX_TOTAL = 64 << 20
MAX_SECONDS = 60
MAX_MODEL = 32 << 20
MAX_PREVIEW = 16 << 20
MAX_JSON = 1 << 20
LEGACY_CODEC = 'rangle-temporal-rgs1'
SEGMENTED_CODEC = 'rangle-temporal-rgs1-segments-v1'


class SmokeError(ValueError):
    pass


def require(condition, message):
    if not condition:
        raise SmokeError(message)


def document(body):
    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, 'duplicate JSON key')
            result[key] = value
        return result
    try:
        result = json.loads(body, object_pairs_hook=pairs,
                            parse_constant=lambda _: require(False, 'non-finite JSON number'))
    except (UnicodeError, json.JSONDecodeError) as error:
        raise SmokeError('invalid media JSON') from error
    require(isinstance(result, dict), 'expected media JSON object')
    return result


def integer(value, maximum=MAX_MODEL):
    return type(value) is int and 0 < value <= maximum


def number(value):
    return type(value) in (int, float) and math.isfinite(value)


def proof(body, size, digest):
    require(integer(size) and isinstance(digest, str) and re.fullmatch(r'[0-9a-f]{64}', digest),
            'invalid size/SHA256 proof')
    require(len(body) == size and hashlib.sha256(body).hexdigest() == digest,
            'media size/SHA256 mismatch')


def inflate(body, maximum):
    decoder = zlib.decompressobj(31)
    try:
        raw = decoder.decompress(body, maximum + 1)
    except zlib.error as error:
        raise SmokeError('corrupt gzip model') from error
    require(len(raw) <= maximum and decoder.eof and not decoder.unused_data and not decoder.unconsumed_tail,
            'gzip model exceeds bound, is truncated, or has trailing members')
    return raw


def temporal(body, scene=None):
    require(len(body) >= 32, 'truncated RGS1 model')
    magic, count, stride, frames, fps, duration, reserved = struct.unpack('<8sIIIffI', body[:32])
    require(magic in (b'RANGL4D1', b'RANGL4Z1') and integer(count, 500000) and stride == 56
            and integer(frames, 15000) and frames >= 2 and number(fps) and 0 < fps <= 240
            and number(duration) and abs(duration - (frames - 1) / fps) <= 1e-5 and reserved == 0,
            'invalid RGS1 header/timeline')
    expected = count * stride
    require(expected + 32 <= MAX_MODEL, 'decoded RGS1 exceeds model bound')
    if magic == b'RANGL4Z1':
        require(len(body) >= 50 and not body[35] & 0xe0, 'invalid RGS1 gzip header')
        require(len(inflate(body[32:], expected)) == expected, 'RGS1 decoded size mismatch')
    else:
        require(len(body) == expected + 32, 'RGS1 raw size mismatch')
    if scene is not None:
        require(scene.get('frameCount') == frames and number(scene.get('frameRate'))
                and struct.unpack('<f', struct.pack('<f', scene['frameRate']))[0] == fps,
                'scene/model timeline mismatch')
    return {'frames': frames, 'fps': fps, 'duration': duration}


def playable_scene(scene):
    # The viewer needs these fields before installing a model. Provenance
    # approval, optional calibrated camera fields and rendering remain separate.
    require(scene.get('version') == 1, 'invalid scene version')
    camera = scene.get('camera')
    require(isinstance(camera, dict), 'missing scene camera')
    for key in ('position', 'lookAt', 'up'):
        value = camera.get(key)
        require(isinstance(value, list) and len(value) == 3 and all(number(n) for n in value),
                'invalid scene camera vector')
    require(camera['position'] != camera['lookAt'] and any(camera['up'])
            and number(camera.get('fovYDegrees')) and 0 < camera['fovYDegrees'] < 180,
            'degenerate scene camera')
    attribution = scene.get('attribution')
    require(isinstance(attribution, dict) and all(isinstance(attribution.get(key), str)
            and attribution[key].strip() for key in ('label', 'source', 'license')), 'missing scene attribution')


def webm(body):
    # Structural container smoke only; compressed video frames are not decoded.
    def elements(data):
        offset, visited = 0, 0
        while offset < len(data):
            visited += 1
            require(visited <= 10000, 'too many WebM elements')
            def vint(position, keep_marker):
                require(position < len(data) and data[position] != 0, 'truncated EBML integer')
                width = 1
                while not data[position] & (128 >> (width - 1)):
                    width += 1
                require(width <= (4 if keep_marker else 8) and position + width <= len(data), 'invalid EBML integer')
                value = int.from_bytes(data[position:position + width], 'big')
                if not keep_marker:
                    value &= (1 << (7 * width)) - 1
                return value, width
            tag, width = vint(offset, True)
            offset += width
            size, width = vint(offset, False)
            offset += width
            if size == (1 << (7 * width)) - 1:
                require(tag in (0x18538067, 0x1f43b675), 'unexpected unknown WebM element size')
                size = len(data) - offset
            require(size <= len(data) - offset, 'truncated WebM element')
            yield tag, data[offset:offset + size]
            offset += size
    top = list(elements(body))
    require(len(top) == 2 and top[0][0] == 0x1a45dfa3 and len(top[0][1]) <= 4096
            and top[1][0] == 0x18538067, 'invalid WebM header/segment')
    require([value for tag, value in elements(top[0][1]) if tag == 0x4282] == [b'webm'], 'invalid WebM DocType')
    video, blocks = False, False
    for tag, value in elements(top[1][1]):
        if tag == 0x1654ae6b:
            for entry, track in elements(value):
                if entry != 0xae:
                    continue
                fields = dict(elements(track))
                video |= (int.from_bytes(fields.get(0x83, b''), 'big') == 1 and fields.get(0x86) in (b'V_VP8', b'V_VP9', b'V_AV1'))
        if tag == 0x1f43b675:
            blocks |= any(tag in (0xa3, 0xa0) and len(value) >= 4 for tag, value in elements(value))
    require(video and blocks, 'WebM requires a video track and media blocks')


class Media:
    def __init__(self, origin, clock=time.monotonic):
        u = urlsplit(origin)
        require(u.scheme == 'https' and u.hostname and u.netloc == u.hostname and not u.path
                and not u.query and not u.fragment, 'expected fixed HTTPS media origin')
        self.origin, self.clock = origin, clock
        self.deadline = clock() + MAX_SECONDS
        self.requests = self.bytes = 0

    def url(self, value, base=None):
        require(isinstance(value, str) and value and not any(c.isspace() for c in value), 'invalid media URL')
        url = urljoin(base or self.origin + '/', value)
        u, origin = urlsplit(url), urlsplit(self.origin)
        require(u.scheme == origin.scheme and u.netloc == origin.netloc and u.path.startswith('/')
                and not u.query and not u.fragment and not any(c in u.path for c in '\\%')
                and all(p not in ('.', '..', '') for p in u.path.split('/')[1:]),
                'media URL must stay on the fixed origin and immutable path')
        # Reject dot traversal before urljoin normalizes it.
        require(all(p not in ('.', '..') for p in urlsplit(value).path.split('/')), 'media URL traversal')
        return url

    def fetch(self, value, maximum, media_type, encoding='identity'):
        url = self.url(value)
        remaining = self.deadline - self.clock()
        require(remaining > 0 and self.requests < MAX_REQUESTS, 'media request/time budget exceeded')
        maximum = min(maximum, MAX_TOTAL - self.bytes)
        require(maximum > 0, 'media byte budget exceeded')
        self.requests += 1
        with tempfile.TemporaryDirectory(prefix='rangle-media-smoke-') as directory:
            output, headers = Path(directory) / 'body', Path(directory) / 'headers'
            # --disable ignores user curl config, no redirect/retry/auth/header
            # forwarding; one subprocess timeout covers connect, headers and body.
            # Stored static splats request gzip, like the publication verifier,
            # to prevent GCS decompressive transcoding before wire SHA checks.
            command = ['curl', '--disable', '--silent', '--show-error', '--fail-with-body',
                       '--proto', '=https', '--connect-timeout', '5', '--max-time', str(min(20, remaining)),
                       '--max-filesize', str(maximum), '--header', 'Accept-Encoding: ' + encoding,
                       '--dump-header', str(headers), '--output', str(output), '--write-out', '%{http_code}', url]
            try:
                result = subprocess.run(command, capture_output=True, text=True, timeout=min(20, remaining))
            except subprocess.TimeoutExpired as error:
                raise SmokeError('media request timed out') from error
            require(result.returncode == 0 and result.stdout == '200', 'media must return HTTP 200 without redirects')
            require(output.stat().st_size <= maximum, 'media body exceeds limit')
            body = output.read_bytes()
            values = {}
            for line in headers.read_text().splitlines():
                if line.startswith('HTTP/'):
                    values = {}
                elif ':' in line:
                    key, value = line.split(':', 1)
                    values.setdefault(key.lower().strip(), []).append(value.strip())
            require(values.get('content-type', [''])[0].split(';')[0].strip() == media_type, 'incorrect media Content-Type')
            require(values.get('content-encoding', ['identity']) == [encoding], 'incorrect media Content-Encoding')
            require(body, 'empty media body')
        self.bytes += len(body)
        require(self.clock() <= self.deadline, 'media stage deadline exceeded')
        return body

    def json(self, url, maximum=MAX_JSON):
        return document(self.fetch(url, maximum, 'application/json'))


def check(clip, media):
    require(isinstance(clip, dict), 'invalid API clip')
    scene_url = media.url(clip['scene_url']) if clip.get('scene_url') else None
    if 'codec' in clip or 'manifest_url' in clip:
        require(clip.get('codec') in (LEGACY_CODEC, SEGMENTED_CODEC), 'unsupported published transport codec')
        segmented = clip['codec'] == SEGMENTED_CODEC
        require(re.fullmatch(r'[A-Za-z0-9_-]{1,64}', clip.get('id', ''))
                and re.fullmatch(r'[A-Za-z0-9_-]{1,64}', clip.get('revision', '')), 'invalid transport identity')
        path = f"/manifests/{clip['id']}/{clip['revision']}.json"
        require(media.url(clip['manifest_url']) == media.origin + path, 'manifest URL identity mismatch')
        manifest = media.json(clip['manifest_url'])
        require(manifest.get('version') == 1 and manifest.get('clip_id') == clip['id']
                and manifest.get('revision') == clip['revision'] and manifest.get('codec') == clip['codec'],
                'API/manifest identity or codec mismatch')
        reps = manifest.get('representations')
        require(isinstance(reps, list) and 0 < len(reps) <= 16 and isinstance(reps[0], dict), 'invalid representations')
        rep = reps[0]
        require(re.fullmatch(r'[A-Za-z0-9_-]{1,64}', rep.get('id', '')), 'invalid representation identity')
        prefix = f"/assets/{clip['id']}/{clip['revision']}/{rep['id']}/"
        segments = rep.get('segments')
        require(isinstance(segments, list) and segments and isinstance(segments[0], dict), 'missing transport segment')
        require(not segmented or len(segments) >= 2, 'segmented profile requires at least two segments')
        segment = segments[0]
        require(segment.get('index') == 0 and segment.get('start_seconds') == 0
                and number(segment.get('duration_seconds')) and segment['duration_seconds'] > 0
                and segment.get('independent') is True, 'invalid first independent segment')
        model_url = media.url(segment['url'])
        require(urlsplit(model_url).path.startswith(prefix) and integer(segment.get('size_bytes')), 'segment path/size mismatch')
        if rep.get('init_url'):
            init = media.url(rep['init_url'])
            require(urlsplit(init).path.startswith(prefix) and integer(rep.get('init_size_bytes'), 64 << 10), 'invalid scene init')
            require(scene_url is None or scene_url == init, 'API/init scene mismatch')
            scene_url = init
            scene_body = media.fetch(init, 64 << 10, 'application/json')
            require(len(scene_body) == rep['init_size_bytes'], 'scene init size mismatch')
            scene = document(scene_body)
        elif scene_url:
            scene = media.json(scene_url, 64 << 10)
        else:
            scene = None
        require(scene is not None, 'transport profile requires its playable scene')
        playable_scene(scene)
        model = media.fetch(model_url, segment['size_bytes'], 'application/octet-stream')
        proof(model, segment['size_bytes'], segment.get('sha256'))
        wire = temporal(model, scene)
        # Header keeps the final local sample, including for segmented RGS1.
        # Only new-profile transport coverage holds that final sample to N/FPS.
        duration = wire['frames'] / wire['fps'] if segmented else wire['duration']
        require(abs(segment['duration_seconds'] - duration) <= 1e-5, 'transport/model duration mismatch')
        if scene is not None:
            require(urlsplit(scene_url).path.startswith(prefix), 'scene revision path mismatch')
            duration = (scene['frameCount'] if segmented else scene['frameCount'] - 1) / scene['frameRate']
            require(abs(segment['duration_seconds'] - duration) <= 1e-5, 'transport/scene duration mismatch')
            require(scene.get('version') == 1 and scene.get('temporal', {}).get('url') == urlsplit(model_url).path.rsplit('/', 1)[-1], 'scene/model reference mismatch')
    else:
        match = re.fullmatch(r'generated-([0-9a-f]{32})', clip.get('id', ''))
        require(match and clip.get('revision') == match[1] and scene_url == media.origin + '/generated/' + match[1] + '/scene.json', 'generated API identity mismatch')
        scene = media.json(scene_url, 64 << 10)
        playable_scene(scene)
        require(scene.get('version') == 1 and number(scene.get('frameRate')) and 0 < scene['frameRate'] <= 240,
                'invalid generated scene')
        metrics = media.json(media.url('metrics.json', scene_url), 256 << 10)
        require(metrics.get('job_id') == match[1], 'generated metrics identity mismatch')
        dynamic = scene.get('temporal') is not None
        if dynamic:
            require(scene['temporal'] == {'url': 'model.rgs'} and not scene.get('frames')
                    and metrics.get('mode') == 'dynamic', 'invalid generated temporal mode')
            model = media.fetch(media.url('model.rgs', scene_url), MAX_MODEL, 'application/octet-stream')
            proof(model, metrics.get('model_bytes'), metrics.get('model_sha256'))
            temporal(model, scene)
        else:
            require(scene.get('frames') == ['model.splat'] and metrics.get('mode', 'static') == 'static', 'invalid generated static mode')
            model = media.fetch(media.url('model.splat', scene_url), MAX_MODEL, 'application/octet-stream', 'gzip')
            if 'model_bytes' in metrics or 'model_sha256' in metrics:
                proof(model, metrics.get('model_bytes'), metrics.get('model_sha256'))
            require(integer(metrics.get('splat_bytes')) and metrics['splat_bytes'] % 32 == 0, 'invalid raw splat size')
            raw = inflate(model, metrics['splat_bytes'])
            proof(raw, metrics['splat_bytes'], metrics.get('splat_sha256'))
    if clip.get('preview_url'):
        preview = media.fetch(clip['preview_url'], MAX_PREVIEW, 'video/webm')
        webm(preview)
    require(media.clock() <= media.deadline, 'media stage deadline exceeded')
    print(f'Media sample passed: {media.requests}/{MAX_REQUESTS} requests, {media.bytes}/{MAX_TOTAL} bytes; '
          'first clip/segment only, no rendering or full-catalog claim.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('api', type=Path)
    parser.add_argument('media_origin')
    args = parser.parse_args()
    require(args.api.stat().st_size <= MAX_JSON, 'API document too large')
    clips = document(args.api.read_bytes()).get('clips')
    require(isinstance(clips, list) and len(clips) == 1, 'expected one API clip')
    check(clips[0], Media(args.media_origin))


if __name__ == '__main__':
    try:
        main()
    except (SmokeError, KeyError, TypeError, OSError, struct.error) as error:
        raise SystemExit('Media smoke failed: ' + str(error))
