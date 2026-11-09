# 公開フィードの継続取得

Flutter の `ClipPagingController` は `PagedClipRepository.loadPage` を使い、20件ずつ取得する。既存 `ClipRepository.load` と `ApiClipRepository(endpoint:, client:)` は互換性を保つ。旧 `load` は従来どおり先頭50件（共有clipの補完を含む）を返し、画面は新しいpaging契約を使う。

## 取得と復旧

- 初回の後は、取得済み末尾2件へ移動したとき、末尾の次ボタン/キー操作、または「続きを読み込む」で1ページだけ取得する。取得完了から次の取得を自動連鎖させない。スクロール/ボタン連打は同じfutureを共有する。
- `next_cursor` は最大128文字の不透明な値として保持し、そのまま `cursor` に渡す。返されないときだけ全件の終端になる。空ページにcursorがあれば「続きを読み込む」で進める。進まない同一cursorは可視のエラーにする。
- 失敗しても成功済みページは維持する。「続きを再試行」は失敗したcursorだけを取り直す。通常の移動では失敗を自動再試行しない。初回失敗にも手動再試行を用意する。
- 継続ページの400はcatalog/origin更新によるcursor失効として、現在のquery/tag/共有clipを保ち先頭を1回だけ自動再取得する。旧snapshotのメタデータは破棄する。同じ明示更新・検索の世代で再度400になったら、そのページの手動再試行を待つ。先頭ページの400は自動再取得しない。
- 検索、tag、共有deep link、repositoryの変更、明示更新は新しい世代にする。前世代のHTTP requestをabortし、キャンセルに対応しないrepositoryから遅れて届いた成功・失敗も世代比較で破棄する。

API URLは公開の `limit=20`、必要な `cursor/q/tag` だけで構成する。利用者ID、nonce、時刻、cache bust queryを追加しない。共有clipの詳細取得は最初のページだけで、cursorの進行には影響しない。

## 保持上限と操作

永続的に保持するmetadataと重複検出用IDは最大100件。要求中の1ページ（20件、初回共有clip補完時21件）と検証用の一時リストは別に存在するが、catalog全体には比例しない。古いpage/cursor履歴や全件ID集合は蓄積しない。保有cursorは次の1つだけ（共有clipのIDは1つ保持）で、後方の再取得cacheは持たない。

追加ページで100件を超える分だけ、選択中clipより前の既読prefixを破棄する。まだ安全に破棄できないときは取得を保留し、`hasMore`を維持する。上限を全件終端として扱わない。ユーザーが先へ進むと再び取得できる。

PageViewの番号は絶対indexのまま保ち、100件を超えても選択clipや既存card/controllerのindexをずらさない。前ボタンとスクロール下限を保持windowの先頭に合わせる。このため100件より古い閲覧履歴には戻れない。先頭へ戻すにはページ再読み込みか検索条件の変更を行う。この前進windowは、上限で閲覧を打ち切る方式や、毎回PageViewを作り直す方式を避け、最後のページまで継続しつつ選択中の再生/GSを維持するために採用した。

IDの重複は保持window内と受信ページ内で除く。先頭に補完した共有clipのIDはwindowを離れた後も再表示しない。evict済みIDの全履歴は保持しないため、API snapshotの一覧が一意なIDを順に返す契約に依存する。失効時は全windowを捨てて新snapshotを先頭から始める。

件数表示の `+` は続きがあることを示す（例：`20 / 40+`）。100件の上限をcatalog総数として表示しない。evictによるページ番号の巻き戻しはない。

## mediaと検証

ページ取得はJSON metadataだけを扱う。動画・scene・Gaussianの全件先読みを追加せず、既存の選択中cardだけがmedia controllerを持つ。単なるappendではfeedの世代keyを変えず、GSモード・再生位置・選択を維持する。検索やstale snapshotの先頭再取得時は新しいfeedを表示する。

自動テストは55件のページ走査、240件を取得した100件window、single-flight、ページ再試行、400の復旧上限、query/tag/deep-link世代、HTTP abort、canonical URL、連続操作、GS/controller/時刻保持、後方scroll制限を検証する。実ブラウザ操作・スクリーンショット確認は実施していない。
