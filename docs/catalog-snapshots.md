# catalog snapshotの選択・切替・復帰

APIはprivate GCSの `snapshots/<sha256>.json` を既存read-only mountから起動時に読み、終了まで使う。更新は新objectと新API revisionで反映し、稼働中objectを上書きしない。

## 選択した版を照合する

公開feed・静的detailのGET/HEAD/304は `X-Rangle-Catalog-SHA256` を返す。値は空白・改行を含む**base static catalogの元JSON bytesのSHA-256**で、object名と手元の `sha256sum` を照合する。

| 識別子 | 意味 |
| --- | --- |
| `snapshots/<sha>.json` / header | 再起動・増設へ渡す元bytes |
| catalog `version` | schema版（現在1）、release番号ではない |
| clip `id/revision` | manifest/assetのidentity |
| HTTP `ETag` | 要求ページ/詳細のbody bytes |
| `next_cursor` | 正規化catalog・origin・検索・掲載registryに結び付く不透明な位置 |

indentだけの変更は元SHAを変えるが、正規化catalog・body/ETag/cursorを変えない。掲載registryは別データで、掲載変更後もheaderはbase static SHAを示す。registry障害は503/no-storeとし、空feedにしない。static rollbackでregistryを戻したり削除したりしない。

候補・公開版・復帰先それぞれに、API revision、OCI digest、catalog object/SHA、asset origin、確認したclip id/revision・feed ETag、traffic配分を記録する。revision名・image tag・health200だけでは不十分。候補URLと公開URLで選択SHAを照合する（以下は読取のみ）。

```sh
sha256sum "$catalog_file"
curl --fail --silent --show-error -I "$candidate_origin/api/v1/clips?limit=1"
curl --fail --silent --show-error -I "$app_origin/api/v1/clips?limit=1"
```

GETのid/revision・manifest/preview/scene URLと配信bytes/hash/headerも確認する。304でもcatalog SHAとcache済みbodyのETagを区別する。loaderは16MiB・schema/manifestを検証するが、object名のSHAとの照合も外部media取得も行わない。妥当なJSONでも選択SHAとheaderが異なる候補は昇格せず、mediaの公開確認を先に済ませる。

## 切替と混在時の契約

1. 許諾・allowlist・bytes/hash/headerを確認し、asset → manifest → catalogを新しい不変keyへ配置する。新旧media/snapshotを保持する。
2. 正常revision/digest/SHA/origin/trafficを記録し、正常revisionへtrafficを明示固定する。IaC変更時も未検証LATESTへ自動送出しない。
3. 候補URLでSHA/API/mediaを確認する。snapshot欠損・読取失敗・不正JSON/schemaは起動失敗とし、旧版・デモへfallbackしない。
4. 候補へ切り替え、公開URLも照合する。失敗時は記録済み正常revisionのtrafficへ戻す。[CI/CD](cicd.md)・[stg runtime管理](stg-deployment.md)に従い、provider/権限/Billingは変更しない。

互換imageなら同じdigestでcatalog SHAだけを変えられる。image更新CIはIaCのcatalog/mount/originを保つ。非互換変更はimageとcatalogを組で確認する。

混在中もcacheは `public, max-age=15, stale-while-revalidate=30`、エラーはno-store。旧cursorを新snapshot/originへ送ると400、旧ETag不一致は200。同じsnapshot/originのinstance間ではbytes/ETag/cursorが一致し、条件付き取得は304。

clientは[継続取得契約](feed-pagination.md)に従い、継続400でquery/tag/共有clipを保って先頭を1回だけ取り直し、旧windowを捨てる。同世代の再失敗は手動再試行を待つ。重複排除はwindow/受信page/共有clipを対象とし、全履歴集合を持たない。混在中の途切れないpagingは保証しない。

## 復帰とasset保持

正常revisionへtrafficを戻すか、同じdigest/SHA/originのrevisionを再作成・確認する。正確な元bytes/originで静的body/ETag/cursorを再現できるが、検索・現在のregistryが違えばETagも異なる。新cursorは旧版で400になり、同じ先頭再取得契約を使う。

rollbackで新assetを削除しない。このrelease中は新旧snapshot・image digest・manifest/mediaを不変bytesで保持し、GC/bucket lifecycleを変更しない。API cacheの15+30秒をmedia削除期限にしない。稼働client・共有リンク・生成結果・復帰候補を確認した後の削除は別作業。

## 機械検証の範囲

```sh
go test -race ./internal/catalog -run 'TestSourceIdentity|TestStaticSourceIdentity|TestSnapshotServerLifecycle' -count=1 -v
```

race付きserverをbuildしローカルTCPの2processで、増設・混在・旧cursor/validator・origin変更・旧版復帰・HEAD/304のSHA・新旧media保持を検証する。不正候補がlisten前に失敗しても旧processが正常なこと、動的registryとstatic SHAの独立性も確認する。

Cloud API・GCS/FUSE・実CDN期限/trafficは操作せず、実環境は上の候補/公開URL照合で確認する。負荷・10万同時視聴の実証は含まない。
