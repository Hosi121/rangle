# rangle 配信部品の初期契約

Goのmanifest・HTTP・ABR基準実装の契約。[実再生](temporal-playback.md)・[packing](gaussian-compression.md)・[言語間fixture](../testdata/contracts/v1/README.md)とは分ける。合成入力は4DGS映像ではなく、[実client接続](transport-playback.md)は合成HTTP/browserまで検証し、実品質ラダーのABR/QoE・HLS/DASH準拠・スマートフォン性能は未検証。

## 共通の型

正本は`internal/stream/manifest.go`。moduleは`github.com/Hosi121/rangle`、初期実装はGo1.27.3以降の標準ライブラリを使用する。

- 全品質で時刻・長さ・区間数を揃え、各区間を独立復号可能にする。
- 資産URLは`/assets/{clip_id}/{revision}/{representation_id}/{filename}`。同一URLの公開bytesを変更しない。
- bitrateはbits/s、sizeはbytes、時刻はseconds、復号時間はmilliseconds。
- `peak_decoded_bytes`は復号/描画の作業メモリ推定、`decode_time_ms`は復号時間推定。file sizeやGPU描画実測とは別で、合成入力では仮値。
- 初期化資産のURL/sizeを広告し、切替時の取得予算へ含める。

## HTTP配信

入口：`internal/delivery.NewHandler(manifest stream.Manifest, assets map[string][]byte) (http.Handler, error)`。

- manifestは`/manifests/{clip_id}/{revision}.json`。広告URLの資産だけを提供する。
- 起動時にmanifest・実bytes・size/SHA256を照合する。
- 不変資産/manifestにcache指示・ETagを付け、GET/HEAD/Range/条件付き要求を扱う。errorを長期cacheしない。

大きいfileは`delivery.NewFileHandler`で起動時stream照合し、要求ごとに読む。memory handlerとloopback既定の`cmd/rangle-dev`は小さな合成デモとして残す。[feed/外部origin](api-delivery.md)

## ABR制御

`internal/abr`が次の品質を選び、`cmd/abr-sim`が有限の合成系列で示す。

- 異なる時定数のEWMAで帯域を保守的に推定し、cache/失敗/中断sampleを無条件に混ぜない。
- 帯域・再生buffer・次区間の実bytes・未取得初期化・復号時間・作業メモリを考慮する。
- 起動時と上昇時は慎重に、悪化時は下げる。hard制約を満たす品質がなければ選択不能を返す。
- 初期化cacheはclip/revision内だけで有効。同じ品質IDでも別版へ持ち越さない。
- metadata判定とは別に、実clientは旧/新品質と先読みを含む総メモリを管理する。

BOLAを実装していない規則をBOLAと呼ばず、合成simulatorを実回線の改善実績とは扱わない。

## 初期実装時の分担と完了条件

`internal/stream`が共通型、`internal/abr`が推定/選択、`internal/delivery`がHTTPを担当。型・制約・Range/ETag・不正要求と部品間接続を検証する。現在の統合・race/vet・PR手順は[開発ルール](../CONTRIBUTING.md)に従う。
