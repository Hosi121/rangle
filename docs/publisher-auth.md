# 団体担当者の認証

一般ユーザーのログインと、団体配下の担当者ログインを分離する。旧adminはrangle運営。最初の団体と管理者は運営だけが作成でき、一般登録で団体権限を得ることはできない。

| API | 認証・入力 |
| --- | --- |
| `POST /api/v1/operations/publishers` | 運営。`id, name, kind, staff_username, staff_password` で団体と最初の管理者を同時に作る |
| `GET /api/v1/operations/publishers?cursor=...` | 運営。団体のプロフィール・状態をID順に20件ずつ取得 |
| `POST /api/v1/publisher-auth/login` | `account_id, username, password`。団体IDと団体内の担当者名でログイン |
| `GET /api/v1/publisher-auth/me` | 現在の団体・担当者。未ログインは `identity: null` |
| `POST /api/v1/publisher-auth/logout` | 担当者セッションを失効しCookieを削除 |

団体IDは小文字英数字・`_`・`-`で先頭は英数字、1〜64文字。`kind` は `company` / `organization`。担当者名は小文字化した英数字・`_`・`-`の3〜32文字、パスワードは15〜128文字。団体作成はcreate-onlyで、既存団体や担当者を上書きしない。

保存先は既存community DB内の `publisher_accounts/{account}`、その配下の `staff_accounts/{staff}` と名前索引 `staff_names/{username}`。団体・最初の担当者・名前索引を同じtransactionで作る。担当者IDは団体内の識別子であり、他団体で同じIDや担当者名が使われても同じ主体とみなさない。

パスワードは既存と同じArgon2id。応答にhash・salt・内部状態を含めない。Cookieは `rangle_staff_session`、保存先は `staff_sessions/{sha256_cookie}` とし、一般ユーザーのCookie・sessions collectionと相互利用できない。HttpOnly・Secure・SameSite=Lax、7日で失効、ログインで旧セッションを削除する。役割はセッションに保存せず、毎回現在の団体・担当者を読む。資格情報の世代も照合するため、世代更新後に古いセッションは復活しない。

CSRF・本文上限・同時hash数・ログイン頻度制限を適用する。Cloud Armorの既存auth制限へ担当者loginを追加し、`staff_sessions.expires_at` のTTLとindex免除をIaC定義へ追加した。TTLの削除を待たずGoで期限を検査する。別DB・IAM権限は追加しない。IaCの実適用は別途確認する。

担当者管理と団体用の生成・公開APIへ接続済み。[団体担当者の画面](https://app.rangleplay.stream/demo/?publisher=1)は、フィードのアカウントから「団体担当者のログイン」でも開ける。団体ID・担当者名・パスワードでログインし、団体の生成履歴・取消・公開を扱う。アカウントメニューから本人のパスワード変更とログアウト、団体管理者は担当者管理へ進む。

運営は既存の管理画面から「団体管理」を開き、団体登録・担当者管理・パスワード再設定・入力割当を行う。画面の役割表示は操作の案内に使い、認可は毎回APIが検査する。GoのAPI/race・Firestore emulatorとFlutterのAPI/widgetテストで確認し、実ブラウザ操作は利用者が確認する。

## 公開プロフィール・フォロー

| API | 操作 |
| --- | --- |
| `GET /api/v1/publishers/{account}` | 認証不要。利用中の団体のID・名前・種別 |
| `GET /api/v1/clips?publisher={account}` | 団体IDの完全一致で公開カタログを絞る。既存のページ送り・検索を併用できる |
| `GET/PUT/DELETE /api/v1/publishers/{account}/subscription` | 一般ユーザー本人の無料フォロー状態・登録・解除。本文不要 |
| `GET /api/v1/me/subscriptions?cursor=...` | 本人のフォロー中団体。団体ID順に20件ずつ取得 |

フォローは `accounts/{user}/publisher_subscriptions/{account}` に保存する。担当者の所属や投稿権限を与えず、課金・通知は行わない。PUT・DELETEは冪等。登録transactionで現在の団体を再検査し、停止・削除中・削除済みの団体はプロフィールとフォロー一覧に表示しない。削除後も本人による解除は可能。cursorは本人に結び付け、一覧は最大21件とプロフィールのbatch読取で処理する。

団体ページは `/demo/?organization={account}`。動画の「概要」→「投稿者」からも開ける。一般ユーザーはページからフォロー・解除し、アカウントの「フォロー中の団体」から戻れる。公開動画を選ぶとフィードで再生する。

## 担当者の管理

| API | 団体担当者の経路 |
| --- | --- |
| `GET /api/v1/publishers/{account}/staff?cursor=...` | 団体管理者が20件ずつ一覧を取得。担当者IDの昇順 |
| `POST /api/v1/publishers/{account}/staff` | 団体管理者が `{username, password, role}` で担当者を追加 |
| `PUT /api/v1/publishers/{account}/staff/{staff}` | 団体管理者が `{role, disabled}` を設定。両方必須 |

運営は同じ操作を `/api/v1/operations/publishers/{account}/staff[/{staff}]` から行う。運営Cookieと担当者Cookieを相互の代用にせず、経路で認証主体を明確にする。団体の投稿担当 `editor` は担当者管理を行えない。他団体を指定した要求は拒否し、一覧cursorも団体に結び付ける。

作成時は担当者名の一意性をtransactionで検査する。変更時は団体・操作主体・対象担当者を同じtransactionで読み、現在の役割と資格情報世代を再検査する。受付後に管理者権限を失った要求もコミットしない。権限・無効化状態の変更では資格情報世代を進め、古いセッションを失効させる。同じ設定の再送では世代を進めない。

最後の有効な管理者も無効化できる。団体と動画は残し、運営が別の管理者を作成して引継ぐ。担当者名・IDは上書き・再割当せず、無効化した担当者は監査のため残す。団体のセルフ登録は設けない。

## パスワード変更・再設定

本人は `PUT /api/v1/publisher-auth/password` に `{current_password, new_password}` を送り、現在のパスワードを確認して変更する。団体管理者は `PUT /api/v1/publishers/{account}/staff/{staff}/password`、運営は同じ操作を `/api/v1/operations/publishers/{account}/staff/{staff}/password` から `{password}` で再設定できる。未ログインのID指定だけで再設定する経路は設けない。

既存と同じArgon2id・本文/同時hash数/頻度制限を使う。DB transactionで現在の操作主体・役割・資格情報世代を再検査し、新しいsalt/hashと世代を同時に更新する。変更前の全セッションは失効し、本人の変更後は再ログインする。再設定で担当者のID・役割・無効化状態や団体のジョブは変えない。パスワード・hash・salt・要求本文を応答やログへ出さない。

## 団体の生成・公開

団体担当者は `/api/v1/publishers/{account}/generation` 配下を利用する。`administrator` と `editor` が使え、毎回現在の団体・担当者・資格情報世代を検査する。別団体のURL・ジョブは404。一般ユーザーや運営のCookieで代用できない。既存 `/api/v1/generation` は運営用ジョブの経路として維持する。

| API（上記prefixからの相対パス） | 操作 |
| --- | --- |
| `GET /presets` | 当該団体に許可された入力だけを表示 |
| `POST /jobs` | `{preset, request_id}` で生成。再送キーは団体単位 |
| `GET /jobs`・`GET /jobs/{job}` | 団体が所有するジョブを参照 |
| `DELETE /jobs/{job}` | 団体のジョブを取消 |
| `PUT /jobs/{job}/publication` | `{published: true/false}` で公開・取り下げ |

運営は `GET/PUT /api/v1/operations/publishers/{account}/generation/presets` で入力割当を確認・設定する。PUTは `{presets: ["preset-id", ...]}` で全件置換し、空配列で解除する。確認済みpresetのIDだけを受け付け、任意の入力URIや学習条件は設定できない。

割当はmanagerの専用DBへ保存し、未設定時だけconfigの `publisher_accounts` を使う。明示した空配列はconfigの許可も無効にする。新規生成の台帳transactionでも現在の割当を再検査する。解除後も既存ジョブの参照・取消・再送は維持する。作成者が脱退してもジョブは残り、他の有効な担当者が引き継ぐ。全団体・運営で既存のGPU予算・同時実行枠・期限を共有する。公開時の資産検証と既存deep linkの挙動は維持する。

## 団体削除

団体管理者は `GET/PUT /api/v1/publishers/{account}/deletion-request` で要求を確認・変更する。PUTは `{requested: true|false, confirmation: "団体ID"}`。要求だけでは利用を停止しない。rangle運営が `DELETE /api/v1/operations/publishers/{account}/deletion` に `{confirmation: "団体ID"}` を送り、実行する。同じURLのGETで進捗を確認・再開できる。削除中からの復帰・IDの再利用は行わない。

団体管理者はアカウントメニューの「団体の削除」から依頼・撤回し、運営は「団体管理」の対象団体から確定する。団体IDの一致を確認して送信し、削除中は状態を定期確認する。画面を閉じた場合も「削除の状況」から再開できる。個人の声援履歴では、削除済み動画の行と日時を残し、再生操作を無効にする。

公開サーバーはcommunity DBの状態と削除要求ID、private managerは所有ジョブと処理段階を保存する。削除開始は生成受付と同じ団体別文書をtransactionで更新し、所有ジョブを確定して公開registryから除外する。新しい生成・公開・担当者操作を拒否し、生成済み動画の詳細・交流APIも参照不能にする。遅れて届いた受付も対象から漏れない。

処理はジョブ終了確認 → 所有資産削除 → CDN無効化。受付結果が不明なGPUジョブは終了扱いにせず、照合・取消を待つ。`generated/{job}/`、`live/{job}/`、`live-index/{job}.json` だけを導出し、各オブジェクトのgeneration一致で削除する。再一覧で空を確認し、固定media hostのキャッシュ無効化がDONEになるまで完了にしない。進捗の世代照合と固定requestIdで並行ポーリング・応答消失に対応する。管理画面の確認と既存の認証済みschedulerが有限量ずつ進める。

完了時は動画の削除印を残す。声援・コメント・個人履歴・担当者とジョブの監査記録は保持し、履歴は「削除済み動画」として再生できなくする。LIVEの一覧からも除外する。共有の元データ・試合・選手・チーム・会場、旧運営所有のジョブは対象外。GCSのsoft-delete保持期間や取得済みのブラウザ内コピーは別管理であり、物理消去の完了を意味しない。

managerへ追加する権限はmedia bucketの一覧、`generated/`・`live/`・`live-index/` の削除、CDN無効化とoperation参照のみ。公開runtimeやtrainerには付けない。URL mapのIAM Conditionsではresource.nameを使えないため、CDNの2権限はproject単位・既存期限付きとし、アプリがURL map/host/所有キーを固定する。バケット復元・作成・LB変更権限は付けない。[GCSのIAM](https://docs.cloud.google.com/storage/docs/access-control/iam)、[IAM条件の対象資源](https://docs.cloud.google.com/iam/docs/conditions-resource-attributes)、[CDN無効化API](https://docs.cloud.google.com/compute/docs/reference/rest/v1/urlMaps/invalidateCache)を参照。

公開runtimeは認証結果を短寿命の `X-Rangle-Principal` としてprivate managerへ送る。managerはCloud Run IAMと、指定runtimeのGoogle署名・audienceを検証してから採用する。ブラウザの同名headerやCookieは転送しない。旧runtime用 `X-Rangle-Owner` は運営IDに限って受け、両headerの併記は拒否する。managerを先に更新することで旧runtimeも動く。新しいDB/IAM権限や共有secretは追加しない。失効は新規リクエストの受付で反映し、既に受付済みの操作までDBをまたいで巻き戻す仕組みではない。
