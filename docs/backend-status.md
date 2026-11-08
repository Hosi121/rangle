# Goサーバーの現状と次の作業

2026-11-08時点。実装・ローカル検証と、公開環境での成立確認を分ける。

## 実装済み

| 領域 | できていること | 残っていること |
| --- | --- | --- |
| フィードAPI | 一覧・詳細、limit/cursor、入力検証、ETag・304・HEAD | クライアントの次ページ取得、更新時の復旧 |
| 版付き資産 | manifest検証、size/SHA照合、Range、immutable cache、公開対象の限定 | CDNを通した同じ契約の検証 |
| 配信先の分離 | 外部media originのURLを返し、Goで資産を中継しない | 実CDN・HTTPSでのUI/API/資産の接続確認 |
| サーバー運用 | PORT対応、health、ヘッダーtimeout、終了時の処理待ち、slog | 要求別の遅延・失敗の観測、過負荷時の制御 |
| catalog | 起動時に不変snapshotを検証。GCS保存・readonly mountのIaCも用意 | 公開工程の自動化、複数revision混在・復帰の確認 |
| ABR | Goの基準実装と合成データによるテスト | 実Gaussian・クライアント再生との接続 |

発表用Cloud Runの設定はmin0/max1・concurrency40。費用を抑える設定であり、10万人向けの容量根拠ではない。詳細は [API仕様](api-delivery.md)、[ログ方針](logging.md)、[stg配備](stg-deployment.md) を参照。

## 今回の拡張：API応答の事前生成

各clipのJSONとETagを起動時に生成し、詳細応答で再利用する。一覧は必要なclipのJSONだけを結合する。応答bytes・ETag・cursorの契約を保ち、ページの組合せごとに蓄積するキャッシュは持たない。保持するのはメタデータで、Gaussian本体は含まない。

Go 1.27.3、固定CPUコア・GOMAXPROCS=1、合成100clip、各200ms×5回、変更前後の順序を交替した中央値。比較元は `0df2273` に同じbenchmarkを追加したもの。

| 要求 | 処理時間 µs/op（前→後） | メモリ割当回数/要求（前→後） |
| --- | --- | --- |
| 一覧10件 | 7.340 → 3.239 | 28 → 26 |
| 一覧50件 | 28.363 → 11.240 | 29 → 27 |
| 詳細1件 | 2.191 → 1.140 | 25 → 19 |

これはソケット・TLS・CDNを含まないhandlerの計測で、配信p99や同時視聴数を示さない。起動時のJSON生成・保持と引き換えに、要求時の処理を減らす。HTTP契約のテスト、`go test -race ./...`、`go vet ./...` で検証する。

## 次に実施すること

1. **観測**：API遅延・エラー率、CDN hit/origin転送量、端末の再生開始・再バッファを確認できるようにする。[#33](https://github.com/Hosi121/rangle/issues/33)
2. **過負荷制御**：計測結果とSLOから同時処理上限・timeout・再試行条件を決める。[#24](https://github.com/Hosi121/rangle/issues/24)
3. **公開・復帰**：asset→manifest→catalogの検証と公開、旧snapshotへ戻す工程を整える。[#25](https://github.com/Hosi121/rangle/issues/25)、[#26](https://github.com/Hosi121/rangle/issues/26)
4. **実配信の検証**：CDN越しのRange/CORS/cacheを確認し、許可された予算で段階負荷試験を行う。[#32](https://github.com/Hosi121/rangle/issues/32)、[#35](https://github.com/Hosi121/rangle/issues/35)

先行の1万要求テストは同一PC・HTTP/1.1 loopbackでの有限GET。APIは9,466/10,000件成功し、534件は生成側の一時ポート不足だった。TLS/CDNや持続視聴を含まず、数千〜10万人の配信性能は未検証。
