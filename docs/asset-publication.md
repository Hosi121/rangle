# Asset publication preflight

`go run ./cmd/rangle-publish` verifies catalog v1, operator approval ledger, and an exact public root, then writes a receipt. It never uploads assets or selects the active catalog. Existing catalog/transport validation is unchanged.

## Inputs and approval ledger

Freeze inputs. Keep catalog, ledger, receipt, research inputs, weights, and private evaluations **outside** the public root. Each new `clip_id/revision` needs one approval row:

```json
{
  "version": 1,
  "clips": [{
    "clip_id": "example",
    "revision": "content-revision",
    "status": "approved",
    "actor": "codex",
    "basis": "Operator records the existing approved input scope and explicit publication instruction; not a personal user review or third-party signature.",
    "source_url": "https://example.org/dataset",
    "license": "CC BY 4.0",
    "license_url": "https://creativecommons.org/licenses/by/4.0/",
    "modifications": "Selected time interval, calibrated reconstruction, and RGS1 compression.",
    "attribution": "Dataset authors / CC BY 4.0",
    "caveat": "Record limitations of the approval basis here.",
    "files": {
      "/assets/example/content-revision/full/model.rgs": {
        "bytes": 123,
        "sha256": "replace-with-the-actual-64-lowercase-hex-digest"
      }
    }
  }],
  "retained": []
}
```

This template needs actual `bytes/sha256` proofs for **every** public file, including `/manifests/{clip_id}/{revision}.json`; no other proof fields are accepted. Both arrays are required. Unknown fields, duplicate JSON keys/rows, missing provenance/permission, or status other than `approved` fail. `caveat` may be empty; source/license URLs must be HTTPS. Operator assertions do not establish legal permission or resolve unconfirmed conditions.

Copy approved attribution verbatim into the catalog description, including the license; scene/metrics source and license must match. For VRU, retain the [documented attribution](../apps/web/assets/samples/ATTRIBUTION.md) and supplied-calibration caveat. Do not invent an archive license or personal user review.

New revisions support one `full` representation, codec `rangle-temporal-rgs1`. Allowlisted bundle files are `model.rgs`, `scene.json`, `metrics.json`, `preview.webm`, optional `heldout-<view>-frame-<time>.webp` under `/assets/{clip_id}/{revision}/full/`, plus the standalone manifest. Undeclared files, symlinks, NPZ, checkpoints, and weights fail even if declared in the ledger. Other formats require a verifier change.

Unchanged entries may use `retained`:

```json
{"clip_id":"basketball","revision":"baseline6000-reference270","actor":"codex","basis":"Retain the pinned previous entry unchanged; asset and permission review are not repeated in this run."}
```

Retention requires `-previous-catalog` and exact body `-previous-sha256`, with a completely matching normalized entry. The receipt records previous-body/entry hashes, actor/basis, and `validation: pinned-previous-only`. Retained assets are not fetched or newly approved. The previous catalog is an explicit trust input. Changed entries cannot reuse its revisions; removals are recorded.

## Run before promotion

```bash
go run ./cmd/rangle-publish \
  -catalog /private/candidate-catalog.json \
  -approvals /private/approvals.json \
  -root /staging/public \
  -origin https://media.rangleplay.stream \
  -previous-catalog /private/previous-catalog.json \
  -previous-sha256 <exact-previous-file-sha256> \
  -receipt /private/verification-receipt.json \
  -remote
```

1. Pass the local gate **before** uploading. Upload frozen assets to new immutable keys with create-only preconditions; resume only after byte comparison. Upload the standalone manifest last and keep the candidate catalog unadvertised.
2. Pass `-remote`: every new local/remote file must match size/SHA256; standalone/embedded manifests must match. Scene/RGS1/metrics/preview/transport must agree on revision/time. The gate checks bounded RGS1 gzip/transpose/raw proof/header/primitive values, bounded WebM EBML/video/duration/unlaced frame timestamps/count/first keyframe, and held-out WebP headers. It does not decode every image/frame or judge reconstruction quality.
3. Only a successful **`local-and-remote`** receipt permits separately approved promotion. Recompute the catalog file SHA and match `catalog_sha256`; archive exact catalog, ledger, receipt, and verifier commit. Select that content-addressed catalog through the existing deployment process. A `local` receipt proves no remote availability; failure leaves the active pointer unchanged.

Paired RGS1 bundles use clip-local time: preview frame/sample `i` both represent `i/fps`, even when selecting a later source interval. Clamp playback transfers to `[0,(N-1)/fps]`; preview support is `[0,N/fps)`. Thus 20 samples at25fps end at0.76s, while preview support is0.8s. The batch producer uses the same prepared tensor. Other pairs require verified binding/origin mapping; title/URL similarity is insufficient.

| Remote rule | Contract |
| --- | --- |
| Network | One fixed HTTPS origin, port443, exact paths, no query/redirect/proxy. Public DNS/IP checks before dialing resolved IP |
| Response | Expected Content-Type, identity/no HTTP Content-Encoding, token-parsed public cacheability, positive max-age or s-maxage; reject private/no-store/no-cache |
| Cache | CDN `public,max-age=86400` accepted; immutable hint optional. Keys/proofs keep bytes immutable |
| Time | Total15min, maximum1h; GET30s; sequential verification |
| Bounds | 256 entries;64 files/new revision;4096 files/512MiB total; RGS1 32MiB;scene64KiB;metrics256KiB;preview16MiB;held-out image4MiB |

Manifest resource estimates retain their declared/calculated meaning; passing makes no mobile, ABR, or100k-user claim. Receipts are deterministic, atomic, and outside the root. Identical reruns reverify bytes and may reuse identical output; differing output is refused. Failure writes no success receipt and never overwrites an old one. Existing receipts require matching catalog/ledger/origin/scope.

## Withdrawal, rollback and remaining integration

Withdrawal omits the clip from a verified/promoted catalog; known media URLs and CDN caches remain. A rights-driven stop separately requires origin denial/deletion, CDN invalidation, retention of private records under applicable policy, and verification that known URLs stop serving. The CLI does none of these and sets no retention deadline.

Rollback uses archived exact previous bytes/hash with a fresh availability check or recorded pinned-previous trust decision. Retain prior assets for the agreed period. The verifier rejects revisions changed against its previous input; historical global key uniqueness and create-only enforcement remain the uploader's responsibility.

The asset → manifest → catalog workflow is tracked by completed [#25](https://github.com/Hosi121/rangle/issues/25). Remaining provenance and allowlist coverage across all assets and release paths is tracked by [#22](https://github.com/Hosi121/rangle/issues/22). Production image scans, unrelated bucket enumeration, all `/demo` dependencies, and enforced adoption across release paths remain outside this gate. Offline success alone does not establish that broader coverage.

## Finite GCS provider adapter

Build with `go build -o /private/rangle-publish ./cmd/rangle-publish`, then run:

```bash
python3 scripts/publish-catalog.py \
  --gate /private/rangle-publish \
  --catalog /private/candidate-catalog.json --approvals /private/approvals.json \
  --root /staging/public --origin https://media.rangleplay.stream \
  --previous-catalog /private/previous-catalog.json --previous-sha256 <sha256> \
  --project comin-up --media-bucket rangle-stg-113704194486-media \
  --catalog-bucket rangle-stg-113704194486-catalog \
  --environment presentation-stg --account <explicit-account-email> \
  --output-receipt /private/upload-receipt.json
```

Default: local dry run, no credentials/cloud calls/upload receipt. Add **`--execute`** only for authorized publication. Total deadline defaults1800s, capped3600s. After the local gate, `gcloud auth print-access-token --account=...` stays in RAM. Before writes, verify numeric project identity and both buckets' project, Tokyo location, `application=rangle`/environment labels, uniform access, and enforced public access prevention. No IAM, Cloud Run, active selection, update/delete, or GPU operations are available.

Upload order is media → scene → transport manifest. Inserts use [`ifGenerationMatch=0`](https://docs.cloud.google.com/storage/docs/json_api/v1/objects/insert). A 412 resumes only if size, SHA metadata, actual GCS MD5, content type/cache policy, absent encoding, and valid generation match. `rangle_source` is an ownership hint; matching `local-batch-20261109` is unchanged. Rehash every uploaded body after the local gate. No automatic cloud retries; rerun the same frozen inputs after interruption.

After the complete remote gate, create private `approvals/<catalogsha>.json` → `receipts/<catalogsha>.json` → `snapshots/<catalogsha>.json`, all create-only, `private,no-store`. Identical content resumes; different content fails. The atomic private upload receipt records bucket/key/generation/size/SHA256/MD5 and verified catalog/ledger/remote-receipt hashes, never tokens or authenticated bodies. Different existing output is refused; temporary gate receipts are always removed.

Pass verified SHA to reviewed Terraform/CI, check the candidate's catalog header before selection, and archive selection/prior SHA. The adapter cannot activate it. `python3 scripts/publish-catalog.test.py` covers cloud-stub resume/interruption/failures in delivery CI; real execution/deployment still needs operator evidence.
