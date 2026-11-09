# APIと版付き資産の配信

今回の完了範囲は、GoのフィードAPIから版付きmanifestを取得し、広告された資産をHTTPで取得できることとする。生成・画質評価は別工程で行う。クラウド資源の作成や、大規模な負荷実験は含めない。

## 構成

カタログAPIは起動時に読み込んだcatalogを公開する。利用者ごとの状態を持たず、同じcatalogを複数のAPIプロセスに配布できる。初期版のcatalog更新は再起動で反映し、動的な編集や推薦は後続とする。[アカウント・いいね・コメント](accounts.md) は別APIと保存先を使う。

公開フィードの `X-Rangle-Catalog-SHA256` は選択した静的catalogの元JSON bytesを識別し、HEAD/304でも確認できる。生成結果の掲載registry、応答ETag、cursorのhashとは役割が異なる。不変snapshotの切替・新旧instance/cacheの混在・旧版への復帰とasset保持は [catalog snapshotの運用契約](catalog-snapshots.md) に従う。

各clipのJSONとETagは起動時に生成し、詳細応答で再利用する。一覧は指定ページのJSONを結合し、その応答内容からETagを計算する。ページごとに蓄積するキャッシュは持たず、保持量はcatalogのメタデータ量に比例する。[実装状況と計測](backend-status.md) を参照。

資産配信は `internal/delivery` のHTTP handlerへ分離する。ローカルではAPIと同じHTTP入口に載せ、本番では版付きmanifestと資産をCDN・object storageへ配置する。APIが返す取得先originを設定で変更できるようにし、APIプロセスがCDNの資産を中継しない。

## 配信handlerの契約

`delivery.NewFileHandler(manifest stream.Manifest, assetRoot string, logger *slog.Logger) (*delivery.FileHandler, error)` を入口とする。rootの下に、manifest内の絶対pathから先頭 `/` を除いた位置で資産を配置する。例えば `/assets/basketball/rev1/full/000.splat` は `assetRoot/assets/basketball/rev1/full/000.splat` を指す。

- 既存の `stream.Manifest` を正本とし、manifestのサイズ・SHA256と実ファイルを起動時に照合する。manifest内で広告した初期化資産・区間だけを公開し、同じディレクトリの別ファイルや一覧は公開しない。
- ファイルは `os.Root` でroot内に閉じ、ハッシュ照合もHTTP配信も全量をRAMへ読み込まず行う。公開対象の数に比例してfile descriptorを常時保持しない。
- 版付きmanifestと資産にSHA256由来の強いETag、長期不変キャッシュと `no-transform` を付ける。GET・HEAD・Range・条件付き取得を扱い、エラーは `no-store` とする。
- 公開中のファイルを変更しない。追加の内容は新しいrevisionで公開する。起動後の置換・サイズ・mtime変更を検知したら、古いETagで応答せず取得を失敗させる。読み取り中の同時上書きもサポートしないため、本番の配信ファイルは読み取り専用にする。
- `Close` はHTTP要求が終了してから呼ぶ。実行時の資産I/O失敗はこのHTTP境界で `slog` に記録する。

実験用sceneのmanifestは描画用メタデータ、`stream.Manifest` は区間・品質・資源予算の配信メタデータであり、別の役割を持つ。両方が存在するだけでFlutterのABR再生まで接続されたとは扱わない。

## フィードAPI

| 要求 | 応答 |
| --- | --- |
| `GET /api/v1/clips?limit=10` | `clips` 配列と、次ページがあれば `next_cursor` |
| `GET /api/v1/clips?limit=10&cursor=...` | 同じcatalog snapshotの続き |
| `GET /api/v1/clips?q=slam&tag=basketball` | カタログ全体を検索し、結果をページ送り |
| `GET /api/v1/clips/{id}` | 1件のメタデータ。存在しなければ404 |
| `HEAD` | GETと同じヘッダー、bodyなし |

`limit` は既定10、上限50。cursorは不透明な値として扱い、加工しない。catalogや取得先originの更新でcursorが無効になったら400を返すため、先頭から取り直す。無効・重複・未定義のクエリも400にする。初期版は全利用者に同じ公開フィードを返し、`public, max-age=15, stale-while-revalidate=30` と応答内容由来のETagを付ける。エラーは `no-store` とする。

`q` はtitle・sport・description・tagsの大文字小文字を区別しない部分一致。空白区切りの全語を含む結果を返す（最大256bytes・8語）。`tag` は `#` を含めない完全一致。検索条件もcursorに結び付ける。catalogの `tags` は省略可・最大10個で、各tagは文字・数字・結合文字・`_`の32文字以内。未指定の場合、sportがtagとして使える形なら応答に補う。

クリップには `id`、`revision`、`title`、`sport`、`description`、`codec`、`manifest_url` を返す。通常動画の `preview_url` と描画用の `scene_url` は任意。`manifest_url` は配信契約のmanifestを指し、動画プレイヤーへ直接渡すURLではない。Flutterは `RANGLE_API_URL=/api/v1/clips` で先頭ページとmedia URLを読み込み、動画・自由視点・時刻シークを行う。[Webの起動手順](../apps/web/README.md#apiから動画自由視点を開く)を参照。ABR制御への接続は次の段階とする。

## 公開するcatalog

catalogはAPIからそのまま公開しない設定用JSONで、最大16MiB。version 1と `clips` 配列を必須とし、未知フィールド、重複clip ID、不正なmanifestを起動時に拒否する。配列順がフィード順となる。空配列も受け付ける。

フィードの応答サイズを抑えるため、1件のtitleは256bytes、sportとcodecは各128bytes、descriptionと各media URLは4096bytesまでとする。

以下はHTTP契約を確認する3byteの合成資産であり、描画可能なGaussianではない。実際の公開では、生成済み資産の実サイズ・SHA256と、測定した資源予算を設定する。

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

このJSONを `.local/catalog.json` に保存し、次のように起動する。`-ui-root` を追加すると `/` は製品入口、`/demo/` は既存Webビルドになる。Flutter自身の静的ファイルは `/demo/assets/` 等へ移り、フィードAPI・manifest・公開clipの版付き資産URLは変わらない。

カタログの `preview_url` が `/quality-previews/clip.webm` 等のローカル動画を参照し、そのファイルがUI root内の同じ相対pathにある場合は、宣言された動画だけを従来のroot側URLでも配信する。絶対HTTP(S) URLをローカルaliasへ変換せず、JavaScript・フォント等のUI資産は `/demo/` 配下に限定する。公開対象は起動時に固定され、未宣言動画のroot側aliasは作られない。

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

外部取得先を使うときは `-asset-origin https://media.example.com` を指定し、ローカル配信を不要とするAPIプロセスでは `-asset-root` を省く。この場合、APIは外部manifest/資産へのURLを返すだけで、取得や中継を行わない。originはHTTP(S)のschemeとhostに限定する。preview/sceneの相対pathもこのoriginへ解決し、既に絶対HTTP(S) URLなら保持する。外部URLの存在や権利はcatalogの投入前に公開工程で確認する。

Cloud CDN等へ移す際は、同じ版付きpathと公開bytesを配置し、Content-Type・Cache-Control・CORSを配信先に設定する。Rangeと強いETagを使う資産では動的圧縮による変換を避ける。`no-transform` はこの条件を守るために付ける。[Cloud CDNのキャッシュ](https://docs.cloud.google.com/cdn/docs/caching)、[動的圧縮の制約](https://docs.cloud.google.com/cdn/docs/dynamic-compression)

サーバーの既定はループバック待受けで、PORT環境変数がある場合は `0.0.0.0:PORT` で待ち受ける。明示した `-listen` 引数があればそちらを優先する。サーバー自体はCDNやGoogle Cloud資源を作成しない。Dockerでのビルドと起動は [Dockerの手順](docker.md) を参照。数千〜数万人の同時視聴への耐性は、CDNのwarm/cold状態、originへの集中、APIの水平分散を含めた別の容量検証で判断する。
