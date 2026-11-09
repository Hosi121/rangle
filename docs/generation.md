# GS生成バックエンド

校正済みの複数カメラ画像から静的3DGS、複数時刻の画像から動く4DGSを学習し、自由視点ビューアで開ける。一般的な単眼動画からの生成は対象外。画面のデータ選択で「静的な3DGS（1時刻）」と「動く4DGS（複数時刻）」を区別する。

管理者でログインすると、フィードの代わりに管理ページを開く。`/demo/?generate=1` の直接リンクも管理者専用。管理ページでデータ選択、生成、状態確認、結果表示まで進める。画面を閉じてもジョブは続き、再度開けば自分の履歴を確認できる。生成結果のリンクは直接自由視点で開く。

公開Go APIは既存のアカウントセッションを使い、IAM認証必須の別Cloud Run `rangle-stg-generation` へ操作を転送する。managerが専用Firestore DB `rangle-stg-generation` のトランザクションで全ユーザー共通の実行枠を一つ確保し、Vertex AIの東京Spot T4を起動する。公開runtimeはcommunity DB `rangle-stg` を使い、生成台帳への直接アクセス・Vertex操作・trainer actAsを持たない。workerも配信APIとは別プロセスで学習する。

| 操作 | API |
| --- | --- |
| 利用できるデータセット | `GET /api/v1/generation/presets` |
| 作成 | `POST /api/v1/generation/jobs`、JSONは `{"preset":"…","request_id":"32桁の小文字hex"}` |
| 自分の履歴 | `GET /api/v1/generation/jobs` |
| 最新状態・結果 | `GET /api/v1/generation/jobs/{id}` |
| 中止 | `DELETE /api/v1/generation/jobs/{id}` |
| フィード掲載・取り下げ | `PUT /api/v1/generation/jobs/{id}/publication`、JSONは `{"published":true}` または `{"published":false}` |

プリセットを含む生成API全体で管理者ログインが必要。作成・履歴・job詳細・中止・掲載操作では、既存の所有者チェックも維持する。同じユーザー・request_idの再送は元のジョブを返し、追加起動しない。jobには `published` を含み、成功時には `viewer_url` と `scene_url` が付く。履歴一覧は保存済み状態を返す。個別GETと作成時の照合に加え、有効時はSchedulerが2分ごとにactive 1件の未終了状態を同期する。同期は生成attemptを増やさない。OIDCと有効化は[掲載・同期の契約](generated-publication.md)を参照。

入力のURI・SHA256、学習方式、コンテナdigest、GPU構成、期限はサーバー設定に固定する。ユーザーから任意のストレージパスやコマンドは受け取らない。presetの `mode` は `static` または `dynamic`。既存の設定で省略されている場合は `static` として扱う。作成時の方式はジョブへ保存し、その後の設定変更で書き換えない。品質profileもサーバー側presetに固定し、公開リクエストから指定できない。クラウドではmanagerだけが生成設定ファイルを読む。運用設定は[インフラの手順](generation-infrastructure.md)を参照。

クラウドの公開processは `-generation-manager-url` だけで生成先を選び、local生成configや同期endpointとの併用を拒否する。managerがreview済みconfigを読み、公開runtimeの署名・audience・issuer・expiry・verified emailを独立検証してからownerを受け取る。browser cookieや任意のprovider spec・document CRUDはmanagerへ公開しない。ローカルの単一process modeは開発と初回移行用に維持する。

この境界でも、公開runtimeを侵害した相手は認証済みruntimeとしてownerを転送し、残るattempt内の正規preset操作を行える。admin/CSRF確認は公開process側の責任であり、RCEに耐えると主張しない。manager自身が実行枠・予算・固定spec・deadlineを守るため、公開runtimeから台帳を初期化したり任意のVertex jobを作ったりする経路はない。公開feed・生成detail・交流の存在確認も認証付きmanager呼出しへ置き換え、閲覧者にadminログインを要求しない。

## 計算予算と失敗時

この発表用環境では同時1件・1件30分・最大10試行。閲覧者の同時接続数と、学習の同時実行数は別に扱う。T4とCPUの構成を固定し、1試行あたり保守的に$2を予約して、承認されたGPU関連の$20枠に対応させる。これは請求額の実測や、請求の厳密なhard capではない。新規環境の初回確認では試行数を1に設定する。失敗や中止でも試行枠は戻さない。

Vertexへの作成リクエストがタイムアウトしても、自動で再作成しない。ジョブラベルで照合してから状態を確定する。照合できない不明状態は実行枠を保持するため、運用者の確認が必要。中止要求だけでは実行枠を解放せず、終了状態を確認して解放する。

Vertexの30分制限に加え、workerにも作成時刻からの絶対期限を渡す。2026-11-14 15:00 UTC（11月15日00:00 JST）の停止ワークフローでも、このアプリのラベルを持つ東京のジョブだけを中止し、状態同期Schedulerをpauseする。Spotは空き不足や中断があり、即時実行や完了は保証されない。

## 学習と結果

校正済みの同期画像・動画、カメラ、初期点群からtrainer入力と来歴記録を作る共通処理は[入力前処理](dataset-preprocessing.md)を使う。品質profileと比較条件・測定値は[品質比較](training-quality.md)、可逆圧縮は[圧縮比較](temporal-compression-comparison.md)へ分けて記録する。

静的workerはgsplatのDefaultStrategyとRGB、L1+SSIMで最大6,000 iterationを実行する。動的workerは最大12,000 iterationで、同じGaussianの位置・速度・中心時刻・時間幅などを複数時刻から共同学習する。時刻ごとに独立した3DGSを作って並べる方式ではない。動的入力は2〜32時刻、画像配列512MiB以下に制限する。表現・評価・保存形式の詳細は[4DGS学習](dynamic-gs-training.md)を参照。

学習・評価カメラは分離し、評価画像は損失に使わない。動的モデルでは全評価カメラ・全時刻を評価し、正しい時刻と逆順の時刻を比較したPSNR、初・中・終時刻の画像も保存する。ただし提供された校正と初期点群が評価カメラを含む可能性があるため、独立した汎化性能とは主張しない。

静的出力は32バイト `.splat` とgzipによる転送圧縮。動的出力は既存ビューアのRGS1形式 `model.rgs` で、速度・中心時刻・時間幅を含む。一度受信したモデルをクライアントの時刻で動かす。RGS内部のbyte shuffleとgzipは可逆で、HTTPの `Content-Encoding: gzip` は付けない。色・回転などの保存時量子化は浮動小数点モデルに対して可逆ではないため、metricsではfloat版と保存形式を分けて評価する。

GCSにはファイルを新規作成限定で保存し、最後に `scene.json` を公開する。成功した結果は共有リンクで閲覧し、未掲載・取り下げ後もログイン済みの第三者がいいね・コメントを使える。フィードへの追加は自動ではなく、所有者が成果物を確認して掲載APIを呼ぶ。取り下げはフィード一覧から外す操作で、共有リンクや交流を非公開にはしない。固定path・サイズ・SHA256・出典の掲載検証は[生成結果の掲載](generated-publication.md)を参照。

私的な実験環境は独立した検証用であり、この実行経路・資格情報・配信基盤には接続しない。

## 有効化と確認

対応するGo imageを先に配備し、SHA256を確認した入力とdigest固定のtrainerを用意する。空の新規環境の初回configは `attempts: 1` とする。既存環境ではattemptを戻さず、受付・同期・掲載変更を凍結し、全jobのterminalとactive空を確認してから既存台帳を専用DBへ移す。native fields・使用数・owner・snapshot・履歴・掲載aggregateを保ち、private backupと移行後の厳密一致を確認する。[移行手順](security-generation-migration.md)

configはprivate catalogの内容SHA付きパスへ新規作成限定で配置し、managerのdatabaseを `rangle-stg-generation` とする。Terraformがmanager・限定IAM・停止定義と公開runtimeのremote引数を管理し、CIが同じimageのmanager候補を認証付きhealthで確認・昇格してから公開アプリをLB経由で検証・昇格する。初回bootstrapでは、旧public templateがlocal configだけを使う間はmanager配備をskipできる。remote flagがあるのにmanagerが欠落・不一致なら変更前に失敗する。TerraformとCIのCloud Run更新は同時に行わない。

CIの復帰対象は両serviceのrevision/trafficであり、DB・IAM・使用済みattemptは戻さない。targetで書き込みが始まった後は旧DBのmonolithへ生成処理を戻さず、同じ専用台帳を使う互換revisionを選ぶ。初回切替失敗で旧revisionへtrafficが戻った場合も、旧権限を復活させて生成を再開せず、移行状態を確認してremote構成を再検証する。

実ジョブの終了、metrics、評価画像、配信ファイルを確認してから、同じ台帳を保ったままconfigの試行上限を増やす。config変更は新しいSHAへ配置し、再度TerraformとCIで反映する。台帳の使用数や稼働中ジョブを消して枠を戻さない。無効化は既存ジョブの終了確認後に行う。
