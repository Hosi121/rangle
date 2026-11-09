# Asset publication preflight

`go run ./cmd/rangle-publish` verifies a candidate catalog v1, an operator approval ledger, and an exact public asset root. It writes a verification receipt, never uploads assets or changes the active catalog. The existing catalog loader and transport manifest validation remain unchanged.

## Inputs and approval ledger

Keep the candidate catalog, ledger, receipt, research inputs, weights, and private evaluation records **outside** the public root. Freeze the inputs during verification. Each new catalog entry needs one ledger row for its exact `clip_id` and `revision`:

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

The example is a template: `files` must contain actual size/SHA256 proofs for **every** public file, including the standalone `/manifests/{clip_id}/{revision}.json`. It accepts only `bytes` and `sha256` per proof. Both arrays are required. Unknown fields, duplicate JSON keys, duplicate rows, missing permission/provenance fields, and any status other than `approved` fail verification. `caveat` may be empty; source and license need explicit HTTPS URLs. The gate records operator assertions; it cannot establish legal permission or turn an unconfirmed condition into approval.

The approved attribution must appear verbatim in the catalog description and include the license. Scene and metrics source/license must match the ledger. For the existing VRU scope, retain the [documented attribution](../apps/web/assets/samples/ATTRIBUTION.md) and the supplied-calibration caveat in the operator's ledger; do not invent a separate archive license or a user's personal review.

New revisions currently support one `full` representation with codec `rangle-temporal-rgs1`. Its root allowlist is `model.rgs`, `scene.json`, `metrics.json`, `preview.webm`, optional `heldout-<view>-frame-<time>.webp`, and the standalone manifest. All bundle files live under `/assets/{clip_id}/{revision}/full/`. Undeclared files, symlinks, NPZ inputs, checkpoints, and weights fail even when an approval row tries to declare them. Adding another publication format requires an explicit verifier change.

An unchanged entry may instead have a `retained` row:

```json
{"clip_id":"basketball","revision":"baseline6000-reference270","actor":"codex","basis":"Retain the pinned previous entry unchanged; asset and permission review are not repeated in this run."}
```

Retention requires `-previous-catalog` and its exact file-body `-previous-sha256`. The normalized entry must match that catalog completely. The receipt records the previous body hash, entry content hash, actor/basis, and `validation: pinned-previous-only`; retained assets are neither fetched nor claimed as newly approved/verified. The previous catalog is an explicit trust input, not permission inferred from an arbitrary URL. A changed entry using a revision already in that previous catalog is rejected. Removed entries are recorded for rollback/audit.

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

1. Prepare the frozen allowlisted root and ledger. Run the local gate before uploading any public bytes. Upload assets to new immutable revision keys with create-only preconditions; resume an interrupted upload only by comparing existing bytes. Upload the matching standalone manifest after its asset files. Keep the candidate catalog unadvertised. The provider adapter below connects these steps.
2. Run preflight with `-remote`. Every new file must match its local and remote byte size/SHA256. The standalone manifest must equal the embedded catalog manifest. Scene, RGS1, metrics, preview and transport must identify the same revision and agree on time. The gate verifies packed RGS1's bounded gzip decode, column transpose, raw proof, header and primitive values. It checks WebM's bounded EBML/video container, duration, unlaced frame count/timestamps and first keyframe, plus held-out WebP headers; it does not decode every preview/image frame or judge reconstruction quality. A 20-sample scene at 25 fps ends at 0.76 s; its 20-frame preview has a nominal 0.8 s support interval.
3. Only a successful **`local-and-remote`** receipt authorizes the operator to proceed with the separately approved promotion. Recompute the candidate file SHA256 and require the receipt's `catalog_sha256`; archive that exact catalog, ledger, receipt and verifier source commit. Point the existing configuration/deployment process at that content-addressed catalog. `local` receipts are offline checks and do not establish remote availability. Any failure leaves the current catalog pointer unchanged.

The current paired RGS1 bundles use one clip-local time origin: preview frame `i` and GS sample `i` come from the same selected source frame and both represent `i / fps`. Selecting a later interval in the source video does not retain that global offset in either local clock. Their parent playback state must clamp transfers to the GS sample range `[0, (N-1)/fps]`; the preview support interval `[0, N/fps)` is longer by one sample interval. Unrelated preview/scene pairs require an explicitly verified binding and origin mapping; neither titles nor URL similarity establish alignment. The existing batch producer builds both from the same prepared image tensor.

Remote verification uses HTTPS on port 443 at one fixed origin, exact paths without queries, no redirects, no proxies, public DNS/IP checks before dialing the resolved IP, and bounded GETs. It requires the expected Content-Type, identity/no HTTP Content-Encoding, and token-parsed public cacheability with a positive `max-age` or `s-maxage`; `private`, `no-store`, and `no-cache` are rejected. The CDN's `public,max-age=86400` rewrite is accepted; an HTTP `immutable` hint is optional. Model bytes remain immutable by key and proof.

The default total deadline is 15 minutes (maximum one hour); a GET is limited to 30 seconds. Bounds are 256 catalog entries, 64 files per new revision, 4096 files / 512 MiB total, RGS1 32 MiB, scene 64 KiB, metrics 256 KiB, preview 16 MiB, and each held-out image 4 MiB. Verification is sequential. Resource estimates in manifests retain their declared/calculated meaning; passing this gate makes no mobile, ABR, or 100k-user performance claim.

Receipts are deterministic and created atomically outside the public root. An identical rerun verifies all new bytes again and may reuse the identical receipt; a different receipt at the same output path is refused. Failure produces no success result or new receipt and never overwrites a previous receipt. An old receipt is usable only with its matching catalog/ledger/origin/scope; its existence alone is insufficient.

## Withdrawal, rollback and remaining integration

To withdraw a clip, prepare a catalog omitting it, verify that snapshot, and promote it through the same operator process. Catalog removal does not revoke known media URLs or purge CDN caches. Cache retention follows the actual response TTL; the existing immutable origin objects can outlive the catalog. For a rights-driven stop, the operator must separately decide origin deletion/serving denial and CDN invalidation, preserve private approval/receipt records under the applicable retention policy, and check that known URLs stop serving. This CLI does not perform those actions or invent a retention deadline.

Rollback uses the archived exact previous catalog body and hash, with a fresh availability check or an explicitly recorded pinned-previous trust decision before switching the configuration back. Keep the prior immutable assets for the agreed rollback period. This verifier rejects mutation of a revision present in its previous input; global historical key uniqueness and create-only upload enforcement remain the uploader's responsibility.

This is the finite metadata/asset gate for [#22](https://github.com/Hosi121/rangle/issues/22) and [#25](https://github.com/Hosi121/rangle/issues/25). It does not scan a production container image, enumerate unrelated bucket objects, inspect every `/demo` dependency, or make the adapter mandatory for all release paths. Release-artifact allowlists and enforced release-path adoption remain acceptance work; these issues must not be closed solely because an offline preflight passes.

## Finite GCS provider adapter

Build the reviewed gate with `go build -o /private/rangle-publish ./cmd/rangle-publish`, then run:

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

The default is a local-gate dry run: no credential command or cloud API call, and no upload receipt is written. Add **`--execute`** only for an already authorized publication. The total deadline defaults to 1800 seconds, capped at 3600. Execution first passes the local Go gate, then obtains `gcloud auth print-access-token --account=...` into RAM. It verifies the active project's numeric identity and both buckets' project, Tokyo location, `application=rangle` / requested environment labels, uniform access, and enforced public access prevention. Missing or mismatched scope fails before the first write. No IAM, Cloud Run, active catalog selection, object update/delete, or GPU API is available in the adapter.

All media data precedes scene files, which precede transport manifests. Inserts use [`ifGenerationMatch=0`](https://docs.cloud.google.com/storage/docs/json_api/v1/objects/insert); a 412 is resumable only when size, SHA256 metadata, actual GCS MD5, content type/cache policy, absent encoding, and a valid generation match the local bytes. `rangle_source` is an ownership hint, not approval proof; a matching existing `local-batch-20261109` marker is left unchanged. Every uploaded body is rehashed after the local gate. There are no automatic cloud retries; interruption stops safely and the same frozen inputs can be rerun.

Only after the complete remote Go gate passes does the adapter create private `approvals/<catalogsha>.json`, `receipts/<catalogsha>.json`, and finally `snapshots/<catalogsha>.json` in that order, all create-only with `private,no-store`. Different existing content fails; identical content resumes. The private local upload receipt records bucket/key/generation/size/SHA256/MD5 and verified catalog/ledger/remote-receipt hashes, never tokens or authenticated response bodies. Its creation is atomic and refuses different existing output. Temporary gate receipts are removed on success or failure.

Pass the resulting verified SHA to the existing reviewed Terraform/CI deployment path. Check the candidate response header's catalog snapshot before selecting it; this adapter cannot change the active configuration. Archive the deployment selection and prior SHA for rollback. `python3 scripts/publish-catalog.test.py` runs cloud-stub interruption/resume and failure tests in the existing delivery CI job; real project execution and deployment verification remain operator evidence.
