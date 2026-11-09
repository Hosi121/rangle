# アカウントと交流 API

ユーザーID・パスワードで登録／ログインする。閲覧・検索・共有はログイン不要、いいね・コメントの変更にはログインが必要。OAuth clientやメール送信の設定は不要。

| API | 用途 |
| --- | --- |
| `GET /api/v1/auth/me` | ログイン状態・機能の有効状態 |
| `POST /api/v1/auth/register` | `{username, name, password}` で登録・ログイン |
| `POST /api/v1/auth/login` | `{username, password}` でログイン |
| `POST /api/v1/auth/logout` | セッションを失効 |
| `GET /api/v1/clips/{id}/engagement` | 件数と自分のいいね状態 |
| `PUT` / `DELETE /api/v1/clips/{id}/likes` | 自分のいいねを設定／解除 |
| `GET /api/v1/clips/{id}/comments?cursor=...` | 新しい順に20件ずつ取得 |
| `POST /api/v1/clips/{id}/comments` | `{text, request_id}` で投稿 |
| `DELETE /api/v1/clips/{id}/comments/{comment}` | 自分のコメントを削除 |
| `GET` / `POST /api/v1/clips/{id}/cheers` | [連打できる声援と再生位置ごとの熱量](immersive-ui.md#声援と熱量バー) |

IDは半角英数字・`_`・`-`の3〜32文字で、大文字小文字を区別しない。表示名は任意・40文字以内、パスワードは15〜128文字。コメントは1〜500文字、`request_id`は32桁の小文字hex。同じ投稿の再送は同じIDを使い、二重投稿を防ぐ。

パスワードは個別salt付きArgon2id（19 MiB・2pass・1lane）、セッションは暗号学的乱数32byteのcookieを使い、保存先にはcookieのSHA256だけを置く。cookieはHttpOnly・Secure・SameSite=Lax、有効期間7日。ログアウト時は保存先から削除し、期限は読み取り時にも検査する。Go標準のCrossOriginProtectionで変更要求を保護し、認証の頻度・同時hash数・入力サイズを制限する。ログにパスワード・cookie・コメント本文を残さない。

クラウドの保存先は専用named Firestore。Goのruntime identityだけがアクセスし、ブラウザはGo APIを使う。カタログ／配信資産のキャッシュに個人の状態を混ぜず、交流APIは `Cache-Control: no-store`。いいねは1ユーザー1document、件数はaggregationで取得し、人気clipの共通counter documentを更新しない。10万人での性能・費用は未検証。

ローカルで一時的に試す場合は既存のserver起動引数に `-community-memory -insecure-local-cookies` を追加する。loopback限定で、再起動するとデータは消える。永続化には `-firestore-project=<project> -firestore-database=<database>` を使う。

Firestore emulatorの検証：

```bash
FIRESTORE_EMULATOR_HOST=127.0.0.1:8787 go test -race ./internal/community
```

emulator未指定時は外部DBへ接続せず、このintegration testだけskipする。アカウント再設定・メール確認・管理者モデレーションは今回の実装範囲に含めない。

## 管理アカウント

`admin` は一般登録で取得できない予約ID。管理者でログインすると、フィードの代わりにGS生成・LIVE・生成履歴の管理ページを表示する。通常ユーザーの視聴・交流機能は従来どおり利用できる。

生成APIはプリセット一覧も含めて管理者専用。権限はFirestoreのアカウントに保存し、管理操作のたびに読み直す。`/auth/me` とログイン応答の `user.is_admin` を画面の切り替えに使い、URLやユーザーIDだけでは権限を付与しない。ジョブの所有者チェック、CSRF対策、生成予算は維持する。

運用者は対象DBへのADC権限で次を実行する。パスワードは標準入力から読み、個別salt付きhashとして保存する。既存アカウントは上書きしない。信頼された管理者作成では8〜128文字を許可し、一般登録の15文字以上という条件は維持する。

```bash
read -r -s -p 'Administrator password: ' rangle_admin_password
printf '\n'
printf '%s\n' "$rangle_admin_password" | go run ./cmd/rangle-admin -project PROJECT_ID -database DATABASE_ID
unset rangle_admin_password
```

参照：[OWASP Password Storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)、[Go CrossOriginProtection](https://pkg.go.dev/net/http#CrossOriginProtection)、[Firestore aggregation](https://firebase.google.com/docs/firestore/query-data/aggregation-queries)。
