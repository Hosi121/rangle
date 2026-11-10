# 生成結果のフィード掲載UI

運営と団体担当者の共通画面を対象とする。団体担当者は[専用API](publisher-auth.md#団体の生成公開)へ接続し、履歴・公開操作を団体のスコープで行う。

「自分の生成」の成功結果から「フィードに掲載」「掲載を取り下げる」を操作し、掲載中は状態を表示する。生成中・失敗・中止では操作を出さず、新GPU jobを作らない。成功結果には、掲載前/取り下げ後も共有リンクで開けることを添える。

## APIとの契約

[session/CSRF](accounts.md)と[掲載API](generated-publication.md)を使う。

```http
PUT /api/v1/generation/jobs/{id}/publication
Content-Type: application/json

{"published":true}
```

取り下げは同じURLへ `{"published":false}`。応答は既存の `job/viewer_url/scene_url` で、履歴のpublished省略だけfalseとして読む。

団体担当者のURLは `/api/v1/publishers/{account}/generation/jobs/{id}/publication`。ログアウト・団体変更後は古い応答を反映せず、既存ジョブは担当者個人ではなく団体へ残す。

PUT成功はjob ID・成功状態・boolean publishedが要求と一致する場合だけ確定する。欠落/不一致は再試行可能なエラー。viewer URLは同一origin・generated clip形式を検証する。

操作中は結果操作を無効化する。応答喪失時は確認済み状態を維持し、同じbooleanを再送する。logout・owner変更・離脱後の旧世代応答は画面/callbackへ反映しない。

## フィードへ戻るとき

確認済み変更を `onPublicationChanged` で親へ伝え、戻るとrepository cache世代を無効化し、現在の検索/tagで先頭を再取得する。結果を開いて戻る操作は既存どおりgenerated clipへ移動する。

`ApiClipRepository` は次の先頭GETへ `Cache-Control: no-cache` を付ける。成功時だけ世代を検証済みにし、不正応答/失敗では再試行も同headerを使う。検証済み世代は単調増加し、遅い旧loadで巻き戻さない。公開URLは `limit/cursor/q/tag` のまま。

検証後は通常cacheへ戻す。変更なしの再入/復帰では追加取得せず、複数の掲載変更も復帰時に先頭から再検証する。生成画面を開く際に動画/GS controllerを破棄し、復帰時は新controllerで再入する。

## 検証と配備

APIテストはUTF-8 JSONとCompleter開始通知でcache世代・逆順完了raceを確認する。widget/API統合は掲載/取り下げ後の同URLへのno-cache取得各1回、変更なしの再入、通常cacheへの復帰、GS破棄/再入を確認する。

生成テストは状態/callback、busy、応答喪失/不正成功後の同意図PUT、logout後の遅延応答、既存生成/共有/結果表示を含む。実browser/スクリーンショットは未確認。

backendを先に配備し契約成立後にUIを配備する。実backend配備・本番掲載データ更新はこのUI検証に含めない。
