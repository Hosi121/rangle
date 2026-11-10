# アカウントと交流 API

閲覧・検索・共有はログイン不要。ユーザーID・パスワードで登録／ログインし、いいね・コメントを変更する。OAuth client・メール送信の設定は不要。

| API | 用途 |
| --- | --- |
| `GET /api/v1/auth/me` | ログイン状態・機能の有効状態 |
| `POST /api/v1/auth/register` | `{username, name, password}` で登録・ログイン |
| `POST /api/v1/auth/login` | `{username, password}` でログイン |
| `POST /api/v1/auth/logout` | セッション失効 |
| `GET /api/v1/clips/{id}/engagement` | 件数と自分のいいね状態 |
| `PUT` / `DELETE /api/v1/clips/{id}/likes` | 自分のいいねを設定／解除 |
| `GET /api/v1/clips/{id}/comments?cursor=...` | 新しい順に20件ずつ取得 |
| `POST /api/v1/clips/{id}/comments` | `{text, request_id}` で投稿 |
| `DELETE /api/v1/clips/{id}/comments/{comment}` | 自分のコメントを削除 |
| `GET` / `POST /api/v1/clips/{id}/cheers` | [声援と再生位置ごとの熱量](immersive-ui.md#声援と熱量バー)。2026-11-10調査で公開router未接続を発見。修正/配備は[#127](https://github.com/Hosi121/rangle/issues/127)で追跡 |

2026-11-10の調査時点では、[community handler](../internal/community/handler.go)にある声援APIが公開[application router](../cmd/rangle-server/application.go)へ未登録で、既存clipへのGETも404だった。公開POSTの書き込み確認は行っていない。既存実装を公開経路へ接続し、認証/CSRF・同じrequest_idの再送・件数上限をapplication経由で確認する。現在の修正と配備段階は#127を正本とする。

| 入力 | 制約 |
| --- | --- |
| ID | 半角英数字・`_`・`-`の3〜32文字。大小文字を区別しない |
| 表示名 | 任意、40文字以内 |
| パスワード | 15〜128文字 |
| コメント | 1〜500文字 |
| `request_id` | 32桁の小文字hex。同じ投稿の再送は同じIDで二重投稿を防ぐ |

パスワードは個別salt付きArgon2id（19 MiB・2pass・1lane）。セッションは暗号学的乱数32byteのcookieで、保存先にはSHA256だけを置く。HttpOnly・Secure・SameSite=Lax、有効期間7日。ログアウトで削除し、読取時にも期限を検査する。変更要求はGo標準のCrossOriginProtectionで保護し、認証頻度・同時hash数・入力サイズを制限する。ログへパスワード・cookie・コメント本文を残さない。

保存先は専用named Firestoreで、Goのruntime identityだけがアクセスする。ブラウザはGo APIを使う。交流APIは `Cache-Control: no-store` とし、catalog／資産cacheに個人状態を混ぜない。いいねは1ユーザー1document、件数はaggregationで取得し、共通counterを更新しない。10万人での性能・費用は未検証。

ローカル一時利用は既存起動引数へ `-community-memory -insecure-local-cookies` を追加する（loopback限定、再起動で消去）。永続化は `-firestore-project=<project> -firestore-database=<database>` を使う。

```bash
FIRESTORE_EMULATOR_HOST=127.0.0.1:8787 go test -race ./internal/community
```

emulator未指定時は外部DBへ接続せず、このintegration testだけskipする。アカウント再設定・メール確認・管理者モデレーションは未実装。

## 管理アカウント

`admin` は一般登録できない予約ID。管理者ログインではフィードの代わりにGS生成・LIVE・履歴の管理ページを表示する。通常ユーザーは視聴・交流を利用する。

生成APIはプリセット一覧も管理者専用。管理操作ごとにFirestoreの権限を読み直し、URLやユーザーIDで権限を付与しない。画面は `/auth/me` とログイン応答の `user.is_admin` で切り替える。job所有者チェック・CSRF・生成予算も維持する。

運用者は対象DBへのADC権限で次を実行する。パスワードは標準入力から読み、個別salt付きhashで保存する。既存アカウントは上書きしない。新規管理者は15〜128文字の有効なUTF-8・最大512byteが必須。既存管理者のパスワードと有効なセッションは変更しない。

```bash
read -r -s -p 'Administrator password: ' rangle_admin_password
printf '\n'
printf '%s\n' "$rangle_admin_password" | go run ./cmd/rangle-admin -project PROJECT_ID -database DATABASE_ID
unset rangle_admin_password
```

参照：[OWASP Password Storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)、[Go CrossOriginProtection](https://pkg.go.dev/net/http#CrossOriginProtection)、[Firestore aggregation](https://firebase.google.com/docs/firestore/query-data/aggregation-queries)。
