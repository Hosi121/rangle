# CI/CD

GitHub Actionsで検証し、`stg` の変更をCloud Run `rangle-stg`（`comin-up` / `asia-northeast1`）へ配備する。

## workflow

| workflow | 実行条件・内容 |
| --- | --- |
| `ci.yml` → `validate.yml` | `stg` / `main` 宛PRでGo race/vet、viewer・Flutterの検証、配備スクリプトのテスト、Terraformの静的・mock検証 |
| `docker-build.yml` | 同じPRでDocker imageのbuildを確認 |
| `deploy-stg.yml` | `stg` へのpush、または `stg` を指定した手動実行で検証・build・配備 |

Go＋Flutter Webをマルチステージで1つの非root imageにまとめ、digestを指定して配備する。依存・build cacheを再利用し、デプロイは直列実行する。認証にはWIFを使う。

## 配備の流れ

1. 実行元・設定・期限を確認し、共通の検証を通す。
2. imageをbuild/pushし、通常trafficを流さず候補revisionを作る。
3. 候補のtag付き `run.app` でhealth・UI・APIと公開メディアURLを確認する。
4. 候補へtrafficを100%切り替え、通常の `run.app` と `https://app.<domain>` をHTTPSで再確認する。

昇格時・昇格後の確認に失敗したら、以前のrevisionとtraffic比率へ戻す。正常終了・エラー時とも、その実行で作った候補タグの削除を試みる。

フィード先頭は通常catalogのクリップと、掲載済みの生成結果の両方を受け付ける。生成結果はtransport codec/manifestを持たない契約のまま、ID・revision・固定scene pathとmedia originを照合する。不完全なtransport情報や別jobのsceneは昇格を止める。この確認はHTTPの接続・metadata検査で、ブラウザ描画や実機性能の検証とは別。

## 初期設定

正常に動くCloud Run、公開catalog・メディア、app/mediaのDNS・HTTPSを先に用意し、GitHub repository variablesを設定する。

| 変数 | 内容 |
| --- | --- |
| `GCP_WORKLOAD_IDENTITY_PROVIDER` | IaCの `deployment.workload_identity_provider` |
| `GCP_DEPLOY_SERVICE_ACCOUNT` | IaCの `deployment.deploy_service_account` |
| `RANGLE_STG_DOMAIN` | 取得したbase domain（scheme・pathなし） |

CIはimageとtrafficを更新する。インフラ・IAM・catalog・メディアの管理はIaCと公開工程が担当する。配信先は `app.<domain>` と `media.<domain>`。

revision名はCloud Runに生成させ、候補tagを別操作で付ける。同時指定するとTerraformにrevisionの差分が残る [providerの既知問題](https://github.com/hashicorp/terraform-provider-google/issues/17218) を避け、IaCによるruntime設定の変更を維持する。

新規build・配備・昇格の期限は **2026-11-15 00:00 JST**。稼働資源の停止・予算監視は別のジョブが担当する。構成・権限・停止条件は [stg配備手順](stg-deployment.md) を参照。
