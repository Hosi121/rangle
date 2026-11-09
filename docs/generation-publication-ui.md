# 生成結果のフィード掲載UI

生成画面の「自分の生成」から、成功した結果を「フィードに掲載」し、「掲載を取り下げる」で戻せる。掲載中は「フィードに掲載中」と表示する。生成中・失敗・中止したjobには掲載操作を出さない。掲載操作で新しいGPU jobを作ることはない。

結果のリンク共有とフィード掲載は独立している。リンクを知っている人は、掲載前や取り下げ後も同じ結果を開ける。この説明を成功した結果の操作に添える。

## APIとの契約

既存のcookie sessionとauth/CSRF検証を使う。

```http
PUT /api/v1/generation/jobs/{id}/publication
Content-Type: application/json

{"published":true}
```

取り下げは同じURLへ `{"published":false}` を送る。応答wrapperの `job` / `viewer_url` / `scene_url` は既存の生成結果と同じ形を使い、jobの `published` が現在の掲載状態を表す。旧履歴でフィールドが省略された場合はfalseとして読む。

掲載PUTの成功を認めるには、応答jobのID、成功状態、booleanの `published` が要求と一致する必要がある。欠落・不一致は再試行できるエラーとし、掲載状態を確定しない。viewer URLには既存の同一origin・生成clip形式の検証を適用する。

操作中は結果操作を無効にし、同じjobへの重複操作を防ぐ。応答を失った場合は確認済みのjob状態を維持するため、再試行でも同じbooleanのPUTを送る。ログアウト・owner変更・画面離脱後に届いた旧世代の応答は画面やcallbackへ反映しない。

## フィードへ戻るとき

確認できた掲載変更は `onPublicationChanged` で親画面へ伝える。生成画面から戻るとrepositoryのcache世代を無効化し、現在の検索条件・タグでフィードの先頭を取り直す。結果を開いて戻る既存動作は生成clipへ移動する。

`ApiClipRepository` は次の先頭ページに `Cache-Control: no-cache` を付け、成功してからそのcache世代を検証済みにする。公開URLは従来の `limit/cursor/q/tag` のまま維持する。失敗・不正な応答では再検証を消費せず、再試行にも同じheaderを付ける。検証済み世代は単調増加とし、新しいloadの完了後に古いloadが完了しても巻き戻さない。

一度検証できた後は通常のcache動作へ戻す。掲載変更のない生成画面への再入・戻る操作だけでは追加のAPI取得を行わない。掲載・取り下げを複数回行ってから戻る場合も、戻った時点で先頭から再検証する。

生成画面を開くと既存の動画・GS controllerを破棄する。戻ったフィードでは新しいcontrollerで再入できる。生成画面内で非表示のGSを保持し続ける処理は追加していない。

## 検証と配備

APIテストはUTF-8 JSONを使い、cache invalidationと完了順が逆転するraceをCompleterの開始通知で再現する。widget/API統合テストは、掲載と取り下げから戻る操作で同じ公開URLへ一度ずつno-cache取得すること、変更のない再入時に取得しないこと、通常cacheへ戻ること、GS破棄・再入を確認する。

生成テストには成功時の状態・callback、busy、応答喪失・不正な成功応答後の同意図PUT、logout後の遅延応答、既存の生成・共有・結果表示を含む。実ブラウザ操作・スクリーンショット確認は行わない。

掲載backendを先に配備し、そのAPI契約を満たした後にUIを配備する。backendの配備と本番の掲載データ更新は、このUI変更の検証には含めない。
