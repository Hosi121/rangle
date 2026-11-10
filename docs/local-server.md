# ローカルsceneのGo配信

[資料一覧](README.md) · [Web build](../apps/web/README.md) · [APIと版付き資産](api-delivery.md) · [Docker](docker.md)

Go検証版は `go.mod` と同じ1.27.3。`-scene-dir` は `frames` に相対 `.splat` を列挙するscene用で、RGS1 (`temporal/model.rgs`) は[catalogから配信](api-delivery.md)する。利用許可済みsceneとFlutter buildを指定し、実データ/重みはGitへ加えない。

```bash
go run ./cmd/rangle-server \
  -ui-root /path/to/build/web \
  -scene-dir /path/to/local/scene \
  -scene-id basketball
```

| path/操作 | 内容 |
| --- | --- |
| `http://127.0.0.1:4174/` | 製品入口 |
| `/demo/` | Web UI。`/demo` はqueryを保ちredirect |
| `/samples/basketball/scene.json` | 指定scene |
| `/health` | 起動確認 |
| Ctrl+C | 処理中要求を最大5秒待ち停止 |

内蔵デモは `RANGLE_DEMO=true` と `RANGLE_SCENE_URL` を指定してbuildする。既定はsame-origin API。scene不要なら `-scene-dir/-scene-id` を両方省く。

UI rootは `index.html/landing.html` 必須で、通常Flutter buildに含まれる。静的UIは `/demo/` のみ。旧rootの `/main.dart.js`・`/viewer/*`・UI `/assets/*` は公開しない。catalog宣言のlocal動画（webm/mp4/m4v/ogv）がUI rootにある場合だけroot URLを維持する。API/manifest/版付きasset URLは維持する。

隠しfile・`*.local.md`・`*.private.md`・一覧は非公開。`/samples/` はUI directoryから独立し、scene manifestと列挙された相対splatだけをroot内から配信する。大きいframeは全量RAMコピーせずstreamする。

公開対象は起動時固定。再build・置換・manifest変更は再起動で反映する。可変local fileのためimmutableを付けず `no-cache/Last-Modified` で再検証する。HEAD/Range/条件付きGET、エラーno-store。frame名は自身のmanifestに合わせる。

```bash
curl -I http://127.0.0.1:4174/samples/basketball/scene.json
curl -i -H 'Range: bytes=0-15' http://127.0.0.1:4174/samples/basketball/frames/000.splat
```

待受は既定 `127.0.0.1:4174`、`PORT` があれば `0.0.0.0:PORT`、明示 `-listen` 優先。外部公開時は対象と環境側認証/アクセス範囲を確認する。これは実回線/大規模性能検証ではなく、本番の大容量資産はCDN/object storageから直接配信する。
