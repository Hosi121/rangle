# rangle Web

スポーツのハイライトを切り替える、Flutter / DartのWebフロントエンドです。日本語UIとMaterial 3を使います。

既定では同一originの `/api/v1/clips` から動画・sceneのURLを読み込みます。`RANGLE_DEMO=true` を明示したビルドだけ内蔵の3件のデモを使います。API URLの未指定・空文字でもデモへ切り替えません。デモ先頭の **VRU Basketball** は、実験で再構成した480×270・25fps・0.8秒の通常動画サンプルです。出典はAVS-VRU / Jiahao Wuほか（CC BY 4.0）。詳細は [assets/samples/ATTRIBUTION.md](assets/samples/ATTRIBUTION.md) に記載しています。

モバイルは1項目を画面いっぱいに表示する縦型フィード、デスクトップは中央の縦長プレイヤーです。映像は元の縦横比を保ち、人物を埋め尽くすためにcropしません。通常動画は映像タップで再生・停止し、再生位置は最下端の細いシークバーで操作します。ホバー・タッチ操作中はその時刻のプレビューを表示し、フレームを取得できない場合は時刻だけを表示します。出典を含む説明全文は「出典・詳細」から読めます。通常動画では上下スワイプ、自由視点では視点操作と競合しない前後ボタンで項目を移動できます。

表示中の通常動画は無音での自動再生とループを試みます。ブラウザの再生制限で拒否された場合は、一度だけプレイヤーを作り直して手動再生へ戻します。動画取得やdecodeの失敗は通常のエラー・再試行として扱います。再生・停止・シーク・ミュートも手動で操作できます。

デモビルドで `RANGLE_SCENE_URL` を設定すると、同じカードを3D Gaussian Splatting表示へ切り替えられます。ドラッグで視点を回転し、スクロールで拡大縮小、最下端のシークバーで時刻を選びます。時間モデルを含むシーンは、ダブルクリック・ダブルタップでループ再生でき、再生中も視点を動かせます。動画と自由視点の再生・停止は、フォーカスしてスペースキーでも操作できます。

初回の自由視点準備中も通常動画を操作でき、最初のGPU描画が終わってから切り替えます。動画へ戻ると自由視点の描画を止め、同じ項目を表示している間は再利用します。準備のキャンセル・項目の移動・エラー時は資源を破棄します。既存の`.splat`フレーム列は時刻シークのみです。ABRとの接続とスマートフォン実機の評価は未実施です。

画面上部の「生成」、または `/demo/?generate=1` から生成画面を開けます。既存アカウントでログインし、確認済みpresetを選んで静的3DGSまたは動く4DGSを生成し、状態・中止・完成した結果を確認できます。選んだデータの方式と進行中ジョブの方式を表示します。生成結果のリンクは自由視点から開きます。任意動画のuploadは扱いません。生成APIが無効な環境では準備中と表示します。

## 開発とビルド

検証環境はFlutter 3.47.9 stable / Dart 3.13.7、Node 24です。Dart依存は`pubspec.lock`、JS依存は`viewer/package-lock.json`で固定しています。SDKやグローバルpackageをリポジトリに追加する必要はありません。

```bash
cd apps/web
npm ci --prefix viewer
npm run build --prefix viewer
flutter --suppress-analytics pub get --enforce-lockfile
flutter --suppress-analytics run -d web-server --web-hostname 127.0.0.1 --web-port 8080 \
  --dart-define=RANGLE_DEMO=true
```

開発時のデモは `http://127.0.0.1:8080/demo/` を開きます。`web/index.html` のbase hrefは `/demo/` に固定しています。製品入口と配信APIを含めた確認には、以下のGoサーバーを使います。

### APIから動画・自由視点を開く

```bash
flutter --suppress-analytics build web --release --no-web-resources-cdn \
  --dart-define=RANGLE_DEMO=false \
  --dart-define=RANGLE_API_URL=/api/v1/clips
```

リポジトリのrootから、[catalogと資産の配置](../../docs/api-delivery.md)に従って起動します。

```bash
go run ./cmd/rangle-server -ui-root apps/web/build/web \
  -catalog .local/catalog.json -asset-root .local/published
```

`http://127.0.0.1:4174/demo/`を開き、映像タップで動画を再生 →「自由視点で見る」→ ダブルタップまたはダブルクリックで動きを流す → シーンをドラッグして視点を回す →「動画に戻る」の順に操作できます。時刻は最下端のシークバーで操作できます。scene URLはcatalogの`scene_url`から読み、モデルや各frameは版付きHTTP資産として配信します。上のAPI URLは同じoriginを使うため、`localhost`でも開けます。

初期版は先頭の1ページだけを読みます（API既定10件、最大50件）。自動追加読込・ABR制御は未実装です。API URLの不正・未設定のcatalog・API失敗時はエラーまたは空状態を表示し、デモへ自動で切り替えません。`RANGLE_API_URL` は `/api/v1/clips` のようなroot相対path、または認証情報・fragmentを含まないHTTP(S)絶対URLです。外部APIを指定する場合は、そのAPIと資産にCORSが必要です。

設定はコンパイル時に固定します。`docker run -e RANGLE_DEMO=...` 等のruntime環境変数では切り替わりません。

| dart-define | 既定・用途 |
| --- | --- |
| `RANGLE_DEMO` | `false`。`true` のビルドだけサンプルを使う |
| `RANGLE_API_URL` | 未指定・空文字は同一originの `/api/v1/clips` |
| `RANGLE_PREVIEW_URL` / `RANGLE_SCENE_URL` | 明示デモ専用。APIモードではcatalogのURLを使う |

### APIを使わないデモ

`RANGLE_DEMO=true` を必ず指定します。自由視点を有効にする場合は、HTTP(S)で取得できるscene manifestを指定します。外部動画で先頭サンプルを置き換える場合だけ`RANGLE_PREVIEW_URL`を追加します。外部overrideはHTTP(S)のみ受け付け、内蔵動画は`VideoPlayerController.asset`で再生します。

```bash
flutter --suppress-analytics build web --release --no-web-resources-cdn \
  --dart-define=RANGLE_DEMO=true \
  --dart-define=RANGLE_SCENE_URL=http://127.0.0.1:4174/samples/basketball/scene.json
```

通常のビルドは `build/web/index.html`（`/demo/` のWeb UI）と `build/web/landing.html`（製品入口）を生成します。リポジトリのrootから、利用が許可されたsceneのディレクトリを指定して起動します。

```bash
go run ./cmd/rangle-server -ui-root apps/web/build/web \
  -scene-dir /path/to/local/scene -scene-id basketball
```

`http://127.0.0.1:4174/` が入口、`http://127.0.0.1:4174/demo/` がWeb UIです。`/demo/` は配信pathで、デモモードを選ぶ設定ではありません。scene・API・配信資産のURLは `/demo/` を付けず、従来どおり使います。sceneや学習重みはGitへ追加しません。HTTPSのアプリではscene・frame・動画もHTTPSとし、別originの場合は配信元で必要なCORSを設定してください。URLに認証情報や秘密を含めないでください。デモ用の静的ファイルは `/demo/` へmountし、`--base-href` の追加指定やビルド後のファイル移動は不要です。

JS viewerは`@mkkellogg/gaussian-splats-3d@0.4.7`と`three@0.160.0`（ともにMIT）をローカルにbundleします。CDNへ依存しません。`npm run build`はviewer・フレーム準備Worker・時間モデルWorkerと配布用`THIRD_PARTY_LICENSES.txt`を生成します。いずれも生成物としてGitから除外しています。

## scene manifest

```json
{
  "version": 1,
  "frameRate": 25,
  "camera": {
    "position": [0, 1, 3],
    "lookAt": [0, 0, 0],
    "up": [0, 1, 0],
    "fovYDegrees": 50
  },
  "frames": ["frames/000.splat", "frames/001.splat"],
  "attribution": {
    "label": "サンプルの出典",
    "source": "https://example.com/source",
    "license": "CC BY 4.0"
  }
}
```

`frames`は非空の`.splat` URL配列、`frameRate`は正の有限数です。frame数とrateはmanifestから読み、例の2frameやサンプルの20frameに固定しません。相対frame URLはmanifestの最終URLを基準に解決します。上の例では最後の時刻は0.04秒、20frame/25fpsでは0.76秒です。各ファイルはその時刻の実際の3D Gaussianを持つ必要があります。

連続再生には`frames`の代わりに`"frameCount": 20, "temporal": { "url": "model.rgs" }`を指定します。RGS1は速度と時間方向の不透明度を持つモデルです。単一モデルを一度読み込み、GPU資源を保ったまま時間と視点を更新します。形式・対応する学習モデル・測定条件は[連続再生の設計](../../docs/temporal-playback.md)を参照してください。一般の4DGSチェックポイントを直接読める形式ではありません。

学習時の描画スケールに依存する資産には、`camera.referenceImageHeight`を指定できます（64〜2160の整数pixel、例：480×270で学習した資産なら270）。指定時はdrawing bufferの高さをその値に保ち、幅は表示領域の縦横比に合わせます。CSSの表示サイズ・縦画角・視点操作は維持します。これは端末DPRから独立したモデルの描画条件であり、解像度を上げるほど高画質になるという指定ではありません。未指定時は従来どおり端末DPRを使います。[比較結果と制約](../../docs/gaussian-quality.md)を参照してください。

校正済みのシーンでは、`camera.intrinsics`に`{ "width": 960, "height": 803, "fx": 915.1, "fy": 915.3, "cx": 476.4, "cy": 407.3 }`のようなpinhole内部パラメータを指定できます。画素座標の原点は左上で、`width`/`height`は校正画像の寸法です。指定時は`fy`から縦画角を求め、`fx/fy`の差と主点のずれも描画へ反映します。表示領域の縦横比が変わると縦画角を保ったまま左右の見える範囲が変わります。指定しないシーンは従来の`fovYDegrees`を使います。pinholeで表せる画像を入力とし、歪み補正やresizeを行った場合は対応するKを渡します。ビューア内ではレンズ歪みの補正を行いません。

時刻切替は1つずつ処理し、切替中の追加seekは最後の要求へ集約します。orbitのカメラ状態は時刻間で保持します。準備に失敗した場合は通常動画と再試行操作を残します。失敗後の遅い応答で表示が復活しないようにしています。

フレーム列の取得・parse・分割・packingはWeb Workerで行い、準備したbufferをtransferします。時間モデルでは同じWorkerが初回の取得・parseと、再生中の深度ソートを担当します。入力は1資産32MiBまでです。GPU転送と初回mesh構築はメインスレッドに残ります。[資源管理・SDKの変更・測定の範囲](../../docs/client-processing.md)を参照してください。

viewerは同じoriginのiframe内でbody直下に生成します。Flutterとの小さなmessage APIはorigin・source・channelを確認します。これは上流viewerのDOM所有権に合わせ、離脱時に非同期disposeとiframe破棄を行うためです。非表示時は描画ループも止めます。SharedArrayBufferとGPU sortは有効にしていません。

## 検証

```bash
npm test --prefix viewer
npm run build --prefix viewer
flutter --suppress-analytics analyze
flutter --suppress-analytics test
flutter --suppress-analytics build web --no-web-resources-cdn
```

JSテストはmanifest、異なるframe数/rate、最新seekへの集約、カメラ保持、失敗・離脱中の破棄をGPUなしで検証します。Flutterテストは既存のフィードと動画操作に加え、asset参照、動画と自由視点の切替、seek、再試行、離脱時の破棄を検証します。実ブラウザのWebGL描画、orbit、コーデック、HTTP/CORS、worker解放は、実際の配信資産を使うブラウザ確認が必要です。

`ViewerAdapter`は従来の動画APIを保ち、`Clip.sceneUrl`で自由視点の参照を追加しています。APIモードは`scene_url`、デモモードは`RANGLE_SCENE_URL`が未設定なら自由視点ボタンを無効にします。Goの`/manifests/{clip_id}/{revision}.json`は配信契約です。描画用scene manifestとは役割が異なり、この画面ではABR判断にまだ使いません。
