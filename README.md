# rangle

**replay + angle**。スポーツの決定的瞬間を、自分の視点で見返す。

rangleは、実写から再構成したスポーツのハイライトを縦型フィードで楽しみ、気になった瞬間には時間と視点を動かして見返せるモバイルプラットフォームです。

既存の多視点データセットを使い、クラウドでの動的Gaussian Splattingの再構成、モバイル向けの圧縮・分割配信、端末での復号・GPU描画を一つの流れにつなぐ構成を検討しています。Go、ML処理、モバイルアプリをモノレポで管理します。

GoによるABR制御・HTTP区間配信と、Flutter Webの縦型フィードを実装しています。Webでは再構成サンプルの動画再生、Gaussian資産の視点操作・時刻シーク・連続再生に対応します。実Gaussianの分割配信とABR再生の接続は今後の実装です。

ハッシュタグとキーワードでハイライトを探し、選んだclipのリンクを共有できます。ユーザーID・パスワードでログインすると、いいね・コメントの投稿と自分のコメントの削除ができます。交流データは専用Firestoreに保存します。[アカウントAPIと設定](docs/accounts.md)・[保存先の構成](docs/community-storage.md)。

確認済みデータセットから静的3DGSと動く4DGSを生成する画面・APIも実装しています。ログインしてデータを選び、生成状態の確認・中止・自由視点での結果表示ができます。学習は配信サーバーから分離したVertex AIの東京Spot T4で行います。[生成の仕様と制約](docs/generation.md)を参照してください。

## 資料

- [Webアプリの実行方法](apps/web/README.md)
- [Dockerでのビルドと起動](docs/docker.md)
- [企画書 草案](docs/proposal.md)
- [初期インフラ構成案](docs/infrastructure-design.md)
- [現在のGCPクラウド構成図](docs/cloud-architecture.md)
- [技術選定案](docs/technology-selection.md)
- [ABRと配信最適化の調査](docs/streaming-research.md)
- [配信部品の共通契約](docs/streaming-contract.md)
- [Goサーバーの現状と次の作業](docs/backend-status.md)
- [Gaussian再構成の画質と描画条件](docs/gaussian-quality.md)
- [ログ方針](docs/logging.md)
- [DevFest提出用リポジトリの案内](docs/submission.md)

## 配信部品の動作確認

Go 1.27.3以降を利用します。開発・検証に使うGoの下限を `go.mod` に揃えています。配信root外へのファイル参照は標準 [`os.Root`](https://go.dev/blog/osroot) で防ぎ、交流データの保存と認証にはFirestore SDK・Argon2idを使います。

```bash
go test -race ./...
go vet ./...
go run ./cmd/abr-sim
```

ローカルの区間配信デモを起動します。終了はCtrl+Cです。

```bash
go run ./cmd/rangle-dev
```

別のターミナルから、マニフェストと資産を取得できます。

```bash
curl http://127.0.0.1:8080/manifests/demo/synthetic-v1.json
curl -i -H 'Range: bytes=0-15' http://127.0.0.1:8080/assets/demo/synthetic-v1/low/segment-0.bin
```

デモの資産と帯域系列は合成データです。実際の4DGSの復号やHLS/DASH再生、実回線での高速化を実証するものではありません。GoのABR制御は、モバイル実装へ判断規則とテストケースを引き継ぐための基準です。

## WebビルドとローカルsceneのGo配信

既存の [Flutter Webビルド](apps/web/README.md) と、利用が許可されたローカルsceneを指定して配信できます。パスは手元のディレクトリへ置き換えてください。実データや学習重みはGitへ追加しません。

```bash
go run ./cmd/rangle-server \
  -ui-root /path/to/build/web \
  -scene-dir /path/to/local/scene \
  -scene-id basketball
```

`http://127.0.0.1:4174/` が製品入口、`http://127.0.0.1:4174/demo/` がデモUI、`/samples/basketball/scene.json` がsceneです。`/demo` はクエリを保持して `/demo/` へリダイレクトします。内蔵デモを使う場合は `RANGLE_DEMO=true` を明示し、sceneのURLを `RANGLE_SCENE_URL` に指定してビルドします。既定は同一originの配信APIを使います。sceneを使わない場合は `-scene-dir` と `-scene-id` の両方を省きます。`/health` は起動確認用です。終了はCtrl+Cで、処理中のHTTP要求を最大5秒待って停止します。

UI rootは `index.html` と製品入口の `landing.html` を必須とします。通常のFlutterビルドが両方を含むため、追加のpost-build処理は不要です。Flutterの静的ファイルは `/demo/` 配下に限定し、旧 `/main.dart.js`・`/viewer/*`・UI用 `/assets/*` 等のroot側aliasは公開しません。カタログで宣言されたローカル動画（webm/mp4/m4v/ogv）がUI root内にある場合だけ、既存のroot側URLを維持します。`/api/v1/*`・`/manifests/*`・版付き `/assets/{clip}/{revision}/*` は従来どおりです。

隠しファイル、`*.local.md`、`*.private.md`、ディレクトリ一覧を公開しません。`/samples/` はUI内の同名ディレクトリから独立し、指定sceneのmanifestとそこに列挙された相対 `.splat` だけを配信します。root外へ参照を解決させず、大きなframeは全量をRAMへコピーせずストリーミングします。

公開対象は起動時に固定します。UIの再ビルド、ファイルの置換、manifestの変更はサーバーを再起動して反映します。ローカルの可変ファイルなので `immutable` は付けず、`no-cache` と `Last-Modified` で再検証します。HEAD・Range・条件付きGETに対応し、エラーは `no-store` です。例のframe名は自身のmanifestに合わせてください。

```bash
curl -I http://127.0.0.1:4174/samples/basketball/scene.json
curl -i -H 'Range: bytes=0-15' http://127.0.0.1:4174/samples/basketball/frames/000.splat
```

`rangle-server` は、`PORT` 環境変数がなければ従来どおり `127.0.0.1:4174` で待ち受けます。`PORT` がある場合は `0.0.0.0:PORT` で待ち受け、明示した `-listen` 引数があればそちらを優先します。外部から接続を受け付ける場合は、配信対象と実行環境側の認証・アクセス範囲を確認してください。実回線の性能や大規模配信を検証するものではなく、本番の大容量資産はCDN・object storageから直接配信する方針を維持します。Dockerでの起動方法は [Dockerでのビルドと起動](docs/docker.md) を参照してください。

## フィードAPIと版付き資産

`rangle-server` に `-catalog` と `-asset-root` を指定すると、フィードAPIからmanifest・資産の取得まで確認できます。大容量ファイルは全量をRAMへ載せず配信し、版付き資産にETag・immutableキャッシュ・Rangeを適用します。`-asset-origin` を使う構成では、APIと大容量配信先を分離できます。

catalogの形式・起動例・キャッシュ方針は [APIと版付き資産の配信](docs/api-delivery.md) を参照してください。

## ログ

Goコマンドのログは標準 `log/slog` を使い、INFO以上のJSONをstderrへ出力します。`service` でコマンドを識別し、コマンドの失敗には `error` を付けます。`rangle-dev` と `rangle-server` は起動・終了とHTTPサーバー内部エラーを記録します。起動時の `listen_addr` に加え、`rangle-dev` は `manifest_url` と `fixture="synthetic"`、`rangle-server` は `scene_id` を示します。`abr-sim` の計算結果はstdout、ログはエラー時だけです。

## リポジトリ

| 用途 | リポジトリ | リモート名 |
| --- | --- | --- |
| 開発の正本 | [Hosi121/rangle](https://github.com/Hosi121/rangle) | `origin` |
| DevFest Hackathon 2026の最終提出 | [Devfest-Hackathon-2026/df-2026-t002](https://github.com/Devfest-Hackathon-2026/df-2026-t002) | `submission` |

通常の開発とpushは `origin` に対して行います。最終提出時に、必要なコードと資料を提出用リポジトリへ反映します。提出用の運用制約と期限は [提出案内](docs/submission.md) を参照してください。

## 開発フロー

常設ブランチは `stg` と `main` です。作業ブランチから `stg` へPRを出し、安定版の反映は `stg` から `main` へのPRで行います。[開発ルール](CONTRIBUTING.md) を参照してください。

## 開発用リポジトリの取得

```bash
git clone https://github.com/Hosi121/rangle.git
cd rangle
```

提出用リモートが必要な場合は追加します。

```bash
git remote add submission https://github.com/Devfest-Hackathon-2026/df-2026-t002.git
```

## ライセンス

自作コードは [MIT License](LICENSE) です。利用するデータセット、外部コード、モデルには、それぞれの利用条件が適用されます。
