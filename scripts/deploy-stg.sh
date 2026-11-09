#!/usr/bin/env bash
# Only update the image/traffic of the pre-existing, dedicated stg service.
set -Eeuo pipefail
set +x
manager_id_token=${GENERATION_MANAGER_ID_TOKEN:-}
unset GENERATION_MANAGER_ID_TOKEN
readonly project=comin-up region=asia-northeast1 service=rangle-stg
readonly manager_service=rangle-stg-generation manager_tag=ci-manager-candidate
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
[[ -n ${RUNNER_TEMP:-} && -d $RUNNER_TEMP && -n ${GITHUB_STEP_SUMMARY:-} && ${RELEASE_RECEIPT_PATH:-} == "$RUNNER_TEMP/rangle-release-receipt.json" ]] || {
  echo 'Set the dedicated receipt path in RUNNER_TEMP and the Actions summary path.' >&2; exit 1;
}
readonly receipt_path=$RELEASE_RECEIPT_PATH
[[ ! -e $receipt_path && ! -L $receipt_path ]] || { echo 'Release receipt already exists.' >&2; exit 1; }
# Reserved by the serialized deploy-rangle-stg workflow and the IaC-owned NEG.
readonly tag=ci-candidate
work=$(mktemp -d)
promotion_attempted=false
candidate_created=false
manager_candidate_created=false
manager_promotion_attempted=false
previous_traffic=''
manager_previous_traffic=''
cloud_run() { gcloud run "$@" --project="$project" --region="$region" --quiet; }
expect_direct_rejected() {
  local status
  status=$(curl --silent --show-error --proto '=https' --connect-timeout 5 --max-time 20 \
    --output /dev/null --write-out '%{http_code}' "$1/health") || return
  [[ $status == 404 ]] || {
    echo "Public run.app ingress was not rejected (HTTP $status)." >&2; return 1;
  }
}
probe_candidate() {
  local attempt
  # Tag routing can take time to converge. A stale revision with the same
  # catalog must fail HTTP revision verification, never pass via the default.
  for attempt in 1 2 3 4 5; do
    if EXPECTED_CATALOG_SHA256="$expected_catalog_sha" EXPECTED_REVISION="$revision" \
      RANGLE_CANDIDATE_PROBE="$tag" bash "$script_dir/check-deployment.sh" "$app_origin"; then
      return 0
    fi
    [[ $attempt != 5 ]] || return 1
    check_deadline || return
    sleep 2
  done
}
cleanup() {
  local result=$?
  trap - EXIT
  if [[ $candidate_created == true ]]; then
    if ! cloud_run services update-traffic "$service" --remove-tags="$tag"; then
      echo "Failed to remove this run's candidate tag: $tag" >&2
      result=1
    fi
  fi
  if [[ $manager_candidate_created == true ]]; then
    if ! cloud_run services update-traffic "$manager_service" --remove-tags="$manager_tag" > /dev/null; then
      echo "Failed to remove this run's manager candidate tag: $manager_tag" >&2
      result=1
    fi
  fi
  if [[ $result != 0 && ( $promotion_attempted == true || $manager_promotion_attempted == true ) ]]; then
    echo 'Promotion failed; restoring the previous explicit revision allocation.' >&2
    if ! cloud_run services update-traffic "$service" --to-revisions="$previous_traffic"; then
      echo 'ROLLBACK FAILED: inspect the dedicated stg service immediately.' >&2
      result=1
    fi
  fi
  # Each restoration is attempted independently, including after a failed
  # public rollback or an interrupted promotion/cleanup request.
  if [[ $result != 0 && $manager_promotion_attempted == true ]]; then
    if ! cloud_run services update-traffic "$manager_service" --to-revisions="$manager_previous_traffic" > /dev/null; then
      echo 'MANAGER ROLLBACK FAILED: inspect the dedicated generation service immediately.' >&2
      result=1
    fi
  fi
  if [[ $result != 0 && -f $receipt_path ]]; then
    rm -f "$receipt_path"
  fi
  rm -rf "$work"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Refuse to create a service or replace its IaC-owned runtime configuration.
cloud_run services describe "$service" --format=json > "$work/before.json"
mapfile -t before < <(python3 "$script_dir/release-receipt.py" plan "$work/before.json" "$media_origin")
[[ ${#before[@]} == 3 ]] || { echo 'Existing service validation failed.' >&2; exit 1; }
readonly service_url=${before[0]} expected_catalog_sha=${before[2]}
previous_traffic=${before[1]}
mkdir "$work/previous"
IFS=',' read -r -a previous_allocations <<< "$previous_traffic"
for allocation in "${previous_allocations[@]}"; do
  previous_revision=${allocation%=*}
  cloud_run revisions describe "$previous_revision" --format=json > "$work/previous/$previous_revision.json"
done
# Verify actual served revisions before making changes. The desired service
# template can already contain a different Terraform-selected catalog.
python3 "$script_dir/release-receipt.py" plan "$work/before.json" "$media_origin" "$work/previous" > /dev/null
generation_mode=$(python3 "$script_dir/release-receipt.py" manager-mode "$work/before.json")
if [[ $generation_mode == remote ]]; then
  [[ -n $manager_id_token && $manager_id_token != *$'\n'* && $manager_id_token != *$'\r'* ]] || {
    echo 'The private generation manager requires a freshly minted deployment ID token.' >&2; exit 1;
  }
  mkdir -p "$work/manager/previous"
  # Read-only validation happens before either service is updated. Never create
  # a missing manager, change IAM, or fall back to the legacy execution mode.
  cloud_run services describe "$manager_service" --format=json > "$work/manager/before.json"
  cloud_run services get-iam-policy "$manager_service" --format=json > "$work/manager/policy.json"
  mapfile -t manager_before < <(python3 "$script_dir/release-receipt.py" manager-plan \
    "$work/before.json" "$work/manager/before.json" "$work/manager/policy.json")
  [[ ${#manager_before[@]} == 3 ]] || { echo 'Existing manager validation failed.' >&2; exit 1; }
  manager_previous_traffic=${manager_before[1]}
  IFS=',' read -r -a manager_previous_allocations <<< "$manager_previous_traffic"
  for allocation in "${manager_previous_allocations[@]}"; do
    previous_revision=${allocation%=*}
    cloud_run revisions describe "$previous_revision" --format=json > "$work/manager/previous/$previous_revision.json"
  done
  python3 "$script_dir/release-receipt.py" manager-plan "$work/before.json" \
    "$work/manager/before.json" "$work/manager/policy.json" "$work/manager/previous" > /dev/null
fi
expect_direct_rejected "$service_url"
check_deadline
if [[ $generation_mode == remote ]]; then
  cloud_run services update "$manager_service" --image="$STG_IMAGE" --no-traffic --format=json > "$work/manager/candidate.json"
  manager_revision=$(python3 "$script_dir/release-receipt.py" manager-candidate \
    "$work/manager/candidate.json" "$work/manager/before.json")
  manager_candidate_created=true
  cloud_run services update-traffic "$manager_service" --update-tags="$manager_tag=$manager_revision" --format=json > /dev/null
  cloud_run services describe "$manager_service" --format=json > "$work/manager/ready.json"
  manager_candidate_url=$(python3 "$script_dir/release-receipt.py" manager-tag "$work/manager/ready.json" "$manager_revision")
  manager_ready=false
  for attempt in 1 2 3 4 5; do
    # Curl reads the header on stdin. Neither argv nor subprocess environments
    # contain this credential, and raw service documents never enter receipts.
    if printf 'Authorization: Bearer %s\n' "$manager_id_token" | \
      EXPECTED_REVISION="$manager_revision" bash "$script_dir/check-deployment.sh" manager "$manager_candidate_url"; then
      manager_ready=true
      break
    fi
    [[ $attempt != 5 ]] || break
    check_deadline
    sleep 2
  done
  [[ $manager_ready == true ]] || { echo 'Generation manager candidate did not become healthy.' >&2; exit 1; }
  check_deadline
  manager_promotion_attempted=true
  cloud_run services update-traffic "$manager_service" --to-revisions="$manager_revision=100" > /dev/null
  cloud_run services describe "$manager_service" --format=json > "$work/manager/after.json"
  cloud_run revisions describe "$manager_revision" --format=json > "$work/manager/verified-revision.json"
  python3 "$script_dir/release-receipt.py" manager-verify "$work/manager" "$work/before.json"
fi
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
expect_direct_rejected "$candidate_url"
probe_candidate
check_deadline
promotion_attempted=true
cloud_run services update-traffic "$service" --to-revisions="$revision=100"
EXPECTED_CATALOG_SHA256="$expected_catalog_sha" EXPECTED_REVISION="$revision" \
  RANGLE_CANDIDATE_PROBE='' bash "$script_dir/check-deployment.sh" "$app_origin"
cloud_run services describe "$service" --format=json > "$work/after.json"
cloud_run revisions describe "$revision" --format=json > "$work/verified-revision.json"
manager_receipt_args=()
if [[ $generation_mode == remote ]]; then
  manager_receipt_args=("$work/manager")
fi
python3 "$script_dir/release-receipt.py" write "$work/before.json" "$work/previous" \
  "$work/after.json" "$work/verified-revision.json" "$revision" "$media_origin" "$receipt_path" "${manager_receipt_args[@]}"
printf 'Verified stg revision %s through %s; public run.app ingress rejected.\n' "$revision" "$app_origin"
