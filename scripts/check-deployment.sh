#!/usr/bin/env bash
# Bounded HTTP smoke only. No asset uploads or infrastructure changes.
set -euo pipefail

# The deploy script streams the credential on stdin. It is never an argument,
# exported environment variable, response artifact, or release receipt field.
if [[ ${1:-} == manager ]]; then
  base=${2:?Usage: check-deployment.sh manager https://ci-manager-candidate---SERVICE.run.app}
  python3 - "$base" <<'PY'
import os, re, sys
from urllib.parse import urlsplit
u = urlsplit(sys.argv[1])
revision = os.environ.get('EXPECTED_REVISION', '')
if (u.scheme != 'https' or not u.netloc.startswith('ci-manager-candidate---rangle-stg-generation-') or
        not u.netloc.endswith('.run.app') or u.netloc != u.hostname or u.path or u.query or u.fragment or
        not re.fullmatch(r'rangle-stg-generation-[a-z0-9-]+', revision) or len(revision) > 63):
    raise SystemExit('Expected the dedicated manager candidate URL and revision.')
PY
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT
  status=$(curl --silent --show-error --fail-with-body --proto '=https' \
    --connect-timeout 5 --max-time 20 --max-filesize 1024 \
    --header @- --dump-header "$work/health.headers" --output "$work/health" \
    --write-out '%{http_code}' "$base/health")
  [[ $status == 200 ]] || { echo 'Manager candidate health did not return HTTP 200.' >&2; exit 1; }
  python3 - "$work" <<'PY'
import os, sys
from pathlib import Path
work = Path(sys.argv[1])
revisions = []
for line in work.joinpath('health.headers').read_text().splitlines():
    if line.startswith('HTTP/'):
        revisions = []
    elif ':' in line and line.split(':', 1)[0].lower() == 'x-rangle-revision':
        revisions.append(line.split(':', 1)[1].strip())
if revisions != [os.environ['EXPECTED_REVISION']] or work.joinpath('health').read_bytes() != b'ok\n':
    raise SystemExit('Manager health did not serve the selected application revision.')
print('Manager candidate health and revision verified.')
PY
  exit 0
fi

# Share one domain contract with the deployment guard. No DNS lookup is needed
# here: the new domain may not have records yet. HTTPS smoke verifies the edge.
origins=$(python3 - <<'PY'
import ipaddress
import os
import re

domain = os.environ.get('RANGLE_STG_DOMAIN', '')
message = 'Set RANGLE_STG_DOMAIN to a DNS base domain without scheme, credentials, port, path, or whitespace.'
if not domain or len('media.' + domain) > 253 or not domain.isascii() or domain != domain.strip():
    raise SystemExit(message)
domain = domain.lower()
labels = domain.split('.')
label = re.compile(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?')
if len(labels) < 2 or not all(label.fullmatch(part) for part in labels) or not re.fullmatch(r'[a-z][a-z0-9-]*[a-z0-9]', labels[-1]):
    raise SystemExit(message)
try:
    ipaddress.ip_address(domain)
except ValueError:
    pass
else:
    raise SystemExit(message)
if any(part in {'localhost', 'localdomain'} for part in labels) or labels[-1] == 'local':
    raise SystemExit(message)
print('https://app.' + domain)
print('https://media.' + domain)
PY
)
if [[ ${1:-} == origins ]]; then
  printf '%s\n' "$origins"
  exit 0
fi
readonly media_origin=${origins#*$'\n'}

base=${1:?Usage: check-deployment.sh https://app.example.com}
base=${base%/}
python3 - "$base" <<'PY'
import os
import re
import sys
from urllib.parse import urlsplit
u = urlsplit(sys.argv[1])
local = u.scheme == 'http' and u.hostname in {'127.0.0.1', 'localhost'}
if not (u.scheme == 'https' or local) or not u.hostname or u.username or u.password or u.query or u.fragment or u.path:
    raise SystemExit('Expected an HTTPS origin (HTTP loopback is allowed for local tests).')
expected = os.environ.get('EXPECTED_CATALOG_SHA256', '')
if expected and not re.fullmatch(r'[0-9a-f]{64}', expected):
    raise SystemExit('EXPECTED_CATALOG_SHA256 must be a complete lowercase SHA256.')
revision = os.environ.get('EXPECTED_REVISION', '')
if revision and (not re.fullmatch(r'rangle-stg-[a-z0-9-]+', revision) or len(revision) > 63):
    raise SystemExit('EXPECTED_REVISION must name a dedicated stg revision.')
if os.environ.get('RANGLE_CANDIDATE_PROBE', '') not in ('', 'ci-candidate'):
    raise SystemExit('RANGLE_CANDIDATE_PROBE must be empty or ci-candidate.')
PY
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
probe_headers=()
if [[ ${RANGLE_CANDIDATE_PROBE:-} == ci-candidate ]]; then
  probe_headers=(--header 'X-Rangle-Candidate: ci-candidate')
fi
fetch() {
  curl --silent --show-error --fail-with-body --proto '=https,http' \
    --connect-timeout 5 --max-time 20 --max-filesize 1048576 \
    --retry 2 --retry-delay 1 --retry-all-errors \
    "${probe_headers[@]}" --dump-header "$work/$2.headers" --output "$work/$2" "$base$1"
}
fetch /health health
fetch / landing
fetch /demo/ demo
fetch '/api/v1/clips?limit=1' api
fetch /api/v1/live live
python3 - "$work" "$media_origin" <<'PY'
import json
import os
import re
import sys
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import urlsplit

work = Path(sys.argv[1])
expected_media = urlsplit(sys.argv[2])
expected_catalog = os.environ.get('EXPECTED_CATALOG_SHA256', '')
expected_revision = os.environ.get('EXPECTED_REVISION', '')
def response_headers(name):
    headers = {}
    for line in work.joinpath(name + '.headers').read_text().splitlines():
        if line.startswith('HTTP/'):
            headers = {}
        elif ':' in line:
            key, value = line.split(':', 1)
            headers.setdefault(key.lower().strip(), []).append(value.strip())
    return headers
if expected_catalog:
    if response_headers('api').get('x-rangle-catalog-sha256') != [expected_catalog]:
        raise SystemExit('API did not serve the selected static catalog SHA256.')
if expected_revision:
    for name in ('health', 'landing', 'demo', 'api', 'live'):
        if response_headers(name).get('x-rangle-revision') != [expected_revision]:
            raise SystemExit('HTTP response did not serve the selected revision: ' + name)
if work.joinpath('health').read_bytes() != b'ok\n':
    raise SystemExit('health did not return the application health contract.')
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
live = json.loads(work.joinpath('live').read_text())
if (not isinstance(live, dict) or live.get('version') != 1 or
        not isinstance(live.get('sessions'), list) or len(live['sessions']) > 8):
    raise SystemExit('Live API did not return the bounded session index.')
doc = json.loads(work.joinpath('api').read_text())
clips = doc.get('clips') if isinstance(doc, dict) else None
if not isinstance(clips, list) or len(clips) != 1 or not isinstance(clips[0], dict):
    raise SystemExit('API must return one published clip, not an empty/demo HTML response.')
clip = clips[0]
for key in ('id', 'revision', 'title', 'sport'):
    if not isinstance(clip.get(key), str) or not clip[key].strip():
        raise SystemExit(f'API clip is missing {key}.')
if not (clip.get('preview_url') or clip.get('scene_url')):
    raise SystemExit('Smoke requires published playable media, not a synthetic transport fixture.')
if 'codec' in clip or 'manifest_url' in clip:
    for key in ('codec', 'manifest_url'):
        if not isinstance(clip.get(key), str) or not clip[key].strip():
            raise SystemExit(f'Published transport clip is missing {key}.')
    if 'synthetic' in clip['codec'].lower():
        raise SystemExit('Smoke requires published playable media, not a synthetic transport fixture.')
else:
    # Successful generated results intentionally expose a scene without inventing
    # a transport codec/manifest. Their identity and fixed scene path must agree.
    match = re.fullmatch(r'generated-([0-9a-f]{32})', clip['id'])
    scene = urlsplit(clip.get('scene_url', ''))
    if not match or clip['revision'] != match[1] or scene.path != f'/generated/{match[1]}/scene.json':
        raise SystemExit('Generated result identity, revision and scene must agree.')
for key in ('manifest_url', 'preview_url', 'scene_url'):
    value = clip.get(key)
    if value is None:
        continue
    u = urlsplit(value)
    if u.scheme != expected_media.scheme or u.netloc != expected_media.netloc or not u.path.startswith('/') or u.query or u.fragment:
        raise SystemExit(f'{key} must use the configured Cloud CDN media origin.')
print('Smoke passed: health, landing, /demo/, and published clip API.')
PY

# Separate finite media stage: never pass candidate/app headers to the CDN.
python3 "$(dirname "${BASH_SOURCE[0]}")/check-media-delivery.py" "$work/api" "$media_origin"
