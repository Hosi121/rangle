# rangle Web

スポーツのハイライトを切り替える、Flutter / DartのWebフロントエンドです。日本語UIとMaterial 3を使います。

先頭の **VRU Basketball** は、実験で再構成した480×270・25fps・0.8秒の通常動画サンプルです。出典はAVS-VRU / Jiahao Wuほか（CC BY 4.0）。詳細は [assets/samples/ATTRIBUTION.md](assets/samples/ATTRIBUTION.md) に記載しています。残り2項目は準備中です。動画は自動再生せず、再生・停止・シーク・ミュートに対応します。

`RANGLE_SCENE_URL` を設定すると、同じカードを3D Gaussian Splatting表示へ切り替えられます。ドラッグで視点を回転し、スクロールで拡大縮小、スライダーで静止時刻を選びます。自由視点モードでは既存の前後ボタンで項目を切り替え、モードを切り替える際に前の再生資源を破棄します。

現在の自由視点は時刻シークに対応します。連続3D再生とABR配信は未実装で、端末での描画性能は別途測定します。

## 開発とビルド

検証環境はFlutter 3.47.9 stable / Dart 3.13.7、Node 24です。Dart依存は`pubspec.lock`、JS依存は`viewer/package-lock.json`で固定しています。SDKやグローバルpackageをリポジトリに追加する必要はありません。

```bash
cd apps/web
npm ci --prefix viewer
npm run build --prefix viewer
flutter --suppress-analytics pub get --enforce-lockfile
flutter --suppress-analytics run -d web-server --web-hostname 127.0.0.1 --web-port 8080
```

自由視点を有効にする場合は、HTTP(S)で取得できるscene manifestを指定します。外部動画で先頭サンプルを置き換える場合だけ`RANGLE_PREVIEW_URL`を追加します。外部overrideはHTTP(S)のみ受け付け、内蔵動画は`VideoPlayerController.asset`で再生します。

```bash
flutter --suppress-analytics build web --no-web-resources-cdn \
  --dart-define=RANGLE_SCENE_URL=http://127.0.0.1:4174/samples/basketball/scene.json
```

`build/web/`を通常のHTTPサーバーで配信します。ローカル検証では、利用が許可されたscene.jsonとframes/を`build/web/samples/basketball/`へ置き、同じoriginの4174番で配信できます。sceneや学習重みはGitへ追加しません。HTTPSのアプリではscene・frame・動画もHTTPSとし、別originの場合は配信元で必要なCORSを設定してください。URLに認証情報や秘密を含めないでください。サブパス配信では`--base-href`を設定します。

JS viewerは`@mkkellogg/gaussian-splats-3d@0.4.7`と`three@0.160.0`（ともにMIT）をローカルにbundleします。CDNへ依存しません。`npm run build`は`web/viewer/viewer.js`と配布用`THIRD_PARTY_LICENSES.txt`を生成します。どちらも生成物としてGitから除外しています。

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

frame切替は1つずつ処理し、切替中の追加seekは最後の要求へ集約します。orbitのカメラ状態は時刻間で保持します。初回読み込みやseekの失敗にはカード内で再試行を示します。失敗後の遅い応答で表示が復活しないようにしています。

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

`ViewerAdapter`は従来の動画APIを保ち、`Clip.sceneUrl`で自由視点の参照を追加しています。`RANGLE_SCENE_URL`が空なら自由視点ボタンは無効です。Goプロトタイプの`/manifests/{clip_id}/{revision}.json`と合成`.bin`は別の転送契約であり、このscene manifestや通常動画には流用しません。
