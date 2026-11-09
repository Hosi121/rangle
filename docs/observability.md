# rangleのログ監視

目的は、発表用stgでAPI・モデル生成・公開配信・運用処理の失敗を一つの画面から調べること。Cloud LoggingとCloud Monitoringを使い、追加の常駐collector・データベース・外部監視サービスは作らない。通知/SLO、ブラウザからのイベント送信、MLの学習変更は対象外。

## 保存と集約

`infra/gcp/environments/presentation` → `modules/presentation/observability.tf` が正本。対象projectは `comin-up`、地域は `asia-northeast1`。

| 資源 | 設定 |
| --- | --- |
| Cloud Loggingバケット | `rangle-stg-logs`、Tokyo、保持30日、Log Analytics無効 |
| sink | `rangle-stg-logs`、同一projectの専用バケットへコピー |
| ダッシュボード | `rangle stg - logs and runtime`、既存IAMで閲覧 |

sinkはCloud Run `rangle-stg`、JSONの `service=rangle-trainer/rangle-live`、専用LBのHTTP/HTTPS forwarding rule、予算停止Workflow・Scheduler、生成同期Schedulerを絞って収集する。Vertex jobのlabelsがそのままログのlabelsになるとは仮定しない。既存MLイベントが `jsonPayload.service` に入ることは実ログで確認した。構造化されていないライブラリ出力やcontainer開始前の基盤ログはMLパネルの対象外で、provider IDを使って元のjobログを調べる。

既存の `_Default` / `_Required` bucket・sinkは変更せず、監査ログを専用sinkから除外する。30日設定はこの専用バケットの保存分に適用される。既存の保存先へもroutingされる場合はコピーが残り、保存量は増える。既存のproject共通保持設定・監査ログの保存期間を、この変更で短縮したとは扱わない。

同じprojectのLoggingバケットへ送るsinkはwriter identityと追加の `bucketWriter` IAMが不要。アプリ・trainer・CIの権限を増やさず、stdout/stderrのプラットフォーム収集を使う。閲覧者を追加する場合は、Logs Viewer・対象viewのLogs View Accessor・Monitoring Viewerなど必要な閲覧権限だけを検討する。この変更では新しい閲覧者や公開共有を追加しない。

バケットは `prevent_destroy` を持つ。既存の予算・期限guardによるCloud Run/LB停止後もログを保持し、30日の保持期間で失効させる。`retention_days` は改ざん防止のlockではない。sink作成前の履歴は専用バケットへ遡及コピーされない。既存履歴は元の保存先から調べる。

## ダッシュボードと検索

配備後に `terraform output -json observability` でダッシュボードURL、Logs Explorer URL、バケット・view・収集条件を取得できる。

画面にはCloud Run要求数/秒、revisionごとの要求時間p95と、警告・エラー、UI/API、生成/LIVE、LB/CDN、guard/同期のログを表示する。ログパネルは専用 `_AllLogs` viewを読む。Cloud RunのグラフはUI・healthを含むサービス全体の既存プラットフォーム指標で、ブラウザ初回描画やAPIだけのp95ではない。表示に数分の遅延があり得る。

app/candidate backendの要求ログは既存の10% samplingを保つ。CDNのbackend bucketは自動の要求loggingを使い、LB設定やcache契約は変更しない。表示されたログ件数から総要求数やhit率を断定しない。

Logs Explorerで同じviewを選び、アプリのjob IDから追う例：

```text
jsonPayload.job_id="<application-job-id>"
OR jsonPayload.session_id="<application-job-id>"
```

Goの `generation job submitted` イベントの `provider_name` がVertexの実job名に対応する。`projects/.../customJobs/<provider-id>` の末尾のIDを使い、元のVertexログを調べる例（この検索では専用viewの範囲外のログも元project側で確認する）：

```text
resource.type="ml_job"
resource.labels.job_id="<provider-id>"
```

新しいGo imageでは `severity` と `message` がCloud Loggingの標準フィールドに対応する。旧imageは `jsonPayload.level` / `jsonPayload.msg` のままなので、警告・エラーパネルでは旧levelも検索する。Goの変更を配備するまで過去ログのseverity分類は変わらない。認証情報・Cookie・本文・入力映像を診断目的で追加記録しない。既存のプラットフォーム要求ログにはURLやclient情報が含まれ得るため、閲覧はproject内の権限に従う。

## 配備と検証

1. 最新のpresentation stateと承認済み設定でplanを確認する。他PR由来のruntime、IAM、traffic、生成設定などの差分は、このログ基盤への適用に混ぜない。
2. Logging/Monitoring API、専用bucket、sink、dashboardを配備する。収集開始後の通常要求を少数確認し、専用viewへ届くこと、30日保持、dashboardの各パネルを確認する。
3. Go imageのseverity対応は通常CIの候補検証とtraffic切替で配備する。MLの新しいjob/GPU起動は不要。

ローカルではGo race/vet、Terraform module/root validateとmock検証を実行する。Google Cloud APIの `projects.dashboards.create?validateOnly=true` と `entries.list` でも、保存を伴わずダッシュボード定義・収集フィルターを検証できる。

## 参照

- [Cloud Runの構造化ログ](https://docs.cloud.google.com/run/docs/logging#special-json-fields)
- [Logging bucketの保持期間](https://docs.cloud.google.com/logging/docs/buckets)
- [同一projectへのsinkとwriter identity](https://docs.cloud.google.com/logging/docs/reference/v2/rest/v2/projects.sinks)
- [ダッシュボードのログパネル](https://docs.cloud.google.com/monitoring/api/ref_v3/rest/v1/projects.dashboards#LogsPanel)
- [LB/CDNのログ設定](https://docs.cloud.google.com/cdn/docs/cdn-logging-monitoring)
