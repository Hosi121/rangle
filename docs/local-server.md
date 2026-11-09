# ローカルsceneのGo配信

[資料一覧](README.md) · [Webの開発とビルド](../apps/web/README.md) · [APIと版付き資産](api-delivery.md)

Goの検証版は `go.mod` と同じ1.27.3です。`-scene-dir` は `frames` に相対 `.splat` を列挙する従来型scene用です。時間モデルのRGS1（`temporal` / `model.rgs`）は[APIと版付き資産の手順](api-delivery.md)でcatalogから配信します。コンテナでの起動は[Dockerの手順](docker.md)を参照してください。

既存の [Flutter Webビルド](../apps/web/README.md) と、利用が許可されたローカルsceneを指定して配信できます。パスは手元のディレクトリへ置き換えてください。実データや学習重みはGitへ追加しません。

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

`rangle-server` は、`PORT` 環境変数がなければ従来どおり `127.0.0.1:4174` で待ち受けます。`PORT` がある場合は `0.0.0.0:PORT` で待ち受け、明示した `-listen` 引数があればそちらを優先します。外部から接続を受け付ける場合は、配信対象と実行環境側の認証・アクセス範囲を確認してください。実回線の性能や大規模配信を検証するものではなく、本番の大容量資産はCDN・object storageから直接配信する方針を維持します。Dockerでの起動方法は [Dockerでのビルドと起動](docker.md) を参照してください。
