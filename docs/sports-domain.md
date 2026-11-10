# 視聴者・投稿者・試合

個人の `User` と団体の `PublisherAccount` は別のアカウントモデルとする。個人は視聴・声援・コメントを行い、投稿主体は企業・団体のアカウントに限定する。担当者は個人として認証し、`Membership` で操作できる団体に結び付ける。

| 概念 | 意味 |
| --- | --- |
| User | 個人のログイン・プロフィール・声援履歴。投稿権限を直接持たない |
| PublisherAccount | 企業・団体の投稿アカウント。個人とは別のID・公開名を持つ |
| Membership | 個人IDと団体アカウントIDを結ぶ。owner / editor が団体として投稿を操作する |
| Event | 試合・練習・デモ。大会、開催日時、会場、参加チームを持つ |
| Athlete / Team / Venue / Competition | 種別ごとの安定IDと表示名。選手の所属を永久属性にしない |
| Clip context | 投稿時点の上記情報。選手はそのクリップに登場する選手 |
| Cheer history | 本人が声援を送ったクリップと最後の送信日時。公開プロフィールとは分離 |

`Clip.context.publisher.id` は団体アカウントIDを指し、個人IDや生成jobの `Owner` とは区別する。公開映像の帰属は団体、操作・監査の主体はログインした個人とする。1団体に複数担当者、1人に複数団体を許容する。団体のパスワードを担当者間で共有する方式にはしない。

Goは `publisher.Account`・専用ID型・`Membership`、Flutterは `PublisherAccount` として独立させる。公開プロフィールやセッションの表示用flagは所属の証拠にしない。投稿時にはサーバーが現在の所属を読み直す。`Account.CanPublish` はこの境界のドメインルールであり、現時点ではHTTPやDBに接続していない。

初版はcatalogと運営確認済みの生成presetに任意の `context` を設定する。DBのマスター編集画面を増やさず、公開時のスナップショットとして保存する。同じ設定内で同じ種別・IDに異なる情報があれば拒否する。既存の不明な選手・会場・投稿者は未設定のままにする。

公開clipの `context` は[架空の入力例](../testdata/sports/context.json)と同じ形。投稿団体、試合・大会、選手、チーム、会場名・都市を `GET /api/v1/clips?q=...` の検索対象に含める。「概要」の各名前からも検索できる。既存のタグ・cursor・版付き資産URLは維持する。

`context` は団体アカウントの公開時点の表示情報で、所属や権限情報を含めない。モデル上の保存先は個人の `accounts/{username}` と団体の `publisher_accounts/{id}` を分離し、所属は団体配下に置く。団体・所属の永続化、団体登録・審査、招待・切替UI、投稿APIの所属確認への接続は次の段階。現行の設定・生成・公開APIは既存の管理者制限を維持する。

本人の[声援履歴](accounts.md#声援の履歴)は交流DBへ保存し、公開catalogに個人状態を混ぜない。実装の検証はGoのrace付きテスト、Firestore emulator、FlutterのAPI/Widgetテストで行う。実ブラウザでの操作・クラウド負荷は別途確認する。
