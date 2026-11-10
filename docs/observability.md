# rangleのログ監視

発表用stgのAPI・生成・配信・運用の失敗をCloud Logging/Monitoringから調べる。[構成図](diagrams/observability.html)・[全体構成](cloud-architecture.md)・[ログ形式](logging.md)。通知/SLO、ブラウザtelemetry、分散traceの自動対応づけは対象外。

## 保存と集約

正本は [observability.tf](../infra/gcp/modules/presentation/observability.tf)。project `comin-up`、`asia-northeast1` に置く。

| 資源 | 設定 |
| --- | --- |
| bucket / sink | `rangle-stg-logs`、Tokyo、保持30日、Log Analytics無効 |
| dashboard | `rangle stg - logs and runtime`、既存IAMで閲覧 |
| 対象 | 公開Run、`service=rangle-trainer/rangle-live`、専用LBのHTTP/HTTPS forwarding rule、guard Workflow/Scheduler、生成同期Scheduler。generation有効時は専用managerも追加 |

- MLの `jsonPayload.service` は実ログで確認済み。構造化されないlibrary出力やcontainer開始前の基盤ログはMLパネルに入らないため、provider IDで元jobログを調べる。Vertex labelsからログlabelsへの転記は仮定しない。
- `_Default` / `_Required` と既存routingは変更せず、監査ログは専用sinkから除外する。他の保存先へのコピー・費用は残る。30日保持は専用bucketだけに適用し、改ざん防止lockではない。
- bucketは `prevent_destroy` を持ち、公開停止後も保持期間までログを残す。sink作成前の履歴は遡及コピーされない。
- 同一projectのLogging bucket宛sinkにwriter identity / `bucketWriter` は不要。app/trainer/CIの権限と収集方式は変更しない。閲覧追加時はLogs Viewer・対象viewのLogs View Accessor・Monitoring Viewer等に限定し、公開共有しない。

## ダッシュボードと検索

`terraform output -json observability` でdashboard/Logs Explorer URL、bucket/view/filterを取得する。

パネルは警告・エラー、UI/API、生成/LIVE、LB/CDN、guard/同期と、公開Runの要求数/秒・revision別p95を表示する。ログは専用 `_AllLogs` viewを使い、generation有効時はmanagerも表示する。グラフは公開Runだけで、health/UIを含むサービス全体の指標。APIだけのp95やブラウザ初回描画ではなく、数分遅延し得る。

app/candidate backendの要求ログは既存の10% sampling、CDN backend bucketは自動要求loggingを使う。ログ件数を総要求数・hit率へ換算しない。

アプリjob IDから検索する例：

```text
jsonPayload.job_id="<application-job-id>"
OR jsonPayload.session_id="<application-job-id>"
```

Goの `generation job submitted` の `provider_name` は `projects/.../customJobs/<provider-id>`。末尾IDで元projectのVertexログを検索する（専用view外も対象）：

```text
resource.type="ml_job"
resource.labels.job_id="<provider-id>"
```

新Go imageは `severity/message`、旧imageは `jsonPayload.level/msg` を使う。警告・エラーパネルは旧levelも検索する。過去ログの分類はimage更新で変わらない。診断用にCookie/本文/入力映像/認証情報を追加しない。platform要求ログのURL/client情報もproject IAMに従って閲覧する。

## 配備と検証

1. 最新presentation stateと承認済み設定でplanを確認し、別変更のruntime/IAM/traffic/生成設定を混ぜない。
2. API・bucket・sink・dashboardを配備し、少数の通常要求で専用viewへの到達、30日保持、各パネルを確認する。
3. Goのseverity対応は通常CIで配備する。新しいGPU jobは不要。

ローカルはGo race/vet、Terraform module/root validate・mockを使う。Dashboard APIの `projects.dashboards.create?validateOnly=true` と `entries.list` でも、保存せず定義/filterを検証できる。

## 2026-11-10 JSTの配備記録

専用bucket・sink・dashboardとAPI管理を **5追加・0変更・0削除** で適用した。別PRのingress/候補backend等を除いた保存済みplanを確認し、runtime/image/trafficとdeployment/budget_guard outputは変更しなかった。

| 確認 | 結果 |
| --- | --- |
| [dashboard](https://console.cloud.google.com/monitoring/dashboards/custom/df3cbd9c-c2fd-4c0d-ac2e-f7ff0e03c0fd?project=comin-up) | 8 widgets、API validateOnly成功 |
| bucket / sink | 30日保持・有効状態・宛先/filterをAPI照合 |
| 専用view | 反映待ち後のCloud Run 200・生成同期Schedulerを受信 |
| media | 公開LIVE index 4,487 bytesの取得で200・`cacheLookup=true` を受信。hit率/大量配信試験は未実施 |
| metrics / local | timeSeries APIで2グラフの実データ取得。Go race/vet、Terraform validate・mock 21ケース成功 |

このインフラ適用ではGo imageを更新していない。MLのservice属性は既存実ログで確認したが、新GPU jobの専用bucket到達は未検証。

### Go再配備後の受信確認

Actions復旧後の[配備run 42735637379](https://github.com/Hosi121/rangle/actions/runs/42735637379)でGoを再配備した。revision `rangle-stg-00082-gmh` の実image・traffic 100%と[release receipt](https://github.com/Hosi121/rangle/actions/runs/42735637379/artifacts/13726792901)の一致を確認した。

専用bucketの `_AllLogs` viewで、次の起動ログを受信した。

```text
timestamp: 2026-11-10T01:29:40.087730117Z
revision: rangle-stg-00082-gmh
severity: INFO
jsonPayload.message: server started
jsonPayload.service: rangle-server
insertId: 6af2738400015bc037ba9711
```

bucketの `ACTIVE` / `retentionDays=30`、sinkの宛先/filter、dashboardの8 widgetsも実APIで再確認し、[#33](https://github.com/Hosi121/rangle/issues/33)は完了した。SLO通知・端末QoE・10万CCUの運用検証は対象外のままで、新GPU jobの専用bucket到達も未検証である。

## 参照

- [Cloud Runの構造化ログ](https://docs.cloud.google.com/run/docs/logging#special-json-fields)
- [Logging bucketの保持期間](https://docs.cloud.google.com/logging/docs/buckets)
- [同一projectへのsinkとwriter identity](https://docs.cloud.google.com/logging/docs/reference/v2/rest/v2/projects.sinks)
- [dashboardログパネル](https://docs.cloud.google.com/monitoring/api/ref_v3/rest/v1/projects.dashboards#LogsPanel)
- [LB/CDNログ設定](https://docs.cloud.google.com/cdn/docs/cdn-logging-monitoring)
