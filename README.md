# rangle

**replay + angle**。スポーツの決定的瞬間を、自分の視点で見返す。

実写のハイライトを縦型フィードで見つけ、時間と視点を動かすFlutter Webアプリ。校正済み多視点データをクラウドでGaussian Splattingへ再構成し、端末で復号・描画します。

[公開アプリ](https://app.rangleplay.stream/) · [Web UI](https://app.rangleplay.stream/demo/) · [ドキュメント](docs/README.md)

## 現在できること

| 機能 | 内容 |
| --- | --- |
| [見る](apps/web/README.md) | 縦型フィード、サムネイル付き検索結果・タグ・共有、自由視点・時間モデルの再生 |
| [生成](docs/generation.md) | 運営・団体担当者向け3DGS/4DGS生成、進捗・ETA、入力割当・公開 |
| [交流](docs/accounts.md) | いいね・声援・コメント、声援履歴、団体ページ・公開動画一覧・フォロー |
| [LIVE](docs/cloud-live.md) | 多視点録画を到着順に学習し、Tokyo T4×1から8モデルを実CDN公開 |

LIVEの継続30秒遅延は未達。実カメラ入力、実スマートフォン性能、大規模視聴、実品質ラダーを使うABRの効果は未検証です。[実装と残る課題](docs/backend-status.md)

## はじめる

Go 1.27.3で合成資産のHTTP配信・ABRを試せます。

```bash
git clone https://github.com/Hosi121/rangle.git
cd rangle
```

```bash
go run ./cmd/abr-sim
go run ./cmd/rangle-dev
```

サーバー起動中、別ターミナルで取得します。終了はCtrl+C。

```bash
curl http://127.0.0.1:8080/manifests/demo/synthetic-v1.json
curl -i -H 'Range: bytes=0-15' http://127.0.0.1:8080/assets/demo/synthetic-v1/low/segment-0.bin
```

実映像の再生・性能検証とは別のデモです。画面は[Flutter Web](apps/web/README.md#開発とビルド)、コンテナは[Docker](docs/docker.md)、手元のsceneは[ローカル配信](docs/local-server.md)を参照。

## 資料と開発

| 場所 | 内容 |
| --- | --- |
| `cmd/`・`internal/` | GoのAPI・配信・生成制御・ABR |
| [apps/web/](apps/web/README.md) | Flutter UI・JS viewer |
| `ml/` | 前処理・学習・packing |
| [infra/gcp/](infra/gcp/README.md) | Terraform |
| [共有fixture](testdata/contracts/v1/README.md) | Go/Python/JS/Dartのmedia契約、74件の合成入力 |
| [docs/](docs/README.md) | 現行の構成・仕様・運用・実測。初期案はarchive |

開発正本は[Hosi121/rangle](https://github.com/Hosi121/rangle)（origin）。作業PR→stg→mainの手順は[開発ルール](CONTRIBUTING.md)、最終提出先と制約は[提出案内](docs/submission.md)を参照。実データ・重み・認証情報はGitへ追加しません。

## ライセンス

自作コードは[MIT](LICENSE)。外部データ・コード・モデルには各利用条件が適用されます。[内蔵サンプルの出典](apps/web/assets/samples/ATTRIBUTION.md)と各実験資料を参照。
