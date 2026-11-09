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

CI deployはruntime argsの `/mnt/catalog/snapshots/<64hex>.json` から期待する静的catalog SHAを取り、候補・通常service・公開appの3つのprobeへ必ず渡す。APIの `X-Rangle-Catalog-SHA256` が一致しなければ、候補では昇格を止め、公開確認では元のtraffic配分へ戻す。生成結果が先頭でも、このheaderはbase static catalogの元JSON bytesを識別する。`check-deployment.sh` 単体では `EXPECTED_CATALOG_SHA256` の指定を任意とし、既存の呼出しも使える。

## release receipt

正常な公開確認の後、sourceの40桁commit SHA、run ID/attempt、候補revision・immutable image digest、静的catalogファイルSHA、generation config SHA（設定時）、asset origin、元のtraffic配分と各復帰先revisionの実configを、小さいJSONへ記録する。`schema_version` は1、最大64KiBとする。

前版は `services describe.spec.template` から取らない。Terraformによるruntime変更後、このdesired templateはまだ通常trafficを受けていない新configを指すことがある。配備前の `status.traffic` で実際に配信するrevisionを確定し、各revisionをread-onlyの `revisions describe` で取得して、`revision.spec.containers` の既知fieldだけを記録する。前版のimageがtagならその実値を残し、`image_digest` はnullとする。古いcatalog pathなどからSHAを確認できない場合もnullとし、desired templateの値で埋めない。

候補のtarget argsとしてのみ配備前のdesired templateを使う。公開確認後、trafficが候補100%であることと、実際の候補revisionのimmutable image・argsが要求と一致することを照合してからreceiptを生成する。新targetのcatalog SHAは必須で、旧版の未知値とは区別する。sourceのremote origin、args全量、env、credentials、GCS input URI、巨大なservice JSONはreceiptやSummaryへ含めない。

必須のローカル処理と外部保存を分ける。

| 処理 | 保存先・失敗時の扱い |
| --- | --- |
| JSON生成・検証とSummary追記 | `${RUNNER_TEMP}/rangle-release-receipt.json` とGitHub Step Summaryへ同じJSONを残す。上書きせず、途中ファイルを公開しない。公開後の生成・検証・Summary書込失敗は既存trapで元trafficへrollbackする |
| artifact保存 | `stg-release-<run_id>-<attempt>`、retention 7日、overwrite false。upload対象は上記の単一明示pathだけ。隠しdirectoryやrunner temp全体を渡さず、ファイル不在はupload errorにする |
| artifactサービス等のupload失敗 | 検証済み配備は維持し、Summaryへ保存失敗を表示する。JSONは既にSummaryへ記録してあるため、そこから回収する。外部保存障害だけでは正常配備をrollbackしない |

guard・期限・候補no-traffic・実instance設定・元trafficの厳密復帰・自分の候補tagだけを消す契約は維持する。receiptの保存やartifact uploadにGCP/IAM設定を追加しない。artifact保持期限後の長期保存や、mainへのdigest昇格は別の判断であり、この記録だけで完了したとは扱わない。

ローカルの `python3 scripts/deploy-stg.test.py` はgcloud/curl/dateをstubに置き換え、Cloud APIへ接続しない。desired templateと実配信revisionが異なるケース、未知の旧版field、秘密の非export、候補/公開catalog SHA不一致、候補100%・image/args不一致、receipt/Summary書込失敗、partial promotionのrollbackを確認する。通常catalog・generated-firstの両方を維持する。100k性能や少量trafficの合否、実ブラウザ描画を測るテストではない。

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
