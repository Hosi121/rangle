# 生成結果のフィード掲載

生成結果の公開URLと、フィードへの掲載は別の状態として扱う。成功した生成結果は従来どおり `/demo/?clip=generated-{job-id}` で開ける。掲載すると通常の `/api/v1/clips` の検索・タグ・pagingに入り、同じclip IDでいいね・コメントを使える。掲載取り下げはフィードと新しいコミュニティ操作から外すもので、結果のURLやmedia bytesを非公開化・削除する操作ではない。

## APIと認可

`PUT /api/v1/generation/jobs/{id}/publication` は既存sessionとcross-origin protectionを使い、所有者だけが `{"published":true}` / `{"published":false}` を指定できる。JSONは256bytesまで、余分なfield・不足field・null・queryを拒否する。存在しないjobと非所有者は404、未完了jobは409、成果物の検証失敗は409 `publication_unavailable`（保存先の障害は同codeの503）。成功応答は既存GETと同じ `{job, viewer_url, scene_url}`。create/get/listの `job` に常にbooleanの `published` を含める。

同じ状態のPUTは冪等で、掲載済みへの再PUTではmodelを再取得しない。未掲載への取り下げも成功する。取り下げは失われた成果物を再取得しないため、media障害時にも掲載を外せる。未完了jobは取り下げ要求も拒否する。掲載操作はGPU起動・生成attempt・active slotを変更しない。

## 掲載前の検証

Succeeded jobの保存済みpreset snapshotを使い、CC BY 4.0の出典、入力SHA256、mode/profileを確認する。現行presetの後日の編集で過去jobの出典やmodeを差し替えない。

Goが検証のために取得するURLは、設定済みmedia originの `/generated/{32桁job-id}/` と固定ファイル名だけ。scene内のURLを自由に追跡せず、redirectもしない。sceneは64KiB、metricsは256KiB、modelは32MiB、要求全体は25秒以内。HTTP 200とpublicなcache宣言、正のTTLを要求する。Cache-Controlはdirective名をtokenとして解析し、private・no-store・no-cacheを拒否する。s-maxage指定時はshared TTLを優先し、未指定ならmax-ageの正値を確認する。

1. scene.jsonのversion、フレーム情報、出典・許諾を確認する。staticは単一の `model.splat`、dynamicは `model.rgs` だけを参照できる。
2. metrics.jsonのjob ID・mode・入力SHA256・出典・許諾とmodelサイズ/SHA256を確認する。追加profileの場合はmetricsのquality_profileも保存済み設定に一致させる。旧baseline metricsの省略はbaselineとして扱う。
3. model bytesの実サイズとSHA256を照合する。static gzipは展開後32MiB以内で、32byte record境界と展開後SHA256も照合する。dynamicは既存RGS1 header・sceneの時刻・payloadサイズ・圧縮時の展開サイズを確認する。

scene/metrics/modelそれぞれの実サイズ・SHA256を検証記録に保存する。workerのscene-last、GCS generation-match=0、固定job pathへの上書き不能な生成先がcontentの不変性の基盤となる。

HTTPのimmutableは任意のcache hintで、この基盤とは区別する。CDNが応答を public,max-age=86400 へ書き換えてhintを省いても掲載でき、従来のpublic,max-age=31536000,immutableも受け入れる。CDN TTLやIACは変更しない。[shared TTLの優先順位](https://www.rfc-editor.org/rfc/rfc9111.html#section-4.2.1)に従う。

検証失敗ではjobのpublishedとpublic registryを変更しない。HTTPの409を返す境界でjob_idと原因だけをWarnとして1回記録し、認証情報や応答本文は記録しない。これらは成果物・出典・サイズ・containerの整合性検証であり、GPU描画品質や実機性能の合否を保証するものではない。

モデルの取得は所有者の掲載検証時だけ。Goは視聴者へmodelを中継せず、再生は引き続きmedia originから直接行う。preview URLは保存済みの審査済みpresetを使い、ユーザー入力の任意URLを検証先として受け取らない。

## 永続化とフィード

Firestoreの `generation_jobs/{id}.published` と `generation_control/public_feed.entries` を同じtransactionで更新する。registryは最大10件の公開metadataと検証記録だけ。全体の生成attempt上限10件に対応し、各origin再検証は単一docの強整合readで完了する。process-localなregistry cacheや、履歴に比例するID/page cacheを持たない。

static catalogは従来の厳格なtransport manifestを通して起動時に検証する。生成結果は別の `catalog.PublicClip` metadataとして統合し、合成codecや偽のtransport manifestを付けない。既存static IDとの衝突を掲載時に拒否する。staticのJSON/search indexは再利用し、各要求でstatic全件をコピー・再符号化せず、最大10件の生成metadataをID順のprefixとして扱う。

生成metadataを含むcanonical snapshotを検索・pagingに使う。掲載/取り下げで応答内容とETag、cursor snapshotが変わり、旧snapshotのcursorは既存契約どおり400になる。複数instance・プロセス再起動でも同じ保存metadataから同じ順序・ETagを再現する。registry読み取りに失敗したら503/no-storeを返し、空フィードへ成功したように見せない。

公開URLと既存 `public, max-age=15, stale-while-revalidate=30` は維持する。掲載直後の明示更新では、UIが最初のmetadata GETへ `Cache-Control: no-cache` を付け、古いvalidatorを再検証する。originは必ずFirestoreの最新registryを読む。明示更新のない閲覧者にはHTTP cache期間中、旧フィードが残り得る。個人ID・nonce・時刻などのqueryで共有cacheを分断しない。

communityの存在確認はstatic ID集合に加え、掲載済みgenerated IDを確認する。取り下げで既存のいいね・コメントを削除せず、再掲載でも同じIDの反応を維持する。

## 検証と運用範囲

CPU/API testsとloopback Firestore emulatorで、所有者・未完了・CSRF/入力制限、static/dynamic成果物、破損/欠損/別job/別出典/redirect拒否、冪等PUT、検索/paging/ETag/旧cursor、複数Firestore clientの同時掲載、再起動、コミュニティ操作と取り下げ後のdeep linkを検証する。

この変更は外部cloud適用、GPU実行、デプロイ、実APIやブラウザの確認を実行しない。定期的なactive job同期は別の変更として扱い、この掲載処理へgoroutine timerを組み込まない。

## リクエストに依存しない状態同期

`-generation-sync-audience https://app.<domain>/internal/generation/sync` を指定したruntimeだけが、`POST /internal/generation/sync` を有効にする。cookieでは認証せず、Googleの署名を検証したOIDC tokenのaudience・issuer・有効期限・subject・verified emailを検査する。許可するemailは `rangle-stg-generation-sync@<project>.iam.gserviceaccount.com` の1つだけ。bodyは空または `{}`、任意jobや設定を受け取らず、tokenをログに出さない。

処理はFirestore admission台帳のactive 1件だけを取得し、未終了なら既存のprovider状態同期を実行する。状態が終端になれば既存transactionがactive slotを解放する。Admit/Submit、attemptの追加や返金、掲載変更、model取得は行わない。既存30分上限のcancelは維持する。idle時はVertexへ問い合わせない。

各要求は10秒、同じinstanceでは認証済み要求を1分に1件へ制限する。generation設定の期限と固定の2026-11-14T15:00:00Zの早い方を上限とし、それ以後はtoken検証やVertex呼び出しより前に410で拒否する。常駐goroutineやrequests-only CPUのtimerを使わず、Cloud Schedulerの2分間隔・自動retryなしのリクエストで進める。

IaCの `generation_sync_enabled` は既定false。明示enable時だけ専用SAとScheduler、runtime audienceを追加する。専用SAにVertex・Firestore・IAM・配備の権限を与えない。token発行には既存のCloud Scheduler service agentの権限を使い、public app内の専用endpointが署名とidentityを検証する。[HTTP target認証の公式契約](https://docs.cloud.google.com/scheduler/docs/http-target-auth)

既存の期限・JPY予算guardの停止分岐は、所有するgeneration-sync Schedulerもpauseする。pause失敗は既存の失敗記録へ集約し、旧来のCloud Run/Vertex停止手順と台帳を維持する。pauseをTerraform applyで勝手に解除しない。IAMやquota・費用の厳密なhard capを新たに主張するものではない。

適用はrootが新image/固定audience・plan・実Workflows compilerを確認してから行う。旧imageへ未対応flagを渡さず、既存の明示traffic固定と候補検証手順を守る。ローカルではGoのOIDC拒否・同期・上限・期限のテスト、Terraform validate/mock planとrender済みWorkflowの400文字制限を確認する。実Scheduler/token/Workflows compilerはこの変更では呼び出さない。
