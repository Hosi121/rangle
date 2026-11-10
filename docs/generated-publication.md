# 生成結果のフィード掲載

成功結果の共有URL `/demo/?clip=generated-{job-id}` とfeed掲載は独立する。掲載で通常feedの検索/tag/pagingへ入り、取り下げで一覧から外れる。掲載前・取り下げ後も既知のURLで閲覧でき、ログインした第三者はいいね・コメントを使える。media削除・非公開化ではない。

## APIと認可

運営は `PUT /api/v1/generation/jobs/{id}/publication`、団体担当者は `PUT /api/v1/publishers/{account}/generation/jobs/{id}/publication` を使う。[各経路のsession/CSRFと現在の権限](publisher-auth.md)を検査し、団体では操作した担当者でなく団体所有者を照合する。

| 項目 | 契約 |
| --- | --- |
| body | `{"published":true}` / `{"published":false}`、最大256bytes。余分/不足field・null・queryを拒否 |
| 拒否 | job不在/非所有者404、未完了409。成果物検証失敗は409 `publication_unavailable`、保存先障害は同code503 |
| 成功 | GETと同じ `{job, viewer_url, scene_url}`。create/get/listのjobは常にboolean `published`を含む |
| 冪等性 | 同状態PUTは成功。掲載済み再PUTはmodelを取得せず、取り下げも成果物を取得しない。未完了は取り下げも拒否 |

掲載でGPU起動・attempt・active slotを変更しない。

## 掲載前の検証

Succeeded jobの保存済みpreset snapshotからCC BY 4.0出典・入力SHA256・mode/profileを照合する。後日のpreset編集で過去jobを差し替えない。

取得先は設定済みmedia originの `/generated/{32桁job-id}/` と固定名だけ。任意scene URLを追わずredirectしない。scene64KiB・metrics256KiB・model32MiB・要求全体25秒。HTTP200、token解析したpublic cacheと正のTTLを必須とし、private/no-store/no-cacheを拒否する。s-maxageがあればshared TTLを優先し、なければmax-ageを使う。

1. sceneのversion・frame・出典/許諾を確認する。staticは `model.splat`、dynamicは `model.rgs` だけを参照する。
2. metricsのjob ID・入力SHA・出典/許諾をsnapshotと照合する。mode省略は旧staticだけ許可し、明示値は一致必須。dynamicはmode必須。profile省略はbaseline、追加profileは保存設定と一致必須。
3. 現workerのmodel_bytes/model_sha256はpairで照合し、片方だけ・null/0・不正hashを拒否する。旧staticだけpair全体の省略を許す。staticのsplat_bytes/splat_sha256は常に必須で、gzip展開後32MiB・32byte境界・raw SHAを照合する。dynamicはmodel pairとRGS1 header・scene時刻・payload/展開サイズを検証する。

旧staticに圧縮file測定値がなければraw宣言値を検証し、`ArtifactProof.Model`へ受信gzipの実サイズ/SHAを記録する。圧縮hashを事前宣言と照合したとは扱わない。scene/metrics/modelそれぞれの実サイズ/SHAを保存する。

不変性はworkerのscene-last・GCS generation-match=0・固定job pathの上書き不可で保つ。HTTP immutableは任意hintで、CDNの `public,max-age=86400` と従来の `public,max-age=31536000,immutable` を受け入れる（[TTL優先順位](https://www.rfc-editor.org/rfc/rfc9111.html#section-4.2.1)）。

失敗時はpublished/registryを変更せず、409境界でjob_idと原因だけをWarn1回記録する。認証・応答本文は記録しない。これは整合性検証でありGPU画質/実機性能の合否ではない。model取得は所有者の掲載検証時だけで、Goは視聴者へ中継しない。previewは保存済み審査presetを使い、任意URLを受け取らない。

## 永続化とフィード

`generation_jobs/{id}.published` と `generation_control/public_feed.entries` を同一Firestore transactionで更新する。registryはattempt上限に対応した最大10件のmetadata/検証記録で、各origin再検証は単一docの強整合read。process cache・履歴比例ID/page cacheは持たない。

staticは既存transport manifestで起動検証し、生成は別の `catalog.PublicClip` として統合する。偽manifest/合成codecを付けず、static ID衝突を拒否する。static JSON/search indexを再利用し、全件copy/再符号化せず生成最大10件をID順prefixへ置く。

統合canonical snapshotで検索/pagingする。掲載変更はbody/ETag/cursorを変え、旧cursorは400。再起動/複数instanceでも同じmetadataから同順序/ETagを再現する。registry障害は503/no-storeで、空feedを返さない。

公開URLと `public, max-age=15, stale-while-revalidate=30` を維持する。掲載後の明示更新は最初のmetadata GETへ `Cache-Control: no-cache` を付け、originが最新registryを読む。明示更新なしではcache期間中旧feedが残り得る。個人ID・nonce・時刻queryを追加しない。[UI契約](generation-publication-ui.md)

communityはstatic ID集合、または既知generated IDのSucceeded job1件で存在確認し、registry/掲載状態には依存しない。未成功/不明IDは拒否し、再掲載でも反応を保持する。job管理は所有者制限を保ち、未掲載結果を列挙するAPIは追加しない。

## 検証

CPU/APIとloopback Firestore emulatorで所有者・未完了・CSRF/上限、static/dynamic検証、破損/欠損/別job/出典/redirect拒否、冪等性、検索/paging/ETag/旧cursor、同時掲載・再起動、未掲載/取り下げ後の第三者交流とdeep linkを確認する。実IAM/OIDC・停止完了・browser操作は別確認。

## リクエストに依存しない状態同期

cloud managerの認証・audience・専用SA・有効化は[生成インフラ](generation-infrastructure.md#状態同期)を正本とする。単一process modeは `-generation-sync-audience https://app.<domain>/internal/generation/sync` 指定時だけ `POST /internal/generation/sync` を有効にする。

cookieは使わず、Google署名OIDCのaudience/issuer/expiry/subject/verified emailを検証する。許可emailは `rangle-stg-generation-sync@<project>.iam.gserviceaccount.com` だけ。bodyは空か `{}`、任意job/configは受け取らずtokenを記録しない。

台帳のactive1件だけを同期し、終端transactionでslotを解放する。30分cancelを維持し、idle時はVertexを呼ばない。Admit/Submit・attempt追加/返金・掲載・model取得は行わない。

要求10秒、認証済み要求はinstanceごと1分1件。config期限と2026-11-14T15:00:00Zの早い方を過ぎるとtoken検証/Vertexより先に410。常駐goroutine/timerは使わず、Schedulerの2分間隔・自動retryなしで進める。

IaC `generation_sync_enabled` は既定false。専用SAにVertex/Firestore/IAM/配備権限を与えない。期限/JPY予算guardはSchedulerもpauseし、失敗を既存記録へ集約する。Terraformでpauseを勝手に解除せず、hard cost capとは主張しない。

初回・image/audience変更はSchedulerをpauseし、対応image/固定audience/planを照合・配備後にresumeして実OIDCを確認する。未対応flagを旧imageへ渡さず、明示traffic固定と候補検証を守る。Goテスト・Terraform validate/mock plan・Workflow400文字制限/実compilerと、実定期呼出しは別確認。
