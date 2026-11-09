# CI/CD

GitHub Actionsで検証し、`stg` の変更を公開Cloud Run `rangle-stg` と、remote生成有効時のprivate manager `rangle-stg-generation`（`comin-up` / `asia-northeast1`）へ配備する。

## workflow

| workflow | 実行条件・内容 |
| --- | --- |
| `ci.yml` → `validate.yml` | `stg` / `main` 宛PRでGo race/vet、viewer・Flutter、配備・台帳移行・cloud metadata checkerのofflineテスト、Terraformの静的・mock検証とArmor regex検査 |
| `docker-build.yml` | 同じPRでDocker imageのbuildを確認 |
| `deploy-stg.yml` | `stg` へのpush、または `stg` を指定した手動実行で検証・build・配備 |

Go＋Flutter Webをマルチステージで1つの非root imageにまとめ、digestを指定して配備する。依存・build cacheを再利用し、デプロイは直列実行する。認証にはWIFを使う。

## 配備の流れ

1. 実行元・設定・期限を確認し、共通の検証を通す。
2. imageをbuild/pushする。公開desired templateの `-generation-manager-url` がある場合は、既存managerのSA、IAM、固定args、config/catalog SHA path、pinned imageと両serviceの実traffic配分・復帰先revisionをread-onlyで確認する。manager欠落や不一致は、どちらのserviceも更新する前に失敗させる。
3. remote構成では、同じimageでmanagerをno-traffic更新し、`ci-manager-candidate` tagへ新しいID token付きで `/health` を呼ぶ。HTTP 200・health本文・`K_REVISION`由来headerを照合してからmanagerを100%へ昇格し、実revisionのimage/argsとtrafficを再確認する。
4. 公開アプリをno-traffic更新する。公開serviceの既定 `run.app` と候補tag付きURLの外部直通拒否を確認し、固定tag `ci-candidate` に対応するLB経路を検証する。`https://app.<domain>` に `X-Rangle-Candidate: ci-candidate` を付け、候補のhealth・UI・APIと公開メディアURLを確認する。
5. 公開候補へtrafficを100%切り替え、候補指定headerなしで `https://app.<domain>` をHTTPSで再確認する。

manager昇格後の公開候補・公開後確認・receipt保存・tag cleanupに失敗した場合は、両serviceをそれぞれ以前の明示revision/traffic比率へ戻す。一方の復帰失敗でも他方を試みる。正常終了・エラー時とも、その実行で作った2つの候補tagだけを削除し、失敗したreleaseのreceiptファイルは残さない。DB・IAM・attempt counterはこの復帰処理の対象外である。

managerへのID tokenはbuild後にWIFで発行し、audienceを `https://rangle-stg-generation-113704194486.asia-northeast1.run.app` に固定する。email claimを含め、deploy scriptへだけ渡す。scriptはexportを解除してcurlのheaderをstdinで渡し、argv・子process環境・ログ・receiptへtokenを含めない。deployerにはmanagerのinvokeとimage/traffic更新を許可するが、managerの独立したidentity検証は業務APIの利用を拒否する。CIはIAM・network・databaseを作成・変更しない。

初回bootstrapでは、公開templateが従来の `-generation-config` を持ちremote flagを持たない間、managerの配備をskipして対応imageを公開できる。その後、操作担当者がwritersを凍結し、Terraform・[台帳移行](security-generation-migration.md)を完了してから同じworkflowをremote構成で再実行する。remote構成からlegacy modeへ自動fallbackしない。

フィード先頭は通常catalogのクリップと、掲載済みの生成結果の両方を受け付ける。生成結果はtransport codec/manifestを持たない契約のまま、ID・revision・固定scene pathとmedia originを照合する。不完全なtransport情報や別jobのsceneは昇格を止める。この確認はHTTPの接続・metadata検査で、ブラウザ描画や実機性能の検証とは別。

CI deployはruntime argsの `/mnt/catalog/snapshots/<64hex>.json` から期待する静的catalog SHAを取り、LB経由の候補・公開appのprobeへ必ず渡す。APIの `X-Rangle-Catalog-SHA256` に加え、全probeの `X-Rangle-Revision` が候補revisionと一致することを確認する。revisionはCloud Runの `K_REVISION` から取得し、request headerを信用しない。候補ルートの反映遅延で旧版が応答しても合格にしない。候補では昇格を止め、公開確認では元のtraffic配分へ戻す。生成結果が先頭でもcatalog headerはbase static catalogの元JSON bytesを識別する。`check-deployment.sh` 単体では `EXPECTED_CATALOG_SHA256`・`EXPECTED_REVISION` を任意とする。

TerraformがLB用の候補NEG/backend・header routeとingressを所有し、CIは専用固定tagとimage/trafficだけを操作する。候補headerはrouting selectorであり、操作認可を省略しない。直列deployを維持し、専用tagを別のworkflowや手動配備と共有しない。切替手順と未実施のクラウド検証は[配備手順](stg-deployment.md#lb入口制限への切替)を参照する。

## release receipt

正常な公開確認の後、sourceの40桁commit SHA、run ID/attempt、候補revision・immutable image digest、静的catalogファイルSHA、generation config SHA（legacy設定時）、manager URL（remote時）、asset origin、元のtraffic配分と各復帰先revisionの実configを、小さいJSONへ記録する。remote時は `generation_manager` にmanagerのcandidate/前版のrevision・image digest・catalog/config SHAと新旧traffic配分も記録する。`schema_version` は1、最大64KiBとする。

前版は `services describe.spec.template` から取らない。Terraformによるruntime変更後、このdesired templateはまだ通常trafficを受けていない新configを指すことがある。配備前の `status.traffic` で実際に配信するrevisionを確定し、各revisionをread-onlyの `revisions describe` で取得して、`revision.spec.containers` の既知fieldだけを記録する。前版のimageがtagならその実値を残し、`image_digest` はnullとする。古いcatalog pathなどからSHAを確認できない場合もnullとし、desired templateの値で埋めない。

managerの候補と復帰先は専用repositoryのdigest固定imageと既知のmanager argsを必須とし、不明な旧manager構成を推測して配備しない。

候補のtarget argsとしてのみ配備前のdesired templateを使う。公開確認後、trafficが候補100%であることと、実際の候補revisionのimmutable image・argsが要求と一致することを照合してからreceiptを生成する。新targetのcatalog SHAは必須で、旧版の未知値とは区別する。sourceのremote origin、args全量、env、credentials、GCS input URI、巨大なservice JSONはreceiptやSummaryへ含めない。

必須のローカル処理と外部保存を分ける。

| 処理 | 保存先・失敗時の扱い |
| --- | --- |
| JSON生成・検証とSummary追記 | `${RUNNER_TEMP}/rangle-release-receipt.json` とGitHub Step Summaryへ同じJSONを残す。上書きせず、途中ファイルを公開しない。公開後の生成・検証・Summary書込失敗は既存trapで元trafficへrollbackする |
| artifact保存 | `stg-release-<run_id>-<attempt>`、retention 7日、overwrite false。upload対象は上記の単一明示pathだけ。隠しdirectoryやrunner temp全体を渡さず、ファイル不在はupload errorにする |
| artifactサービス等のupload失敗 | 検証済み配備は維持し、Summaryへ保存失敗を表示する。JSONは既にSummaryへ記録してあるため、そこから回収する。外部保存障害だけでは正常配備をrollbackしない |

guard・期限・候補no-traffic・実instance設定・元trafficの厳密復帰・自分の候補tagだけを消す契約は維持する。receiptの保存やartifact uploadにGCP/IAM設定を追加しない。artifact保持期限後の長期保存や、mainへのdigest昇格は別の判断であり、この記録だけで完了したとは扱わない。

ローカルの `python3 scripts/deploy-stg.test.py` はgcloud/curl/date/sleepをstubに置き換え、Cloud APIへ接続しない。desired templateと実配信revisionの相違、秘密の非export、catalog/revision不一致、receipt失敗に加え、manager欠落・不正args/IAM、認証付きhealth、2serviceの昇格順・復帰、legacy bootstrapを確認する。通常catalog・generated-firstの両方を維持する。`delivery` jobの `migrate-generation-ledger.test.py` と `check-cloud-security.test.py` もfake transportを使い、実移行やlive cloud checkerは実行しない。`terraform` jobの `security_policy_regex_test.py` はcapturing groupの回帰をofflineで検出するが、CEL/API compilerの検証は代替しない。これらは実環境のIAM実効・移行完了・GPUや100k性能を証明しない。

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
