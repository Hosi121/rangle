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

担当者管理と団体用の生成・公開APIへ接続済み。認証のフロントUIは未接続。GoのAPI/race・Firestore emulator、Terraform mockで検証し、実ブラウザ・本番担当者アカウントの作成は行っていない。

## 担当者の管理

| API | 団体担当者の経路 |
| --- | --- |
| `GET /api/v1/publishers/{account}/staff?cursor=...` | 団体管理者が20件ずつ一覧を取得。担当者IDの昇順 |
| `POST /api/v1/publishers/{account}/staff` | 団体管理者が `{username, password, role}` で担当者を追加 |
| `PUT /api/v1/publishers/{account}/staff/{staff}` | 団体管理者が `{role, disabled}` を設定。両方必須 |

運営は同じ操作を `/api/v1/operations/publishers/{account}/staff[/{staff}]` から行う。運営Cookieと担当者Cookieを相互の代用にせず、経路で認証主体を明確にする。団体の投稿担当 `editor` は担当者管理を行えない。他団体を指定した要求は拒否し、一覧cursorも団体に結び付ける。

作成時は担当者名の一意性をtransactionで検査する。変更時は団体・操作主体・対象担当者を同じtransactionで読み、現在の役割と資格情報世代を再検査する。受付後に管理者権限を失った要求もコミットしない。権限・無効化状態の変更では資格情報世代を進め、古いセッションを失効させる。同じ設定の再送では世代を進めない。

最後の有効な管理者も無効化できる。団体と動画は残し、運営が別の管理者を作成して引継ぐ。担当者名・IDは上書き・再割当せず、無効化した担当者は監査のため残す。団体削除・資格情報再設定・一般向け登録・認証/管理UIは別段階。

## 団体の生成・公開

団体担当者は `/api/v1/publishers/{account}/generation` 配下を利用する。`administrator` と `editor` が使え、毎回現在の団体・担当者・資格情報世代を検査する。別団体のURL・ジョブは404。一般ユーザーや運営のCookieで代用できない。既存 `/api/v1/generation` は運営用ジョブの経路として維持する。

| API（上記prefixからの相対パス） | 操作 |
| --- | --- |
| `GET /presets` | 当該団体に許可された入力だけを表示 |
| `POST /jobs` | `{preset, request_id}` で生成。再送キーは団体単位 |
| `GET /jobs`・`GET /jobs/{job}` | 団体が所有するジョブを参照 |
| `DELETE /jobs/{job}` | 団体のジョブを取消 |
| `PUT /jobs/{job}/publication` | `{published: true/false}` で公開・取り下げ |

運営がpresetの `publisher_accounts` へ団体IDを設定するまで入力は表示・受付されない。作成者が脱退してもジョブは残り、他の有効な担当者が引き継ぐ。全団体・運営で既存のGPU予算・同時実行枠・期限を共有する。公開時の資産検証、公開済みフィードと限定しない既存の生成結果deep linkの挙動は維持する。

公開runtimeは認証結果を短寿命の `X-Rangle-Principal` としてprivate managerへ送る。managerはCloud Run IAMと、指定runtimeのGoogle署名・audienceを検証してから採用する。ブラウザの同名headerやCookieは転送しない。旧runtime用 `X-Rangle-Owner` は運営IDに限って受け、両headerの併記は拒否する。managerを先に更新することで旧runtimeも動く。新しいDB/IAM権限や共有secretは追加しない。失効は新規リクエストの受付で反映し、既に受付済みの操作までDBをまたいで巻き戻す仕組みではない。
