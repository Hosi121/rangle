# catalog snapshotの選択・切替・復帰

API instanceは起動時に選択したcatalogを検証し、終了までその静的snapshotを使う。private GCSの `snapshots/<sha256>.json` を既存のread-only mountから読み、catalogの変更は新しいobjectと新しいAPI revisionで反映する。稼働中のobjectを上書きして更新する方式は採らない。

## 選択した版を照合する

公開フィード `GET /api/v1/clips` は `X-Rangle-Catalog-SHA256` を返す。HEAD、304、静的clip詳細でも同じ値を確認できる。これは**選択したbase static catalogの元JSON bytesのSHA-256**で、空白・改行を含む。object名のhashと手元のファイルの `sha256sum` を照合するための値である。

次の識別子は役割が異なる。

| 識別子 | 表すもの |
| --- | --- |
| `snapshots/<sha>.json` と上記header | 元catalog bytes。再起動・増設へ渡す同一入力 |
| catalog内の `version` | JSON schemaの版。現在は1。release番号ではない |
| clipの `id` / `revision` | 版付きmanifest・assetのidentity |
| HTTP `ETag` | 要求したページ/詳細の応答body bytes |
| `next_cursor` | 正規化catalog、asset origin、検索条件、掲載registryに結び付いた不透明な継続位置 |

同じ内容のJSONをindentして保存すると元bytesのSHAは変わるが、正規化されたcatalog、応答body・ETag・cursorは従来どおり同じになる。headerの追加で既存のJSON/ETag/cursor形式は変えていない。

生成結果の掲載registryは別の永続データである。掲載・取り下げでフィードやcursor/ETagが変わっても、このheaderはbase static catalogのSHAを示し続ける。registry取得失敗は既存どおり503/no-storeとなり、空のフィードへ置き換えない。static catalogのrollbackでregistryを巻き戻したり削除したりしない。

配備ごとに、次の組を候補・現在の公開版・復帰先について記録する。

| API revision | OCI image digest | catalog object / SHA | asset origin | 確認したclip id/revision・feed ETag |
| --- | --- | --- | --- | --- |
| 復帰先 | 実際のdigest | 旧 `snapshots/<sha>.json` | 旧origin | 旧版の確認結果 |
| 候補 | 実際のdigest | 新 `snapshots/<sha>.json` | 選択したorigin | 候補の確認結果 |

revision名だけ、image tagだけ、`healthz=200`だけではcatalog内容を特定できない。候補tagのURLと通常公開URLの両方で、headerが選択したSHAに一致することを確認する。例はローカルファイルとHTTPだけを読み、クラウド設定を変更しない。

```sh
sha256sum "$catalog_file"
curl --fail --silent --show-error -I "$candidate_origin/api/v1/clips?limit=1"
curl --fail --silent --show-error -I "$app_origin/api/v1/clips?limit=1"
```

さらにGETのclip `id/revision`、`manifest_url`、preview/scene URL、配信先のbytes/hash/headerを確認する。条件付きGET/HEADの304にもcatalog SHAが付く。古いcacheのbodyを保持しているときも、返されたheaderとETagを区別して記録する。

loaderは16MiB上限・JSON/schema・manifest等を起動時検証する。ファイル名のhashをexpected値として検証する機能は持たないため、JSONとして妥当でも選択SHAとheaderが一致しない候補は昇格しない。objectの作成前検証・不変keyの維持と、この配備時照合を組み合わせる。外部media originの存在をAPI起動時に取得検証することもないため、mediaの公開確認を先に済ませる。

## 切替と混在時の契約

1. 許諾・allowlist・bytes/hash/headerを検証したasset、manifest、catalogの順に新しい不変keyへ配置する。新旧catalogと新旧media revisionを保持する。
2. 現在の正常revision・image digest・catalog SHA・origin・traffic配分を記録し、trafficをそのrevisionへ明示固定する。既存IaCでcatalog SHA/runtime argsを変更するときも、未検証のLATESTへ自動でtrafficを流さない。
3. 新しいcatalog objectを選ぶ候補revisionを用意し、通常trafficを流す前に候補URLでSHA・API・mediaを照合する。snapshot欠損、読取失敗、不正JSON/schemaは新instanceの起動失敗となる。旧版やデモへ自動fallbackしない。
4. 確認できた候補へ切り替え、通常公開URLでも同じ選択SHAと媒体を確認する。失敗時は記録した正常revisionの明示traffic配分へ戻す。[既存CI/CD](cicd.md)と[stgのruntime管理](stg-deployment.md)に従い、provider・権限・Billingは変更しない。

snapshot更新とimage更新を毎回同時にする必要はない。同じschema/codecを扱えるimageなら、同じdigestのままcatalog SHAを変えたrevisionを作れる。imageだけ更新する既存CIはIaCのcatalog/mount/originを保つ。互換性の異なる変更では、対応するimageとcatalogを組で候補確認する。

新旧instanceとcacheが一時的に混在し得る。フィードのcache契約は従来どおり `public, max-age=15, stale-while-revalidate=30`、エラーはno-store。旧ページのcursorを新snapshot/originへ送ると400となり、旧ETagが一致しない応答は200で返る。同じsnapshot/originの別instanceなら応答bytes/ETag/cursorが一致し、条件付き取得は304になる。

clientは既存の[フィード継続取得契約](feed-pagination.md)に従う。継続ページの400で同じquery/tag/共有clipの先頭を1回だけ再取得し、旧windowを捨てる。同じ世代での再失敗は手動再試行を待つ。保持window内と受信pageのID重複、先頭に補完した共有clipの重複を除き、全履歴のID集合は保持しない。混在中も必ず途切れずページ送りできる、という保証はしない。

## 復帰とasset保持

復帰は記録した正常API revisionへtrafficを戻すか、同じimage/catalog SHA/originを選ぶrevisionを再作成して確認してから行う。以前の正確なcatalog bytesとoriginへ戻せば、静的フィードのbody・ETag・cursorも再現できる。新snapshotのcursorは旧版では400となるため、同じclientの先頭再取得契約を使う。検索条件や現在の生成掲載registryが違えば、同じETagになるとは限らない。

rollbackで新しいassetを削除せず、新旧どちらのmanifest/asset URLも不変bytesとして取得できる状態を保つ。このrelease中は新旧snapshot・image digest・manifest・media revisionを保持し、GCやbucket lifecycleの変更は行わない。15秒+30秒のAPI cache時間をmedia削除の期限には使わない。稼働中のclient、共有リンク、生成結果からの参照とrollback候補を確認した後の削除判断は別作業とする。

## 機械検証の範囲

```sh
go test -race ./internal/catalog -run 'TestSourceIdentity|TestStaticSourceIdentity|TestSnapshotServerLifecycle' -count=1 -v
```

テストは実際の `cmd/rangle-server` をrace付きで一度buildし、ローカルTCPで2processを起動する。同一版の増設、新旧版の混在、cacheから得た旧ページ相当のcursorとIf-None-Match、originだけの変更、同一imageで旧snapshotへ復帰、HEAD/304のSHA、旧・新media bytesの保持を検証する。欠損・読取エラー・破損・不正schemaの候補がlisten前に失敗しても、旧processが正常なままであることも確認する。動的registryの変更とstatic SHAの独立性は別のHTTPテストで確認する。

Cloud API・GCS/FUSE・実CDNのcache期限やtraffic切替はこのテストでは操作しない。実環境の候補/公開URL照合は上の配備手順で行う。負荷試験や10万同時視聴の性能実証は含まない。
