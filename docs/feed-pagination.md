# 公開フィードの継続取得

Flutterの `ClipPagingController` は `PagedClipRepository.loadPage` で20件ずつ取得する。旧 `ClipRepository.load` と `ApiClipRepository(endpoint:, client:)` は互換を保ち、旧loadは先頭50件（共有clip補完を含む）を返す。

## 取得と復旧

| 状況 | 動作 |
| --- | --- |
| 続き | 末尾2件への移動、末尾の次ボタン/キー、「続きを読み込む」で1ページ取得。完了後の自動連鎖なし。連打は同じfutureを共有 |
| cursor | 最大128文字、そのまま返す。省略時だけ終端。空page+cursorは手動で続行。同一cursorで進まなければ可視エラー |
| 失敗 | 成功pageを保持し、手動で失敗cursorだけ再試行。通常移動では自動retryしない。初回も手動retry可能 |
| 継続400 | query/tag/共有clipを保ち、旧windowを捨て先頭を1回だけ自動再取得。同じ更新/検索世代の再400は手動待ち。先頭400は自動再取得なし |
| 世代変更 | 検索/tag/deep link/repository変更・明示更新で旧HTTPをabort。非対応repositoryの遅延成功/失敗も世代で破棄 |

URLは公開の `limit=20` と必要な `cursor/q/tag` だけ。利用者ID・nonce・時刻・cache bustを付けない。共有clip詳細は初回だけ補完し、cursorへ影響させない。

## 保持上限と操作

metadata/重複検出IDは最大100件。要求中1page（20件、初回共有clip補完時21件）と一時検証listは別枠で、catalog全体に比例しない。次cursor1つと共有clip ID1つを保持し、古いpage/cursor履歴・全件ID集合・後方再取得cacheを持たない。

100件超過分だけ選択clipより前の既読prefixを破棄する。安全に破棄できなければ取得を保留し `hasMore` を維持する。進めば再取得でき、上限を終端にしない。

PageViewは絶対indexを保ち、選択clip・既存card/controllerをずらさない。前ボタン/scroll下限はwindow先頭に合わせるため100件以前へ戻れない。先頭へ戻すにはreloadか検索変更を使う。window方式は最後まで読み進めながら選択中の再生/GSを維持するために採用した。

重複はwindow内・受信page内で除く。補完した共有clipはevict後も再表示しない。その他のevict済みIDは記録せず、API snapshotの一意なID順序に依存する。失効時は全windowを捨てる。件数の `+` は続き（例 `20 / 40+`）を示し、100件を総数とせず、evictでも番号を戻さない。

## mediaと検証

pagingはJSONだけで、動画/scene/Gaussianを全件先読みしない。選択cardだけがmedia controllerを持ち、単なるappendではfeed世代key・GS・再生位置・選択を維持する。検索/失効からの再取得では新feedへ移る。

自動テストは55件走査、240件取得と100件window、single-flight、retry/400上限、query/tag/deep-link世代、HTTP abort、canonical URL、連続操作、GS/controller/時刻保持、後方scroll制限を確認する。実browser/スクリーンショット確認は未実施。
