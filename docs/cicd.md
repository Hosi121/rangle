# CI/CD

GitHub Actionsで検証し、`stg` を公開Cloud Run `rangle-stg` とprivate manager `rangle-stg-generation` へ配備する。対象環境・WIF・停止条件は[配備手順](stg-deployment.md)。

## workflow

| workflow | 実行条件・内容 |
| --- | --- |
| `ci.yml` → `validate.yml` | `stg` / `main` 宛PR。Go race/vet、viewer・Flutter、配備・台帳移行・metadata checkerのofflineテスト、Terraform静的/mock検証・Armor regex検査 |
| `docker-build.yml` | 同じPRでimage buildを確認 |
| `deploy-stg.yml` | `stg` push、または `stg` 指定の手動実行。検証・build・配備 |

Go＋Flutter Webを非rootの単一imageにまとめ、cacheを再利用してbuildし、digestで配備する。WIF認証と直列deployを使う。Terraformはargs/mount/IAM/network、CIはimage/trafficと専用候補tagを所有する。

## 配備の流れ

1. 実行元・設定・期限と共通検証を確認し、imageをbuild/pushする。
2. desired templateに `-generation-manager-url` があれば、既存managerのSA/IAM/固定args/config・catalog SHA path/pinned image、両serviceの**実trafficと復帰先revision**を取得する。欠落・不一致なら更新前に失敗させる。
3. 同じimageでmanagerをno-traffic更新する。`ci-manager-candidate` に新しいID tokenで `/health` を呼び、200・本文・`K_REVISION`由来headerを照合する。100%へ昇格後、実image/args/trafficを再確認する。
4. 公開appをno-traffic更新する。既定 `run.app` とcandidate-tag URLの外部直通拒否を確認し、`https://app.<domain>` に `X-Rangle-Candidate: ci-candidate` を付けて候補のhealth/UI/API/LIVE APIとmedia URL・媒体実体の有限サンプルを検証する。
5. 公開候補へ100%切り替え、候補headerなしのHTTPS経路を再検証し、receiptを保存する。

manager昇格後に公開候補・公開後検証・receipt保存・tag cleanupが失敗したら、**両serviceをそれぞれ元の明示revision/traffic比率へ戻す**。一方が失敗しても他方の復帰を試す。成功・失敗とも自分が作った2 tagだけを削除し、失敗releaseのreceiptは残さない。DB/IAM/attempt counterは巻き戻さない。外部artifact保存の失敗だけは下表の例外とする。

manager用ID tokenはbuild後にWIFで発行し、audienceを `https://rangle-stg-generation-113704194486.asia-northeast1.run.app` に固定する。email claimを含め、script内でexportを解除し、curlへstdin headerで渡す。argv・子process環境・ログ・receiptへ含めない。deployerはmanager invoke・image/traffic更新を持つが、managerの独立したidentity検証は業務API利用を拒否する。CIはIAM/network/DBを変更しない。

managerの削除設定は `-generation-cleanup-url-map rangle-stg-https` だけを許可し、公開runtimeには渡さない。IaCでargsを変更しても既存trafficは維持するため、候補の起動確認と明示昇格、または次のCDで有効にする。初回のcustom role作成後、GCSへの権限付与が反映待ちで失敗した場合は同じ限定権限で再plan・適用する。

初回bootstrapに限り、公開templateがlegacy `-generation-config` を持ちremote flagを持たなければmanager配備をskipできる。その後writersを凍結し、IaCと[台帳移行](security-generation-migration.md)を完了してremote構成で再実行する。remoteからlegacyへの自動fallbackはしない。

候補・公開probeは次を照合する。

- desired argsの `/mnt/catalog/snapshots/<64hex>.json` から期待SHAを取り、APIの `X-Rangle-Catalog-SHA256` と比較する。生成結果が先頭でもSHAはbase static catalogの元JSON bytesを識別する。
- 全probeの `X-Rangle-Revision` を候補revisionと照合する。値は `K_REVISION` 由来でrequest headerを信用しない。反映待ちで旧版が応答すれば候補昇格を止め、公開後ならrollbackする。`check-deployment.sh` 単体の `EXPECTED_CATALOG_SHA256` / `EXPECTED_REVISION` は任意。
- フィード先頭の通常clipまたは掲載済み生成結果を検証する。生成結果はtransport codec/manifestを持たない契約で、ID・revision・固定scene path・media originを照合する。不完全なtransport情報や別jobのsceneは拒否する。

`check-media-delivery.py` はAPI先頭1clipを、候補と公開後それぞれのsmokeで実取得する。transportはAPIと独立manifestのID/revision/codecを照合し、先頭representationの先頭segmentだけをsize/SHA256・RGS1 container/headerで検証する。transportのsceneは必須とし、scene/initのサイズ・model参照・timelineも照合する。両形式のsceneにfiniteなcamera vector・非退化position/up・FOV・非空attributionを要求する。生成結果はmanifestを仮定せず、固定job pathのscene/metrics/modelを取得し、job IDとstored size/SHA256を照合する。static生成結果は `Content-Encoding: gzip` を必須とし、旧形式もgzip展開を32MiB以内に制限してraw splat size/SHA256を照合する。previewがあればContent-TypeとWebM構造（DocType・video track・media block）を確認する。

媒体stageは**1回につき最大8 request・合計64MiB・60秒**、各requestはconnect 5秒・全体20秒以内で、redirect/retryを許可しない。body上限はmanifest 1MiB、scene 64KiB、metrics 256KiB、model 32MiB、preview 16MiB。curl設定ファイルを無効化し、媒体にはcandidate selector・appの認証header/cookieを送らない。既存app metadata stageは5 endpoint、各request最大3試行・1MiB・20秒の上限を維持する。候補の反映待ちは既存の最大5回のsmoke再試行を行うため、媒体stageの上限も各回に適用する。

欠損・redirect・不正なorigin/identity/codec・size/SHA不一致・不正containerは候補昇格を止め、公開後なら既存の復帰処理へ進む。対応transport codecは `rangle-temporal-rgs1` と `rangle-temporal-rgs1-segments-v1`。旧形式の区間長は `(N-1)/FPS`、分割形式は最低2区間と先頭sceneの `N/FPS` を確認し、RGS1 headerの最終sample時刻は両形式で `(N-1)/FPS` を維持する。後続区間は取得しない。未知codecは明示的に失敗する。このsmokeは[公開時の承認・全artifact検証](asset-publication.md)を置き換えず、全clip/全representation/全区間の完全性、Gaussian値の全検証、動画frame decode、ブラウザ描画、CDN負荷・性能を証明しない。`main` への同一artifact昇格、少量traffic保持/SLO判定も別の残条件である。

候補headerは公開のrouting selectorで、操作認可は通常どおり適用する。専用固定tagを他workflow/手動配備と共有しない。[LB切替手順・適用状態](stg-deployment.md#lb入口制限への切替)

## release receipt

成功した公開検証の後に、`schema_version=1`、最大64KiBのJSONを作る。

| 記録 | 内容 |
| --- | --- |
| release | sourceの40桁commit SHA、run ID/attempt、candidate revision・immutable image digest、必須static catalog SHA、legacy時のconfig SHA、remote時のmanager URL、asset origin |
| 復帰先 | 配備前の実traffic比率と、その各revisionの実config |
| `generation_manager`（remote時） | managerの新旧revision/digest/catalog・config SHA/traffic比率 |

前版は `status.traffic` から確定し、`revisions describe` の `revision.spec.containers` の既知fieldだけを読む。`services describe.spec.template` は未昇格のruntime変更を含み得るため、**候補target argsにだけ**使う。旧imageがtagなら実値を残してdigestはnull、確認できない旧SHAもnullとし、desired templateで補完しない。managerの新旧revisionには専用repositoryのdigest固定imageと既知argsを必須とする。

receipt生成前にcandidate 100%と実revisionのimage/args一致を確認する。remote origin、args全量、env、credentials、GCS input URI、巨大なservice JSONはreceipt/Summaryへ入れない。

| 保存処理 | 契約 |
| --- | --- |
| JSON生成・検証・Summary | `${RUNNER_TEMP}/rangle-release-receipt.json` とStep Summaryへ同じJSONを保存。上書き・途中ファイル公開なし。失敗時はrollback |
| artifact | `stg-release-<run_id>-<attempt>`、retention 7日、overwrite false。上記単一pathのみをuploadし、ファイル不在はerror |
| upload障害 | 正常配備を維持し、Summaryへ保存失敗を記録。JSONはSummaryから回収する |

長期保存やmainへのdigest昇格は別の判断とする。receiptのためにGCP/IAM権限を追加しない。

## 初期設定

稼働Cloud Run、公開catalog/media、app/mediaのDNS・HTTPSを用意し、repository variablesを設定する。

| 変数 | IaC output / 値 |
| --- | --- |
| `GCP_WORKLOAD_IDENTITY_PROVIDER` | `deployment.workload_identity_provider` |
| `GCP_DEPLOY_SERVICE_ACCOUNT` | `deployment.deploy_service_account` |
| `RANGLE_STG_DOMAIN` | base domain（scheme/pathなし） |

revision名はCloud Runに生成させ、tagは別操作で付ける。同時指定による[provider差分問題](https://github.com/hashicorp/terraform-provider-google/issues/17218)を避け、IaCのruntime設定を保持する。build・配備・昇格期限は **2026-11-14T15:00:00Z**。停止・予算監視は別jobが担当する。

`python3 scripts/deploy-stg.test.py` はgcloud/curl/date/sleepのstubで、実配信revisionとdesiredの相違、秘密の非export、SHA/revision不一致、receipt失敗、manager設定・認証・2service復帰、legacy bootstrap、通常/generated-firstと媒体欠損/破損による候補停止・公開後復帰を確認する。`check-media-delivery.test.py` の有限取得・origin/redirect/header分離・proof/container異常テストも同じコマンドから実行する。deliveryの移行/checker testもfake transport、Terraformのregex testもofflineである。これらはlive IAM・実移行・CEL compiler・ブラウザ描画・GPU/100k性能の証拠ではない。
