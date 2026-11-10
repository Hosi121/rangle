# APIと版付き資産の配信

公開フィードから版付きmanifestとHTTP資産を取得する契約。認証は[アカウント](accounts.md)、snapshot切替は[catalog運用](catalog-snapshots.md)、容量・計測は[バックエンドの現状](backend-status.md)を参照。

## 配信方式

版付きの品質別資産をHTTPで取得する。既存のCDN・キャッシュ・Range要求を利用でき、APIと大容量モデルの配信を分離できるため。資産の分割・取得計画を改善しても同じ品質・端末条件で要件を満たせない場合に、通信方式を再検討する。

## 構成

静的base catalogは起動時から終了まで固定し、新snapshotへの更新は再起動で反映する。生成結果の掲載registryは別の永続データで、掲載変更をfeedへ動的に統合する。同じcatalogとregistryを複数processで使え、掲載変更はETag/cursorも変える。一般的なcatalog編集・推薦は未実装。交流は別API・保存先を使う。

base catalogのclipのJSON/ETagは起動時に生成する。一覧は対象ページのJSONを結合してETagを計算し、ページcacheを蓄積しない。保持量はcatalog metadataに比例する。`X-Rangle-Catalog-SHA256` は選択した静的catalogの元JSON bytesを識別し、HEAD/304でも返す。registry・ETag・cursorとの違いは[snapshot契約](catalog-snapshots.md#選択した版を照合する)を参照。

資産handlerは `internal/delivery` に分離する。ローカルでは同じHTTP入口、本番ではCDN/object storageから直接配信し、APIは設定したoriginへのURLを返す。

## 配信handlerの契約

入口は `delivery.NewFileHandler(manifest stream.Manifest, assetRoot string, logger *slog.Logger) (*delivery.FileHandler, error)`。manifestの絶対pathから先頭 `/` を除きrootへ配置する（`/assets/basketball/rev1/full/000.splat` → `assetRoot/assets/basketball/rev1/full/000.splat`）。

- `stream.Manifest` を正本にサイズ・SHA256を起動時照合し、広告した初期化資産・区間だけを公開する。ディレクトリ一覧・未宣言ファイルは公開しない。
- `os.Root` 内に閉じ、hash照合・配信で全量をRAMへ読まない。公開数に比例する常時file descriptorも持たない。
- manifest/資産はSHA256由来の強いETag、長期不変cache、`no-transform`。GET・HEAD・Range・条件付き取得に対応し、エラーは `no-store`。
- 公開bytesを変更せず、追加内容は新revisionへ置く。起動後の置換・サイズ・mtime変更では古いETagを返さず失敗する。同時上書きは非対応のため本番は読取専用にする。
- `Close` はHTTP要求終了後に呼ぶ。実行時I/O失敗はHTTP境界で `slog` に記録する。

描画用sceneと、区間・品質・資源予算を持つ `stream.Manifest` は別物。両者の存在だけでFlutterのABR接続済みとは扱わない。

## フィードAPI

| 要求 | 応答 |
| --- | --- |
| `GET /api/v1/clips?limit=10` | `clips` と、続きがあれば `next_cursor` |
| `GET /api/v1/clips?limit=10&cursor=...` | 同じsnapshotの続き |
| `GET /api/v1/clips?q=slam&tag=basketball` | catalog全体を検索・ページ送り |
| `GET /api/v1/clips/{id}` | metadata。存在しなければ404 |
| `HEAD` | GETと同じheader、bodyなし |

| 項目 | 契約 |
| --- | --- |
| `limit` | 既定10、上限50 |
| cursor | 不透明。catalog/origin更新で失効すると400。先頭から再取得 |
| query | 無効・重複・未定義は400 |
| cache | 全利用者共通、`public, max-age=15, stale-while-revalidate=30`、body由来ETag。エラーは `no-store` |
| `q` | title/sport/description/tagsと公開context（団体・試合・選手・チーム・会場）の大小文字を区別しない部分一致。空白区切り全語、最大256bytes・8語 |
| `tag` | `#`なしの完全一致。検索条件もcursorへ結び付ける |
| `publisher` | 投稿団体IDの完全一致。検索・tagと併用でき、cursorにも結び付ける |
| catalog `tags` | 任意、最大10個。各32文字以内、文字・数字・結合文字・`_`。未指定なら適合するsportを補う |

共通metadataは `id/revision/title/sport/description`。transportを持つbase catalogのclipは `codec/manifest_url` と任意の `preview_url/scene_url` を返す。掲載済みの生成結果は `preview_url/scene_url` を返し、`codec/manifest_url` は持たない（[掲載契約](generated-publication.md#永続化とフィード)）。manifestは動画へ直接渡すURLではない。Flutterは `RANGLE_API_URL=/api/v1/clips` で動画・自由視点・時刻シークに使う（[起動手順](../apps/web/README.md#apiから動画自由視点を開く)）。対応RGS1 clipはmanifestのidentity・size/SHAを検証して[ABR/区間再生](transport-playback.md)へ接続する。実品質ラダー/実機性能は別途検証する。

## 公開するcatalog

catalogは設定用JSONで、APIへそのまま公開しない。上限16MiB、version 1と `clips` 必須。未知field・重複ID・不正manifestは起動拒否。空配列を許可し、配列順をfeed順にする。1件の上限はtitle256bytes、sport/codec各128bytes、description/media URL各4096bytes。

以下はHTTP検証用の3byte合成資産で、描画可能なGaussianではない。実公開では実サイズ・SHA256・測定した資源予算を使う。

```json
{
  "version": 1,
  "clips": [{
    "title": "Transport example",
    "sport": "demo",
    "description": "Synthetic transport bytes, not Gaussian data",
    "manifest": {
      "version": 1,
      "clip_id": "example",
      "revision": "v1",
      "codec": "synthetic-transport-v1",
      "representations": [{
        "id": "low",
        "bitrate_bps": 24,
        "peak_decoded_bytes": 3,
        "decode_time_ms": 0,
        "segments": [{
          "index": 0,
          "start_seconds": 0,
          "duration_seconds": 1,
          "url": "/assets/example/v1/low/000.bin",
          "size_bytes": 3,
          "independent": true,
          "sha256": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
        }]
      }]
    }
  }]
}
```

`.local/catalog.json` として保存し、起動する。

```bash
mkdir -p .local/published/assets/example/v1/low
printf abc > .local/published/assets/example/v1/low/000.bin
go run ./cmd/rangle-server -catalog .local/catalog.json -asset-root .local/published
```

```bash
curl -i http://127.0.0.1:4174/api/v1/clips
curl -i http://127.0.0.1:4174/manifests/example/v1.json
curl -i -H 'Range: bytes=1-2' http://127.0.0.1:4174/assets/example/v1/low/000.bin
```

`-ui-root` を追加すると `/` は製品入口、`/demo/` はWeb UIとなる。Flutter資産は `/demo/` 配下、API・manifest・版付きasset URLは維持する。catalogが宣言するローカルpreview動画だけ、UI root内の同じ相対pathからroot側aliasで配信する。絶対HTTP(S) URLはalias化せず、JS・font・未宣言動画のroot aliasは作らない。公開対象は起動時固定。

外部配信は `-asset-origin https://media.example.com` を指定する。scheme/hostだけのHTTP(S) originとし、preview/scene相対pathも解決する。既存の絶対HTTP(S) URLは保持する。`-asset-root` を省けばローカル配信をせず、APIは外部取得・中継もしない。存在と権利は公開工程で確認する。

CDNへ同じ版付きpath/bytesを置き、Content-Type・Cache-Control・CORSを設定する。Rangeと強いETagを保つため動的圧縮を避ける（`no-transform`）。[Cloud CDN cache](https://docs.cloud.google.com/cdn/docs/caching)、[圧縮の制約](https://docs.cloud.google.com/cdn/docs/dynamic-compression)。

既定待受はloopback、`PORT` 設定時は `0.0.0.0:PORT`、明示 `-listen` が優先。serverはcloud資源を作らない。[Docker](docker.md)での起動、[容量検証の残件](backend-status.md)を参照。
