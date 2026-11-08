#!/usr/bin/env bash
# Only update the image/traffic of the pre-existing, dedicated stg service.
set -Eeuo pipefail
readonly project=comin-up region=asia-northeast1 service=rangle-stg
readonly deadline=2026-11-14T15:00:00Z
readonly deploy_sa=rangle-stg-deploy@comin-up.iam.gserviceaccount.com
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir

check_deadline() {
  if (( $(date -u +%s) >= $(date -u -d "$deadline" +%s) )); then
    echo "The authorized stg window ended at $deadline." >&2
    return 1
  fi
}
guard() {
  check_deadline
  local origins
  origins=$(bash "$script_dir/check-deployment.sh" origins) || return
  app_origin=${origins%%$'\n'*}
  media_origin=${origins#*$'\n'}
  [[ ${GITHUB_REPOSITORY:-} == Hosi121/rangle && ${GITHUB_REPOSITORY_ID:-} == 1448803636 && ${GITHUB_REPOSITORY_OWNER_ID:-} == 128687243 ]] || {
    echo 'Only the authorized GitHub repository may deploy.' >&2; return 1;
  }
  [[ ${GITHUB_REF:-} == refs/heads/stg && ${GITHUB_WORKFLOW_REF:-} == Hosi121/rangle/.github/workflows/deploy-stg.yml@refs/heads/stg ]] || {
    echo 'Only the merged stg deployment workflow may deploy.' >&2; return 1;
  }
  [[ ${GITHUB_EVENT_NAME:-} == push || ${GITHUB_EVENT_NAME:-} == workflow_dispatch ]] || {
    echo 'PR credentials and other event types are not accepted.' >&2; return 1;
  }
  [[ ${GCP_DEPLOY_SERVICE_ACCOUNT:-} == "$deploy_sa" ]] || {
    echo 'Set GCP_DEPLOY_SERVICE_ACCOUNT to the dedicated stg deploy identity.' >&2; return 1;
  }
  [[ ${GCP_WORKLOAD_IDENTITY_PROVIDER:-} =~ ^projects/113704194486/locations/global/workloadIdentityPools/[a-zA-Z0-9_-]+/providers/[a-zA-Z0-9_-]+$ ]] || {
    echo 'Set GCP_WORKLOAD_IDENTITY_PROVIDER to the approved project WIF provider.' >&2; return 1;
  }
}
case ${1:-} in
  deadline) check_deadline; exit ;;
  guard) guard; exit ;;
  deploy) guard ;;
  *) echo 'Usage: deploy-stg.sh guard|deadline|deploy' >&2; exit 2 ;;
esac
readonly app_origin media_origin
[[ ${STG_IMAGE:-} =~ ^asia-northeast1-docker\.pkg\.dev/comin-up/rangle-stg/server@sha256:[0-9a-f]{64}$ ]] || {
  echo 'STG_IMAGE must be an immutable digest in the dedicated stg repository.' >&2; exit 1;
}
[[ ${GITHUB_SHA:-} =~ ^[0-9a-f]{40}$ && ${GITHUB_RUN_ID:-} =~ ^[0-9]+$ && ${GITHUB_RUN_ATTEMPT:-} =~ ^[0-9]+$ ]] || {
  echo 'A complete commit SHA and GitHub run identity are required.' >&2; exit 1;
}
readonly tag="candidate-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"
work=$(mktemp -d)
promotion_attempted=false
candidate_created=false
previous_traffic=''
cloud_run() { gcloud run "$@" --project="$project" --region="$region" --quiet; }
cleanup() {
  local result=$?
  trap - EXIT
  if [[ $result != 0 && $promotion_attempted == true ]]; then
    echo 'Promotion failed; restoring the previous explicit revision allocation.' >&2
    if ! cloud_run services update-traffic "$service" --to-revisions="$previous_traffic"; then
      echo 'ROLLBACK FAILED: inspect the dedicated stg service immediately.' >&2
      result=1
    fi
  fi
  if [[ $candidate_created == true ]]; then
    if ! cloud_run services update-traffic "$service" --remove-tags="$tag"; then
      echo "Failed to remove this run's candidate tag: $tag" >&2
      result=1
    fi
  fi
  rm -rf "$work"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Refuse to create a service or replace its IaC-owned runtime configuration.
cloud_run services describe "$service" --format=json > "$work/before.json"
mapfile -t before < <(python3 - "$work/before.json" "$media_origin" <<'PY'
import json, sys
from urllib.parse import urlsplit
doc = json.load(open(sys.argv[1]))
status = doc.get('status', {})
url = status.get('url', '')
u = urlsplit(url)
if u.scheme != 'https' or not u.netloc.endswith('.run.app') or u.path:
    raise SystemExit('Expected a pre-existing HTTPS run.app service.')
containers = doc.get('spec', {}).get('template', {}).get('spec', {}).get('containers', [])
if len(containers) != 1:
    raise SystemExit('Expected one preconfigured application container.')
args = containers[0].get('args', [])
for flag in ('-ui-root', '-catalog'):
    if flag not in args or args.index(flag) + 1 >= len(args):
        raise SystemExit('Missing IaC-owned runtime argument: ' + flag)
if '-asset-origin' not in args or args[args.index('-asset-origin') + 1] != sys.argv[2] or '-asset-root' in args:
    raise SystemExit('The existing service does not advertise the approved external media origin.')
traffic = {}
for target in status.get('traffic', []):
    percent = target.get('percent', 0)
    if percent:
        revision = target.get('revisionName') or (status.get('latestReadyRevisionName') if target.get('latestRevision') else None)
        if not revision:
            raise SystemExit('Cannot resolve the existing traffic allocation.')
        traffic[revision] = traffic.get(revision, 0) + percent
if sum(traffic.values()) != 100:
    raise SystemExit('A healthy, pre-existing 100% traffic allocation is required for rollback.')
print(url)
print(','.join(f'{revision}={percent}' for revision, percent in sorted(traffic.items())))
PY
)
[[ ${#before[@]} == 2 ]] || { echo 'Existing service validation failed.' >&2; exit 1; }
readonly service_url=${before[0]}
previous_traffic=${before[1]}
check_deadline
# Let Cloud Run name the revision. A tag on this update makes gcloud persist an
# explicit template revision, causing drift on the next Terraform runtime edit.
cloud_run services update "$service" --image="$STG_IMAGE" --no-traffic --format=json > "$work/candidate.json"
revision=$(python3 - "$work/candidate.json" "$service" "$STG_IMAGE" <<'PY'
import json, re, sys
doc = json.load(open(sys.argv[1]))
revision = doc.get('status', {}).get('latestCreatedRevisionName', '')
containers = doc.get('spec', {}).get('template', {}).get('spec', {}).get('containers', [])
if not re.fullmatch(re.escape(sys.argv[2]) + r'-[a-z0-9-]+', revision) or len(revision) > 63:
    raise SystemExit('The update did not return a valid candidate revision.')
if len(containers) != 1 or containers[0].get('image') != sys.argv[3]:
    raise SystemExit('The candidate image does not match the requested digest.')
print(revision)
PY
)
readonly revision
# Set before the call: an interrupted request might have created the tag.
candidate_created=true
cloud_run services update-traffic "$service" --update-tags="$tag=$revision" --format=json > /dev/null
cloud_run services describe "$service" --format=json > "$work/ready.json"
candidate_url=$(python3 - "$work/ready.json" "$tag" "$revision" <<'PY'
import json, sys
from urllib.parse import urlsplit
doc = json.load(open(sys.argv[1]))
for target in doc.get('status', {}).get('traffic', []):
    if target.get('tag') == sys.argv[2] and target.get('revisionName') == sys.argv[3]:
        url = target.get('url', '')
        u = urlsplit(url)
        if u.scheme == 'https' and u.netloc.endswith('.run.app') and not u.path:
            print(url)
            break
else:
    raise SystemExit('The expected ready candidate revision/tag was not found.')
PY
)
[[ -n $candidate_url ]] || { echo 'Candidate URL missing.' >&2; exit 1; }
bash "$script_dir/check-deployment.sh" "$candidate_url"
check_deadline
promotion_attempted=true
cloud_run services update-traffic "$service" --to-revisions="$revision=100"
bash "$script_dir/check-deployment.sh" "$service_url"
bash "$script_dir/check-deployment.sh" "$app_origin"
if [[ -n ${GITHUB_STEP_SUMMARY:-} ]]; then
  printf 'Deployed %s\n\nRevision: %s\n\nURL: %s\n\nCanonical URL: %s\n' "$STG_IMAGE" "$revision" "$service_url" "$app_origin" >> "$GITHUB_STEP_SUMMARY"
fi
printf 'Verified stg revision %s at %s and %s\n' "$revision" "$service_url" "$app_origin"
