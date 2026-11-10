# ログ方針

GoのAPI・CLI・workerは標準 `log/slog` を使う。収集先・検索・配備状況は[ログ監視の運用](observability.md)。

## 出力形式

入口で `slog.New(slog.NewJSONHandler(os.Stderr, nil))` を作り、`With("service", サービス名)` を付ける。INFO以上、1イベント1行JSONを既定とし、CLIの計算結果はstdoutへ出す。

| 実行環境 | 標準キー |
| --- | --- |
| ローカルCLI | `time`・`level`・`msg` |
| Cloud Run `rangle-server` | `ReplaceAttr` でトップレベルの `level` → `severity`、`msg` → `message`、WARN → `WARNING`。既存属性・入れ子キーは保持 |

messageは `server started` のような固定文にし、IDや処理時間は属性へ渡す。

## レベル

| レベル | 用途 |
| --- | --- |
| DEBUG | 通常無効の開発診断 |
| INFO | 起動・正常終了・job開始/完了などの状態変化 |
| WARN | 再試行・代替経路で継続できる異常 |
| ERROR | request/jobの失敗で対応が必要な異常 |

正常な終了要求・cancelはERRORにしない。frame/Gaussian単位の通常ログは出さない。

## 記録する場所

- CLI/HTTP/job境界の責任層で一度記録する。libraryは原因付きerrorを返し、状態記録が必要なときだけ `*slog.Logger` を受け取る。
- `InfoContext` / `ErrorContext` 等へ既存contextを渡し、IDは明示属性にする。
- `net/http.Server.ErrorLog` は `slog.NewLogLogger` で同じhandlerへ接続する。

## 属性

| 属性 | 用途 |
| --- | --- |
| `service` | 必須の固定コマンド/サービス名 |
| `error` | 失敗原因 |
| `request_id`・`job_id` | 割当済みの処理ID |
| `clip_id`・`revision` | コンテンツと資産の版 |
| `duration_ms` | 数値の処理時間 |

起動時は `listen_addr`、`rangle-dev` は `manifest_url`・`fixture="synthetic"`、`rangle-server` はsample `scene_id`・公開 `clip_count` を記録する。資産I/O失敗はhandlerで `clip_id`・`revision`・queryなしの `asset_url`・原因を残す。認証情報・署名URLのquery・request本文/headers全体は記録しない。

## 他言語と運用

Python/Dartは各言語のloggerを使い、集約属性を揃える。Goの独自wrapper・全処理へのlogger注入は必要時に判断する。

| worker | JSONと記録内容 |
| --- | --- |
| Vertex Python | `service="rangle-trainer"`・`severity`・`event`。開始/完了の `job_id`、途中のiteration/loss/Gaussian数/経過秒。Vertex job IDで検索し、Goの `provider_name` と対応づける |
| LIVE | `service="rangle-live"`・API job IDと同じ `session_id`。`cloud_live_started` / `cloud_live_published` / `cloud_live_report` / 終了・失敗 |

LIVE最終reportは起動準備、入力開始→CDN実HTTP確認の秒数、source EOF前の公開可否、snapshot数/size/held-out画質、子process終了結果を分ける。単位は `_seconds`、時刻はUTC、同一host内の順序はmonotonic。Vertex起動待ちやclient描画を含む値へ読み替えない。[クラウドLIVE](cloud-live.md)

画像・モデル・入力本文・ADC・HTTP例外本文はログへ含めず、結果ファイルと分ける。stdout/stderrのplatform収集を使う。

参照: [Go log/slog](https://pkg.go.dev/log/slog)、[Cloud Loggingの構造化ログ](https://docs.cloud.google.com/logging/docs/structured-logging)。
