# rangle ドキュメント

[製品概要・最初の起動](../README.md) · [公開アプリ](https://app.rangleplay.stream/) · [開発ルール](../CONTRIBUTING.md)

初めて読む方・審査員は製品概要から、実装を追う方は目的に合う入口から進んでください。実装状況は[バックエンドの現状](backend-status.md)、実験の数値は各資料のデータ・ハードウェア・評価条件と合わせて確認します。

| 読みたいこと | 入口 |
| --- | --- |
| 手元でアプリや配信を動かす | [起動手順](#起動手順) |
| 全体構成と自由視点再生を理解する | [構成・再生](#構成再生) |
| API・資産公開・互換性を確認する | [API・配信](#api配信) |
| 入力データ・学習・画質・容量を確認する | [ML・データ](#mlデータ) |
| 配備・権限・運用を確認する | [インフラ・配備](#インフラ配備) |
| 到達点・未解決課題・初期判断を読む | [研究・現状](#研究現状) |

## 起動手順

- [Flutter Web](../apps/web/README.md)：SDK、viewerのbuild、内蔵デモとAPIモード、操作方法。
- [Docker](docker.md)：GoとWebを一緒にbuild・起動する。最小起動とフィード接続の確認を分ける。
- [ローカルsceneのGo配信](local-server.md)：手元のWebビルド・`.splat`フレーム列の配信と公開範囲。RGS1やcatalogを使う場合は[APIの起動例](api-delivery.md#公開するcatalog)。

## 構成・再生

- [クラウド構成](cloud-architecture.md)：Go/UI、CDN・Storage、配備経路の全体図。逐次生成は[クラウドLIVE](cloud-live.md)。
- [自由視点の連続再生](temporal-playback.md)：RGS1形式、時間評価、GPU描画、資源管理と測定条件。[クライアント処理](client-processing.md)は取得・復号・Workerの分担を扱う。
- [LIVE配信](live-streaming.md)：index・モデル更新・クライアントの再接続契約。学習と起動待ちを含む実測は[クラウドLIVE](cloud-live.md#2026-11-09の実gpu検証)。

## API・配信

- [APIと版付き資産](api-delivery.md)：catalog、manifest、URL、Range・ETag・cache。[フィード継続取得](feed-pagination.md)と[過負荷時の動作](api-overload.md)は個別の契約。
- [LIVE配信の測定](live-delivery-measurements.md)：APIの変更前後、実HTTP、CDNの少数取得。測れた範囲と未検証の規模を分ける。
- [アカウントと交流API](accounts.md)：認証、いいね、コメント。[交流データの保存先](community-storage.md)に永続化と分離の条件を記載。
- [生成API](generation.md)：preset、状態確認、中止、予算。[生成結果の掲載](generated-publication.md)と[掲載UI](generation-publication-ui.md)は成功後の公開操作を扱う。
- [資産公開preflight](asset-publication.md)：許諾台帳・許可リスト・bytes検証・公開順。[不変catalog](catalog-snapshots.md)は選択・切替・復帰の手順。
- [配信部品の共通契約](streaming-contract.md)：Goのmanifest・HTTP・ABRの境界。[HTTP採用理由](http-delivery-decision.md)も参照。
- [言語間のmedia契約fixture](../testdata/contracts/v1/README.md)：schema、50件の合成入力、実consumerの適用範囲と既知の差分。形式の正本はこのディレクトリとproduction validatorに置く。

## ML・データ

- [入力前処理](dataset-preprocessing.md)：校正済み画像・動画・点群の準備と来歴。[MLコンテナ](../ml/container.md)は再現環境のbuildとテスト。
- [動的4DGS学習](dynamic-gs-training.md)：共同学習、入力制約、評価、保存形式。[生成仕様](generation.md#学習と結果)から静的3DGSとの違いを確認できる。
- [学習品質の比較](training-quality.md)：現行profileの採用判断とheld-out評価。[時間付きモデルの圧縮比較](temporal-compression-comparison.md)は同じモデルでの容量・誤差を扱う。
- [可逆圧縮の形式](gaussian-compression.md)：RGS1-Zのpacking・復号条件。元のfloatモデルから保存形式への量子化とは区別する。
- [到着画像によるライブ学習](live-training.md)：オンライン更新、録画入力、測定範囲。[クラウドLIVEの実測](cloud-live.md)は起動待ち・公開遅延・破棄windowを含む。
- [初期VRUの画質・描画比較](gaussian-quality.md)と[UVG-GS人物シーンの学習比較](gaussian-training.md)：素材・方式の異なる過去の実験記録。数値を直接横比較しない。

## インフラ・配備

- [発表用stgの配備](stg-deployment.md)：対象環境、IAM、予算・期限停止、検証の到達点。[Terraform README](../infra/gcp/README.md)はroot/moduleとローカル検証の入口。
- [CI/CD](cicd.md)：PR検証、image digest、candidate確認・traffic昇格・復帰。[Docker](docker.md#imageと配信設定の対応)にbuildとruntime設定の対応を記載。
- [生成基盤](generation-infrastructure.md)：有限GPU job、入力・結果の権限とconfig。[クラウドLIVE](cloud-live.md#権限と配備)はLIVE専用の拡張。
- [ログ方針](logging.md)：Go・Pythonの出力と属性、記録する境界。

## 研究・現状

- [バックエンドと配信の現状](backend-status.md)：実装済み・接続済み・未検証の範囲と、残る課題へのリンク。大規模視聴や端末性能の成立とは分ける。
- [4DGSの画質と容量の研究調査](research/gs-quality-compression-2026.md)：一次資料、実装・licenseの確認、次の比較案。提案段階で、新しい非可逆方式の採用結果ではない。
- [企画書](proposal.md)・[技術選定案](technology-selection.md)・[初期インフラ構成案](infrastructure-design.md)・[ABR調査](streaming-research.md)：初期の目的・候補・判断根拠を残した資料。記載された候補を現在の採用構成と読み替えない。
- [DevFest提出案内](submission.md)：開発用と最終提出用のリポジトリ、提出先の制約と運営案内。

データセット・外部コードの出典と利用条件は、各素材・実験の資料と[内蔵サンプルの出典](../apps/web/assets/samples/ATTRIBUTION.md)に記載しています。
