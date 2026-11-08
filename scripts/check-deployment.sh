#!/usr/bin/env bash
# Bounded HTTP smoke only. No asset uploads or infrastructure changes.
set -euo pipefail

base=${1:?Usage: check-deployment.sh https://service.run.app}
base=${base%/}
python3 - "$base" <<'PY'
import sys
from urllib.parse import urlsplit
u = urlsplit(sys.argv[1])
local = u.scheme == 'http' and u.hostname in {'127.0.0.1', 'localhost'}
if not (u.scheme == 'https' or local) or not u.hostname or u.username or u.password or u.query or u.fragment or u.path:
    raise SystemExit('Expected an HTTPS origin (HTTP loopback is allowed for local tests).')
PY
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fetch() {
  curl --silent --show-error --fail-with-body --proto '=https,http' \
    --connect-timeout 5 --max-time 20 --max-filesize 1048576 \
    --retry 2 --retry-delay 1 --retry-all-errors \
    --output "$work/$2" "$base$1"
}
fetch /healthz health
fetch / landing
fetch /demo/ demo
fetch '/api/v1/clips?limit=1' api
python3 - "$work" <<'PY'
import json
import sys
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import urlsplit

work = Path(sys.argv[1])
if work.joinpath('health').read_bytes() != b'ok\n':
    raise SystemExit('healthz did not return the application health contract.')
if 'rangle' not in work.joinpath('landing').read_text().lower():
    raise SystemExit('Product landing page is missing.')
class Demo(HTMLParser):
    base = False
    bootstrap = False
    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == 'base' and attrs.get('href') == '/demo/':
            self.base = True
        if tag == 'script' and 'flutter_bootstrap.js' in attrs.get('src', ''):
            self.bootstrap = True
demo = Demo()
demo.feed(work.joinpath('demo').read_text())
if not demo.base or not demo.bootstrap:
    raise SystemExit('Demo base URL or Flutter bootstrap is missing.')
doc = json.loads(work.joinpath('api').read_text())
clips = doc.get('clips') if isinstance(doc, dict) else None
if not isinstance(clips, list) or len(clips) != 1 or not isinstance(clips[0], dict):
    raise SystemExit('API must return one published clip, not an empty/demo HTML response.')
clip = clips[0]
for key in ('id', 'revision', 'title', 'sport', 'codec', 'manifest_url'):
    if not isinstance(clip.get(key), str) or not clip[key].strip():
        raise SystemExit(f'API clip is missing {key}.')
if 'synthetic' in clip['codec'].lower() or not (clip.get('preview_url') or clip.get('scene_url')):
    raise SystemExit('Smoke requires published playable media, not a synthetic transport fixture.')
for key in ('manifest_url', 'preview_url', 'scene_url'):
    value = clip.get(key)
    if value is None:
        continue
    u = urlsplit(value)
    if u.scheme != 'https' or u.netloc != 'rangle-stg-113704194486.web.app' or not u.path.startswith('/') or u.query or u.fragment:
        raise SystemExit(f'{key} must use the separately published stg media origin.')
print('Smoke passed: health, landing, /demo/, and published clip API.')
PY
