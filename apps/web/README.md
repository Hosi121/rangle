# rangle Web

スポーツのハイライトを上下に切り替える、Flutter / Dart の最小 Web フロントエンドです。日本語 UI と Material 3 を使います。

実装した範囲は、縦型フィード、前後ボタン、↑↓ / PageUp / PageDown キー、デスクトップのカタログ選択、通常動画の再生・一時停止・シーク・ミュートです。選択から外れた動画は停止し、プレイヤーを破棄します。非表示になったアプリも再生を停止します。自動再生はしません。

**初期表示は映像を含まないデモカタログです。** 3項目のタイトルや説明は画面・操作確認用で、実在する試合のメタデータではありません。写真・動画・モデル・学習データは同梱していません。自由視点の操作は準備中として無効にしており、固定視点の映像を回転したように見せる操作はありません。

## 実行環境

検証した版は **Flutter 3.47.9 stable / Dart 3.13.7** です。依存パッケージは `pubspec.lock` で固定しています。ランタイムの追加パッケージは Flutter 公式の `video_player` だけです（日本語ローカライズは SDK 付属）。

Flutter SDK はリポジトリの外へ置いてください。以下の `flutter` は、利用する SDK の実行ファイルに置き換えることもできます。

公式 SDK: [Flutter SDK archive](https://docs.flutter.dev/install/archive)。検証に使った Linux x64 のアーカイブは `https://storage.googleapis.com/flutter_infra_release/releases/stable/linux/flutter_linux_3.47.9-stable.tar.xz`、SHA256 は `25ed8c3c29d51a7353f2a944310b478af8214920639524b19c50a19e6c044c5a` です。

リポジトリのルートから:

```bash
cd apps/web
flutter --suppress-analytics pub get --enforce-lockfile
flutter --suppress-analytics run -d web-server --web-hostname 127.0.0.1 --web-port 8080
```

表示された `http://127.0.0.1:8080` をブラウザで開きます。SDK の起動設定やグローバル PATH を変更する必要はありません。ローカルサーバーは作業後に Ctrl+C で終了してください。

## 実際のプレビュー映像をつなぐ

利用が許可された、ブラウザで直接再生できる HTTP(S) の動画 URL をビルド時に指定します。MP4 の場合はブラウザが対応するコーデック（例: H.264）と正しい `Content-Type` を使い、シークする場合は配信側の Range 対応も確認してください。

```bash
flutter --suppress-analytics run -d web-server \
  --web-hostname 127.0.0.1 --web-port 8080 \
  --dart-define=RANGLE_PREVIEW_URL=https://your-media.example/preview.mp4
```

設定した映像は先頭の項目にだけ表示され、「通常動画」と明示します。残りの項目は準備中です。設定が空なら外部メディアを取得しません。HTTP(S) 以外の URL はカタログの読み込みエラーになります。HTTPS のページでは動画も HTTPS にしてください。配信元で必要な CORS、MIME、キャッシュ方針を設定してください。URL は公開クライアントに含まれるため、認証情報や秘密を埋め込まないでください。

映像の準備中・読み込み中・再生エラーでは再生とシークを無効にします。読み込みが15秒以内に完了しない場合もエラーを表示し、再試行できます。カタログ全体にも読み込み・空・エラー・再読み込みの状態があります。

## ビルドと検証

```bash
flutter --suppress-analytics analyze
flutter --suppress-analytics test
flutter --suppress-analytics build web --no-web-resources-cdn
```

通常動画を組み込むビルドでは、最後のコマンドに `--dart-define=RANGLE_PREVIEW_URL=...` を追加します。`build/web/` を静的 HTTP サーバーから配信してください。ローカルファイルの直接オープンには対応しません。サブパスで配信する場合は `--base-href=/your-path/` を指定します。SDK・キャッシュ・ビルド成果物は Git に追加しません。

ウィジェットテストはソフトウェア描画で実行されます。フィードの移動、モバイルと横持ちレイアウト、準備中の無効操作、カタログの読み込み・空・エラー復帰、メディアの再生操作とエラー再試行、選択から外れたプレイヤーの破棄を検証します。動画部分には fake adapter を使うため、実ブラウザのコーデック対応・Range/CORS・映像再生性能は別途、利用する映像とブラウザで確認してください。

## 構成と次の接続点

- `lib/models/clip.dart`: フィードのメタデータと通常動画の参照。
- `lib/data/clip_repository.dart`: 小さなリポジトリ契約とローカルのデモカタログ。実 API をつなぐ場合は `ClipRepository.load()` を実装します。
- `lib/viewer/viewer_adapter.dart`: 再生状態・時刻・再生制御・描画・破棄の接続点。現在は `VideoPreviewAdapter` が `video_player` に委譲します。
- `lib/widgets/preview_viewer.dart`: 読み込み・エラー・再試行、メディア操作、アプリの表示状態の管理。
- `lib/widgets/clip_card.dart`: クリップのカード表示と、映像準備中の操作表示。
- `lib/app.dart`: フィードとレスポンシブなレイアウト。選択中の項目にだけプレイヤーを作成します。

Go 配信プロトタイプの `/manifests/{clip_id}/{revision}.json` は、独立した区間・品質・チェックサム等の転送契約です。現状の合成 `.bin` は動画でも4DGSの可視化資産でもなく、`RANGLE_PREVIEW_URL` には指定できません。このアプリはそのマニフェストを取得・復号していません。

実際の4DGSを接続する段階で、検証済みの復号・描画処理を `ViewerAdapter` の別実装として追加し、シーン参照とカメラ操作の契約を拡張します。通常動画のプレビューと自由視点の準備状態を区別したまま接続してください。カスタム転送、認証、課金、推薦、分析、クラウドの作成やデプロイは含まれていません。
