# ログ方針

GoのAPI・CLI・ワーカーで、実行ログは標準の `log/slog` に統一する。外部依存を増やさず、同じ属性で処理を追えるようにする。

## 出力形式

入口で `slog.New(slog.NewJSONHandler(os.Stderr, nil))` を生成し、`With("service", サービス名)` を付ける。既定はINFO以上、1イベント1行のJSON。標準キー `time`・`level`・`msg` を維持する。CLIの計算結果はstdoutへ出す。

メッセージは `server started` のような短い固定文にする。IDや処理時間などの変動値は文字列に埋め込まず、属性として渡す。

## レベル

| レベル | 用途 |
| --- | --- |
| DEBUG | 開発時の詳細な診断。通常は無効 |
| INFO | 起動・正常終了、ジョブの開始・完了などの状態変化 |
| WARN | 再試行や代替経路で継続できる異常 |
| ERROR | リクエストやジョブが失敗し、対応が必要な異常 |

正常な終了要求やキャンセルはERRORにしない。フレームやGaussianごとのログは通常出さない。

## 記録する場所

- CLI、HTTP境界、ジョブ実行の責任を持つ層で記録する。同じ失敗を各層で重複記録しない。
- ライブラリは原因を付けたerrorを返す。状態変化を記録する必要がある場合にだけ `*slog.Logger` を受け取る。
- リクエストやジョブに紐づく記録では `InfoContext`・`ErrorContext` 等へ既存のcontextを渡す。IDは明示的な属性として付ける。
- `net/http.Server.ErrorLog` は `slog.NewLogLogger` で同じhandlerへ接続する。

## 属性

| 属性 | 用途 |
| --- | --- |
| `service` | コマンド・サービスを識別する固定名。必須 |
| `error` | 処理が返したerror。失敗時に付ける |
| `request_id`・`job_id` | 割り当て済みの処理識別子。該当する場合だけ付ける |
| `clip_id`・`revision` | 対象のコンテンツと資産の版 |
| `duration_ms` | ミリ秒単位の数値。単位をキーで明示 |

配信コマンドは起動時に `listen_addr` を記録する。`rangle-dev` は `manifest_url` と `fixture="synthetic"`、`rangle-server` はサンプルの `scene_id` と公開する `clip_count` を付ける。資産のI/O失敗は `clip_id`・`revision`・クエリを含まない `asset_url` と原因を配信handlerで記録する。認証情報、署名付きURLのクエリ、リクエストの本文・ヘッダー全体は記録しない。

## 他言語と運用

PythonのML処理とDartのアプリでは、各言語のロガーを使い、サーバーへ集約するログの属性名をこの方針に揃える。Go向けの独自ロガーラッパーや、全処理へのloggerの注入は必要になってから判断する。

VertexのPython workerは `service="rangle-trainer"`・`severity`・`event` を含むJSONをstdoutへ出す。開始・完了はアプリの `job_id`、学習途中はiteration・loss・Gaussian数・経過秒を記録する。実行全体はCloud LoggingのVertex job IDで絞り込み、Goの `provider_name` と対応させる。画像・モデル・入力本文はログへ埋め込まず、結果ファイルと分ける。

ライブworkerは同じJSON方針で `service="rangle-live"` と `session_id` を使い、session IDをAPIのjob IDに揃える。`cloud_live_started`・`cloud_live_published`・`cloud_live_report`・終了/失敗を記録する。最終reportには起動準備、入力開始からCDN実HTTP確認までの秒数、source EOF前に公開できたか、snapshot数・size・held-out画質、子processの終了結果を分けて残す。秒数は `_seconds`、時刻はUTC、同一host内の順序判定はmonotonicを使う。Vertexの起動待ちやクライアント描画を含む値へ読み替えない。ADCの認証情報・HTTP例外本文・画像・モデル本体は記録しない。[クラウドLIVE](cloud-live.md)

クラウドでの収集・保持期間・権限はIaCの運用設計で定める。Cloud Loggingへ接続する際は、JSONの収集とseverity・traceへの対応づけを確認する。現在のJSON出力だけで監視やトレース連携まで実装済みとは扱わない。

参照: [Go log/slog](https://pkg.go.dev/log/slog)、[Cloud Loggingの構造化ログ](https://docs.cloud.google.com/logging/docs/structured-logging)。
