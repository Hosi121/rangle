# rangle

**replay + angle**。スポーツの決定的瞬間を、自分の視点で見返す。

rangleは、実写から再構成したスポーツのハイライトを縦型フィードで楽しみ、気になった瞬間には時間と視点を動かして見返せるモバイルプラットフォームです。

既存の多視点データセットを使い、クラウドでの動的Gaussian Splattingの再構成、モバイル向けの圧縮・分割配信、端末での復号・GPU描画を一つの流れにつなぐ構成を検討しています。Go、ML処理、モバイルアプリをモノレポで管理します。

GoによるABR制御・HTTP区間配信と、Flutter Webの縦型フィードを実装しています。配信部品は合成データ、Webは通常動画で確認しています。4DGSの配信・自由視点描画との接続は未実装です。

## 資料

- [Webアプリの実行方法](apps/web/README.md)
- [企画書 草案](docs/proposal.md)
- [技術選定案](docs/technology-selection.md)
- [ABRと配信最適化の調査](docs/streaming-research.md)
- [配信部品の共通契約](docs/streaming-contract.md)
- [DevFest提出用リポジトリの案内](docs/submission.md)

## 配信部品の動作確認

Go 1.23以降を利用します。初期実装は外部依存なしで動きます。

```bash
go test -race ./...
go vet ./...
go run ./cmd/abr-sim
```

ローカルの区間配信デモを起動します。終了はCtrl+Cです。

```bash
go run ./cmd/rangle-dev
```

別のターミナルから、マニフェストと資産を取得できます。

```bash
curl http://127.0.0.1:8080/manifests/demo/synthetic-v1.json
curl -i -H 'Range: bytes=0-15' http://127.0.0.1:8080/assets/demo/synthetic-v1/low/segment-0.bin
```

デモの資産と帯域系列は合成データです。実際の4DGSの復号やHLS/DASH再生、実回線での高速化を実証するものではありません。GoのABR制御は、モバイル実装へ判断規則とテストケースを引き継ぐための基準です。

## ログ

Goコマンドのログは標準 `log/slog` を使い、INFO以上のJSONをstderrへ出力します。`service` でコマンドを識別し、コマンドの失敗には `error` を付けます。`rangle-dev` は起動・終了とHTTPサーバー内部エラーを記録し、起動時に `listen_addr`、`manifest_url`、`fixture="synthetic"` を示します。`abr-sim` の計算結果はstdout、ログはエラー時だけです。

## リポジトリ

| 用途 | リポジトリ | リモート名 |
| --- | --- | --- |
| 開発の正本 | [Hosi121/rangle](https://github.com/Hosi121/rangle) | `origin` |
| DevFest Hackathon 2026の最終提出 | [Devfest-Hackathon-2026/df-2026-t002](https://github.com/Devfest-Hackathon-2026/df-2026-t002) | `submission` |

通常の開発とpushは `origin` に対して行います。最終提出時に、必要なコードと資料を提出用リポジトリへ反映します。提出用の運用制約と期限は [提出案内](docs/submission.md) を参照してください。

## 開発フロー

常設ブランチは `stg` と `main` です。作業ブランチから `stg` へPRを出し、安定版の反映は `stg` から `main` へのPRで行います。[開発ルール](CONTRIBUTING.md) を参照してください。

## 開発用リポジトリの取得

```bash
git clone https://github.com/Hosi121/rangle.git
cd rangle
```

提出用リモートが必要な場合は追加します。

```bash
git remote add submission https://github.com/Devfest-Hackathon-2026/df-2026-t002.git
```

## ライセンス

自作コードは [MIT License](LICENSE) です。利用するデータセット、外部コード、モデルには、それぞれの利用条件が適用されます。
