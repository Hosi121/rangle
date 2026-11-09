# rangle

**replay + angle**。スポーツの決定的瞬間を、自分の視点で見返す。

rangle は、実写から再構成したスポーツのハイライトを縦型フィードで見つけ、気になった瞬間には時間と視点を動かして見返せる Flutter Web アプリです。校正済みの多視点データをクラウドで Gaussian Splatting に再構成し、端末で復号・GPU描画します。Go・Flutter/Dart・Python の実装をモノレポで管理しています。

[公開アプリ](https://app.rangleplay.stream/) · [Web UI](https://app.rangleplay.stream/demo/) · **[ドキュメント一覧](docs/README.md)**

## 現在できること

| 機能 | 確認済みの範囲 | 詳細 |
| --- | --- | --- |
| ハイライトを見る | 動画フィード、検索・タグ、共有リンク。自由視点での視点操作・シーク・時間モデルの連続再生。対応情報がある素材は動画と3Dの時刻・再生意図を引き継ぐ | [Webアプリ](apps/web/README.md) |
| 生成する | ログイン後に確認済みデータを選び、静的3DGS・動的4DGSを生成。状態確認・中止・結果表示・所有者によるフィード掲載 | [生成の仕様](docs/generation.md) |
| 交流する | ログインした利用者のいいね・コメント投稿・自分のコメント削除。交流データは専用Firestoreへ保存 | [アカウントAPI](docs/accounts.md) |
| LIVEを見る | 校正済み録画を到着時刻に合わせて入力し、Tokyo Spot T4×1で逐次更新した8モデルを実CDNへ公開する経路を確認 | [クラウドLIVEの実測と制約](docs/cloud-live.md) |

生成入力は校正済みの複数カメラ素材です。LIVEの実カメラ入力・継続30秒以内の遅延、スマートフォン実機の性能、大規模同時視聴は未検証または未達です。GoのABR基準実装は実Gaussianの品質切替再生へ未接続です。実装と残る課題は[バックエンドの現状](docs/backend-status.md)、画質・容量の比較条件は[ML・データの資料](docs/README.md#mlデータ)を参照してください。

## はじめる

```bash
git clone https://github.com/Hosi121/rangle.git
cd rangle
```

Goの検証版は `go.mod` と同じ1.27.3です。小さな合成資産のHTTP配信とABR判断を確認できます。

```bash
go run ./cmd/abr-sim
go run ./cmd/rangle-dev
```

`rangle-dev` の起動中、別のターミナルから取得します。終了はCtrl+Cです。

```bash
curl http://127.0.0.1:8080/manifests/demo/synthetic-v1.json
curl -i -H 'Range: bytes=0-15' http://127.0.0.1:8080/assets/demo/synthetic-v1/low/segment-0.bin
```

このデモは合成データで、実4DGSの復号・描画や実回線での高速化を実証するものではありません。

画面を動かす場合は[Flutter Webの開発・ビルド](apps/web/README.md#開発とビルド)、コンテナを使う場合は[Dockerの起動手順](docs/docker.md)へ進んでください。手元の `.splat` フレーム列は[ローカルsceneのGo配信](docs/local-server.md)、RGS1時間モデルやフィードからの実資産取得は[APIと版付き資産](docs/api-delivery.md)にまとめています。実データ・学習重み・認証情報はGitへ追加しません。

## 資料と開発

[ドキュメント一覧](docs/README.md)から、起動手順、構成・再生、API・配信、ML・データ、インフラ・配備、研究・現状へ進めます。初期案と現在の実装・実測記録を分けて案内しています。

| 場所 | 内容 |
| --- | --- |
| `cmd/`・`internal/` | GoのAPI・配信・生成制御・ABR |
| [apps/web/](apps/web/README.md) | Flutter UIとJavaScript viewer |
| `ml/` | Pythonの入力前処理・学習・packing |
| [infra/gcp/](infra/gcp/README.md) | TerraformによるGCP構成 |
| [testdata/contracts/v1/](testdata/contracts/v1/README.md) | Go・Python・JavaScript・Dartの共有契約テストとconsumer matrix。50件の合成fixtureを使用 |

開発の正本は [Hosi121/rangle](https://github.com/Hosi121/rangle)（`origin`）です。作業ブランチから `stg` へPRを出し、安定版は `stg` から `main` へのPRで反映します。検証・PRの手順は[開発ルール](CONTRIBUTING.md)を参照してください。

DevFest Hackathon 2026の最終提出先は [Devfest-Hackathon-2026/df-2026-t002](https://github.com/Devfest-Hackathon-2026/df-2026-t002)（`submission`）です。通常の開発・pushと提出を分け、提出先の制約は[提出案内](docs/submission.md)に従います。

## ライセンス

自作コードは [MIT License](LICENSE) です。利用するデータセット、外部コード、モデルには、それぞれの利用条件が適用されます。内蔵サンプルの出典は[ATTRIBUTION](apps/web/assets/samples/ATTRIBUTION.md)、素材ごとの条件はML・データの各資料に記載しています。
