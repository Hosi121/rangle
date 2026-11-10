# アカウントと交流 API

閲覧・検索・共有はログイン不要。ユーザーID・パスワードで登録／ログインし、いいね・声援・コメントを送る。[声援履歴](#声援の履歴)はアカウント画面から開く。OAuth client・メール送信の設定は不要。

| API | 用途 |
| --- | --- |
| `GET /api/v1/me/cheered-clips?cursor=...` | 本人の声援履歴を新しい順に20件ずつ取得 |
| `GET /api/v1/auth/me` | ログイン状態・機能の有効状態 |
| `POST /api/v1/auth/register` | `{username, name, password}` で登録・ログイン |
| `POST /api/v1/auth/login` | `{username, password}` でログイン |
| `POST /api/v1/auth/logout` | セッション失効 |
| `GET /api/v1/clips/{id}/engagement` | 件数と自分のいいね状態 |
| `PUT` / `DELETE /api/v1/clips/{id}/likes` | 自分のいいねを設定／解除 |
| `GET /api/v1/clips/{id}/comments?cursor=...` | 新しい順に20件ずつ取得 |
| `POST /api/v1/clips/{id}/comments` | `{text, request_id}` で投稿 |
| `DELETE /api/v1/clips/{id}/comments/{comment}` | 自分のコメントを削除 |
| `GET` / `POST /api/v1/clips/{id}/cheers` | [声援と再生位置ごとの熱量](immersive-ui.md#声援と熱量バー) |


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

emulator未指定時は外部DBへ接続せず、このintegration testだけskipする。一般ユーザーのパスワード再設定・メール確認・管理者モデレーションは未実装。団体担当者の変更・再設定は[専用API](publisher-auth.md#パスワード変更再設定)を使う。

## 声援の履歴

アカウント →「声援を送った動画」から再訪する。本人のセッションだけからユーザーを決め、最後に声援を受け付けた日時の降順・同時刻はclip IDの降順に20件返す。動画名と競技は受付時の情報を保存し、一覧表示時に動画ごとの問い合わせを行わない。公開終了した動画を開けなくても元のフィードを保つ。

receipt・集計shard・`accounts/{user}/cheered_clips/{clip}` は同じtransactionで更新する。同じrequest IDの再送は重複も並び替えも起こさない。履歴は本人専用の `no-store` 応答で、cursorも本人に結び付く。履歴に7日TTLは適用しない。旧receiptにはユーザー情報がないため遡及復元しない。receiptの `expires_at` は受付の7日後。TTL未設定の環境では自動削除されないため、保持日数そのものを保証する値ではない。

## 団体ページとフォロー

概要の投稿団体名、または `/demo/?organization=<id>` から団体プロフィールと公開動画一覧を開く。一般ユーザーはログイン後に無料でフォローし、アカウントの一覧から再訪できる。フォローは投稿権限や団体担当者への所属を付与しない。

| API | 用途 |
| --- | --- |
| `GET /api/v1/publishers/{id}` | 有効な団体の公開プロフィール（id/name/kind） |
| `GET /api/v1/clips?publisher={id}` | その団体の公開動画。検索・cursorと併用可 |
| `GET` / `PUT` / `DELETE /api/v1/publishers/{id}/subscription` | 本人のフォロー確認・設定・解除 |
| `GET /api/v1/me/subscriptions?cursor=...` | 本人のフォロー一覧を20件ずつ取得 |

担当者・資格情報・内部状態は公開しない。フォロー状態・一覧は本人のセッションで認可し、`no-store`で返す。団体管理・削除は[団体担当者の仕様](publisher-auth.md)を参照。

## 管理アカウント

`admin` はrangle運営用の予約IDで、一般登録できない。運営ログインではフィードの代わりにGS生成・LIVE・履歴の管理ページを表示する。通常ユーザーは視聴・交流を利用する。[団体担当者](publisher-auth.md)は別の資格情報・Cookieを使う。

運営用 `/api/v1/generation` はプリセット一覧も運営専用。団体担当者は `/api/v1/publishers/{account}/generation` を使い、自団体のジョブと許可済み入力だけを扱う。操作ごとに現在の権限を読み直し、所有者チェック・CSRF・全体の生成予算を維持する。運営画面は `/auth/me` とログイン応答の `user.is_admin` で切り替え、[団体担当者の画面](publisher-auth.md)は別の認証を使う。

運用者は対象DBへのADC権限で次を実行する。パスワードは標準入力から読み、個別salt付きhashで保存する。既存アカウントは上書きしない。新規管理者は15〜128文字の有効なUTF-8・最大512byteが必須。既存管理者のパスワードと有効なセッションは変更しない。

```bash
read -r -s -p 'Administrator password: ' rangle_admin_password
printf '\n'
printf '%s\n' "$rangle_admin_password" | go run ./cmd/rangle-admin -project PROJECT_ID -database DATABASE_ID
unset rangle_admin_password
```

参照：[OWASP Password Storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)、[Go CrossOriginProtection](https://pkg.go.dev/net/http#CrossOriginProtection)、[Firestore aggregation](https://firebase.google.com/docs/firestore/query-data/aggregation-queries)。
