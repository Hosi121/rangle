# stg の検証と配信

Go/Web を1つの非root OCI imageにして、既存の `rangle-stg` へ配信する。対象は `comin-up`（project number `113704194486`）、`asia-northeast1`。必須変数 `RANGLE_STG_DOMAIN` から正規アプリURL `https://app.<domain>` とメディアURL `https://media.<domain>` を導出する。実ドメイン名は取得・DNS設定の確認後に設定する。

正規app hostnameはglobal HTTPS Load Balancerから同じGo/Webへ、media hostnameはCloud CDNと非公開GCSのbackend bucketへ接続する。stgではcandidate tagの直接確認用にCloud Runのdefault `run.app` URLとALL ingressを有効のまま保つ。CIはこの設定を変更しない。Firebase SDK、Auth、database、Hostingの配備は使わない。

## workflow

| workflow | 実行条件と役割 |
| --- | --- |
| `ci.yml` → `validate.yml` | stg/main宛PR。Go race/vet、Node viewer test/build、Flutter format/analyze/test、配信guard、Terraformの静的/mock検証 |
| `docker-build.yml` | stg/main宛PR。image buildだけを行い、認証・push・deployはしない |
| `deploy-stg.yml` | マージ後stgへのpush、またはstgを指定した手動実行。同じvalidationを通し、imageを1回build/pushしてcandidate確認後に昇格 |

`docker-build.yml` は yossi0413 の [PR #40](https://github.com/Hosi121/rangle/pull/40)、原作commit [`bc827b7294c5038a86e29191fa66a79cc6db0eae`](https://github.com/Hosi121/rangle/commit/bc827b7294c5038a86e29191fa66a79cc6db0eae) を引き継ぐ。既存の `rangle-docker` BuildKit cache scope と `mode=max` を維持し、actionのSHA固定とAPI向けbuild引数を加えた。

actionのSHAは2026-11-08に各所有repoのrelease/tag APIで確認した。workflow内に版を併記する。依存はnpm/pubのlockfileを使い、Goは `go.mod`、Flutterは3.47.9、Nodeは24を基準にする。PRの古い検証は取り消せるが、デプロイは同じconcurrency groupで直列化し、進行中runを自動取消ししない。

Terraformは1.17.1に固定し、`infra/gcp` 以下でlockfileを持つ各root/moduleを順番に `init -backend=false -lockfile=readonly`、`validate` する。testsがある単位だけmock testを実行する。google/google-beta両providerのlockfileをcache keyへ含め、同じplugin cacheへ並列書込をしない。OIDC、ADC、gcloud、実backend、実plan/applyは使わない。

## 有効化する前に

親のインフラ作業で対象サービス、Artifact Registry repository、runtime SA、catalog mount、公開フィード、Cloud CDNへのメディア公開と停止処理を用意する。取得したbase domainのapp/media DNSとHTTPS証明書がACTIVEになり、正規app URLでUI/APIへ到達できることを確認してからworkflowを有効化する。CIはTerraform apply、IAM変更、service作成、メディアuploadを行わない。既存サービスがない、または `-catalog` / `-ui-root` / 導出したmedia originが設定されていない場合は止まる。

GitHub repository variablesを設定する。SA keyや長期tokenは登録しない。

| 変数 | 値 |
| --- | --- |
| `GCP_WORKLOAD_IDENTITY_PROVIDER` | `projects/113704194486/locations/global/workloadIdentityPools/POOL/providers/PROVIDER` |
| `GCP_DEPLOY_SERVICE_ACCOUNT` | `rangle-stg-deploy@comin-up.iam.gserviceaccount.com` |
| `RANGLE_STG_DOMAIN` | 取得・管理するDNS base domain。例 `replay.example.test` はテスト用の架空値 |

domainにはscheme、認証情報、port、path、query、fragment、wildcard、空白・制御文字を含めない。DNS labelの長さ・形式を標準ライブラリで検証し、IP addressやlocalhost等は拒否する。ASCII大文字は小文字に正規化する。未設定・不正値はWIF認証やimage build前に失敗する。構文検証ではDNS所有権や証明書の準備完了を推測しない。`scripts/check-deployment.sh` も同じ環境変数でmedia originを検証し、`origins` サブコマンドで導出した2つのoriginだけを確認できる。

WIFは数値 `repository_id=1448803636`、`repository_owner_id=128687243`、`ref=refs/heads/stg`、`workflow_ref=Hosi121/rangle/.github/workflows/deploy-stg.yml@refs/heads/stg` とpush/workflow_dispatchへ限定する。PRにはOIDC権限を渡さない。deploy jobはGitHub Environment `stg` を使い、既存の承認・branch保護規則を維持する。変数やWIFを設定しただけで、未マージbranchのworkflowを走らせる運用にはしない。[Google WIF](https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-deployment-pipelines)

実行SAの書込先は専用Artifact Registry repositoryと既存Cloud Run serviceに限定する。runtime SAのactAsはそのSA単位とし、serviceのIAMや他サービス、入力・datasetのwriter権限を付けない。image/trafficはCI、args・mount・catalog・scale・runtime identityはIaCが担当するため、IaC側のimage/traffic driftの扱いも揃える。

## candidateからの昇格

1. 期限とrepository/ref/workflow/event、WIFの2変数とdomain変数を確認し、共通validationを実行する。
2. build直前に同じguardで期限・domain・WIF設定を再確認する。`RANGLE_API_URL=/api/v1/clips` をDart defineへ渡して1回buildし、`asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/server:FULL_COMMIT_SHA` へpushする。
3. 確定した `@sha256:...` だけを使う。build後にWIF認証を更新し、既存serviceのtrafficをrevision名と比率へ解決して保存する。SA impersonationのOAuth tokenを1時間で要求し、gcloudにはmasked step環境変数で渡す。短いGitHub OIDC assertionを長いbuild後に再利用せず、tokenをimage・ログ・成果物へ保存しない。[gcloudのtoken認証](https://docs.cloud.google.com/sdk/docs/authenticate#use_the_gcloud_cli_with_an_access_token)
4. `gcloud run services update --image=... --no-traffic` でimageのみ更新する。run ID/attempt固有のcandidate tagとrevision名を使い、他のtagには触れない。
5. direct tag付きrun.app URLでcandidateの `/healthz`、`/`、`/demo/`、`/api/v1/clips?limit=1` を有界に確認する。APIは非空の公開clipと `https://media.<domain>` のmanifest/preview/scene URLを要求し、synthetic fixtureやHTML fallbackを拒否する。研究素材の取得・転送はしない。
6. 昇格直前に期限を確認し、そのrevisionへ100%を割り当てる。通常のrun.app service URLと正規 `https://app.<domain>` の両方で同じsmokeを行い、TLS検証を省略しない。
7. 昇格要求や昇格後のいずれかのsmoke（正規app hostnameのTLS失敗も含む）が失敗したら、以前の明示revision比率へ戻す。candidate確認だけの失敗では本trafficを変えない。正常・失敗ともそのrunのcandidate tagを除く。

rollbackは以前の100%または分割比率を復元し、`LATEST=100` へ戻さない。初回は親の配備で正常なbaseline revisionを用意する。rollback/cleanupの失敗はCIに失敗として残す。手動cancelやrunner喪失ではcleanup完了を保証できないため、serviceのtraffic/tagと期限停止を確認する。[Cloud Run traffic操作](https://docs.cloud.google.com/sdk/gcloud/reference/run/services/update-traffic)、[既存serviceの更新](https://docs.cloud.google.com/sdk/gcloud/reference/run/services/update)

## 期限・費用

新しいbuild/deploy/昇格の期限は **2026-11-14 15:00:00 UTC**（日本時間11月15日00:00）。guardはそれ以降を拒否する。失敗からのrollbackとtag cleanupは期限後も試みる。

このguardは稼働中のserviceや配信入口を自動停止しない。親の運用で同期限に専用deploy SA、対象Cloud Run、専用app/mediaの配信入口を止め、課金resourceの片付けを実装・確認する。利用許可は全サービス合計USD300までで、親の運用上限JPY30,000・早期停止JPY15,000も守る。CIのmax instancesや期限checkだけでは請求上限を保証できない。既存Cloud RunのJPY200制限を解除しない。画像・catalogは公開許諾済みのものに限定し、私有UVGや `.local` の研究成果をpublishしない。

## ローカル検証

```sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
bash -n scripts/deploy-stg.sh scripts/check-deployment.sh
shellcheck scripts/deploy-stg.sh scripts/check-deployment.sh
python3 scripts/deploy-stg.test.py
docker build --build-arg RANGLE_API_URL=/api/v1/clips -t rangle:local .
```

配信guardのtestはstub gcloud/curl/dateだけを使い、GCPへ接続しない。candidate失敗、期限切れ、不正domainの早期拒否、部分的な昇格失敗、通常service/正規appの失敗時のrollbackを検証する。コンテナの確認と実Cloud Run/WIF/DNS/TLS/環境保護の確認は区別する。後者は許可された実配備で確認する。
