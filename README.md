# rangle

**replay + angle**。スポーツの決定的瞬間を、自分の視点で見返す。

rangleは、実写から再構成したスポーツのハイライトを縦型フィードで楽しみ、気になった瞬間には時間と視点を動かして見返せるモバイルプラットフォームです。

既存の多視点データセットを使い、クラウドでの動的Gaussian Splattingの再構成、モバイル向けの圧縮・分割配信、端末での復号・GPU描画を一つの流れにつなぐ構成を検討しています。Go、ML処理、モバイルアプリをモノレポで管理します。

現在は企画・技術選定と、Goによる配信部品の初期実装を進めています。ABR制御とHTTP区間配信は合成データで検証し、実際の4DGS再構成・モバイル描画・性能検証はこれから進めます。

## 資料

- [企画書 草案](docs/proposal.md)
- [技術選定案](docs/technology-selection.md)
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

## リポジトリ

| 用途 | リポジトリ | リモート名 |
| --- | --- | --- |
| 開発の正本 | [Hosi121/rangle](https://github.com/Hosi121/rangle) | `origin` |
| DevFest Hackathon 2026の最終提出 | [Devfest-Hackathon-2026/df-2026-t002](https://github.com/Devfest-Hackathon-2026/df-2026-t002) | `submission` |

通常の開発とpushは `origin` に対して行います。最終提出時に、必要なコードと資料を提出用リポジトリへ反映します。提出用の運用制約と期限は [提出案内](docs/submission.md) を参照してください。

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
