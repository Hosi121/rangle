# 団体担当者の認証

一般ユーザーのログインと、団体配下の担当者ログインを分離する。旧adminはrangle運営。最初の団体と管理者は運営だけが作成でき、一般登録で団体権限を得ることはできない。

| API | 認証・入力 |
| --- | --- |
| `POST /api/v1/operations/publishers` | 運営。`id, name, kind, staff_username, staff_password` で団体と最初の管理者を同時に作る |
| `POST /api/v1/publisher-auth/login` | `account_id, username, password`。団体IDと団体内の担当者名でログイン |
| `GET /api/v1/publisher-auth/me` | 現在の団体・担当者。未ログインは `identity: null` |
| `POST /api/v1/publisher-auth/logout` | 担当者セッションを失効しCookieを削除 |

団体IDは小文字英数字・`_`・`-`で先頭は英数字、1〜64文字。`kind` は `company` / `organization`。担当者名は小文字化した英数字・`_`・`-`の3〜32文字、パスワードは15〜128文字。団体作成はcreate-onlyで、既存団体や担当者を上書きしない。

保存先は既存community DB内の `publisher_accounts/{account}`、その配下の `staff_accounts/{staff}` と名前索引 `staff_names/{username}`。団体・最初の担当者・名前索引を同じtransactionで作る。担当者IDは団体内の識別子であり、他団体で同じIDや担当者名が使われても同じ主体とみなさない。

パスワードは既存と同じArgon2id。応答にhash・salt・内部状態を含めない。Cookieは `rangle_staff_session`、保存先は `staff_sessions/{sha256_cookie}` とし、一般ユーザーのCookie・sessions collectionと相互利用できない。HttpOnly・Secure・SameSite=Lax、7日で失効、ログインで旧セッションを削除する。役割はセッションに保存せず、毎回現在の団体・担当者を読む。資格情報の世代も照合するため、世代更新後に古いセッションは復活しない。

CSRF・本文上限・同時hash数・ログイン頻度制限を適用する。Cloud Armorの既存auth制限へ担当者loginを追加し、`staff_sessions.expires_at` のTTLとindex免除をIaC定義へ追加した。TTLの削除を待たずGoで期限を検査する。別DB・IAM権限は追加しない。IaCの実適用は別途確認する。

担当者管理、団体による生成・公開APIの利用は次の接続段階。今回の認証だけでは生成APIや運営専用previewへのアクセスは増えない。認証のフロントUIは未接続。GoのAPI/race・Firestore emulator、Terraform mockで検証し、実ブラウザ・本番担当者アカウントの作成は行っていない。
