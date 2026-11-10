# GS生成バックエンド

校正済み複数カメラの1時刻から静的3DGS、複数時刻から動く4DGSを生成する。一般の単眼動画・任意uploadは対象外。運営ページと `/demo/?generate=1`、団体担当者は `/demo/?publisher=1` でpreset選択・生成・状態/中止・結果を扱う。閉じてもjobは続き、再入すると所有するジョブの履歴を確認できる。

APIのprefixは運営用 `/api/v1/generation`、団体用 `/api/v1/publishers/{account}/generation`。以下は各prefixからの相対パス。

| 操作 | API |
| --- | --- |
| preset一覧 | `GET /presets` |
| 作成 | `POST /jobs`、`{"preset":"…","request_id":"32桁の小文字hex"}` |
| 所有するジョブの履歴 | `GET /jobs` |
| 状態・結果 | `GET /jobs/{id}` |
| 中止 | `DELETE /jobs/{id}` |
| 掲載・取り下げ | `PUT /jobs/{id}/publication`、`{"published":true}` / `{"published":false}` |

全API（preset含む）で各経路のsessionとCSRF検査が必要。[運営](accounts.md#管理アカウント)は従来の運営所有ジョブ、[団体担当者](publisher-auth.md#団体の生成公開)は自団体のジョブと許可済み入力だけを扱う。同じ所有者/request_idの再送は元jobを返し、担当者の交代でも追加起動しない。jobは `published` を含み、成功時は `viewer_url` / `scene_url` が付く。

履歴は保存済み状態を返す。個別GET・作成時の照合、有効時は2分ごとのSchedulerでactive1件を同期する。同期はattemptを増やさない。[掲載・同期の契約](generated-publication.md)

団体・運営画面では進捗バーと**学習終了までの目安**を表示する。実際の完了step数/総step数を使い、経過時間から残りを推定する。準備待ち・観測なし・仕上げ中は不定のバー、失敗・中止は停止し、ジョブ成功後だけ生成完了とする。ログが60秒以上古い場合や予測時刻を過ぎた場合はETAを表示しない。評価・保存の時間はETAに含めず、正確な全工程の完了時刻とは扱わない。

private managerだけが既存rangleログbucketの専用worker viewを読み、所有ジョブのprovider IDと時刻範囲を照合する。最大2行・3秒・10秒キャッシュで同時ポーリングを共有し、本文・URI・loss等の生ログは返さない。閲覧権限は[log view単位](https://docs.cloud.google.com/logging/docs/logs-views)・発表期限付きとし、公開runtimeやtrainerへログ参照権限を追加しない。観測失敗でもジョブ状態・中止・結果は利用でき、worker imageや学習条件は変えない。

入力URI/SHA256・方式・品質profile・image digest・GPU・期限はサーバーpresetへ固定し、公開要求からpath/command/profileを受け取らない。modeは `static` / `dynamic` / `live`、既存省略時はstatic。作成時snapshotを保存し、後の設定変更でjobの方式を変えない。`live` は録画入力ZIPと専用worker/publisherを使い、VOD掲載はできない。[クラウドLIVE](cloud-live.md)を参照。

公開Go APIは認証必須のgeneration managerへ転送し、managerが専用台帳・実行枠・固定spec・予算/期限を守る。公開runtimeに生成台帳直接アクセス・Vertex操作・trainer actAsを与えない。現在の運営/担当者認証とCSRFは公開runtime側の責任で、侵害されたruntimeから正規preset操作を行う脅威は残る。構成・IAM・署名検証・local modeは[生成インフラ](generation-infrastructure.md)を参照。

## 計算予算と失敗時

発表環境は同時1件・1件30分・最大10試行。固定T4/CPUに1試行$2を保守的予約し、GPU関連$20枠へ対応させる。請求実測・厳密なhard capではない。新規環境初回はattempts1、失敗/中止も返金しない。

Vertex作成timeoutでは自動再作成せずjob labelで照合する。不明なら実行枠を保持し運用確認を待つ。中止要求だけで解放せず、終端確認後に解放する。workerにも作成時刻からの絶対期限を渡す。

2026-11-14 15:00 UTC（11月15日00:00 JST）の停止処理は、このアプリのlabelを持つ東京jobだけを中止し、同期Schedulerをpauseする。Spotは空き不足/中断があり、即時起動・完了を保証しない。

## 学習と結果

入力と来歴は[共通前処理](dataset-preprocessing.md)、方式/制限は[学習コンテナ](../ml/container.md)・[4DGS](dynamic-gs-training.md)、profile/評価は[品質比較](training-quality.md)、保存形式/可逆圧縮は[連続再生](temporal-playback.md)・[圧縮比較](temporal-compression-comparison.md)を参照。

GCSは新規作成限定、`scene.json`を最後に公開する。成功結果は共有リンクで自由視点表示し、所有者が確認してから明示掲載する。未掲載/取り下げ後も共有と第三者の交流を使える。[固定path・bytes/hash・出典検証](generated-publication.md)に従う。私的な実験環境はこの実行経路・資格情報・配信基盤へ接続しない。

## 有効化と確認

配備・config・IAM・停止は[生成インフラ](generation-infrastructure.md)、既存台帳の専用DB移行は[移行手順](security-generation-migration.md)へまとめる。使用済みattempt・owner/snapshot/履歴/掲載・稼働jobを消して枠を戻さない。

実jobの終端・metrics・評価画像・配信bytesを確認後、同じ台帳のまま試行上限を増やす。無効化も既存job終了確認後に行う。CI rollbackはrevision/trafficを対象とし、DB/IAM/使用数は戻さない。新台帳へ書込開始後は旧DBのmonolithへ生成を戻さず、同じ専用台帳の互換revisionを使う。
