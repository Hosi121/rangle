# rangle Web

[製品概要](../../README.md) · [資料一覧](../../docs/README.md)

Flutter/Dart・日本語Material3のスポーツfeed。既定はsame-origin `/api/v1/clips`、明示 `RANGLE_DEMO=true` だけ内蔵3件を使う。空URL/未設定/API失敗でdemoへfallbackしない。先頭demoはVRU Basketball（480×270・25fps・0.8秒）、AVS-VRU/Jiahao Wuほか、CC BY4.0。[出典](assets/samples/ATTRIBUTION.md)

mobileは全面縦feed、desktopは中央縦長player。元縦横比を保ちcropしない。映像tap/spaceで再生停止、下端seekbarのhover/touchでpreview（取得不可は時刻だけ）、概要で出典/全文を表示する。無音autoplay/loopを試し、再生制限時は1回だけcontrollerを再作成して手動再生へ戻す。取得/decode失敗はerror/retry、muteも手動操作可能。

キーワード・ハッシュタグ検索はサムネイル付きの結果一覧を表示し、選んだ動画を再生する。再生画面上の検索語から結果へ戻れる。一覧は20件ずつ取得し、GSや再生playerを作らず、専用decoder1件と最大48枚の小さなJPEGでpreviewを表示する。

自由視点はdrag/orbit・scroll/zoom・seek、時間modelはdouble click/tapで再生停止する。入場・動画への復帰・資源保持は[没入UI](../../docs/immersive-ui.md)、Worker/SDKは[client処理](../../docs/client-processing.md)、時間model/評価は[連続再生](../../docs/temporal-playback.md)を参照。[transport再生](../../docs/transport-playback.md)はAPI/manifest・ABR判断・区間描画へ接続済み。実品質ラダー/スマートフォン実機評価は未実施。

管理者はfeedの代わりに生成/LIVE管理へ進む。`/demo/?generate=1` も管理者専用。承認presetの静的/動的生成・状態・中止・結果を扱い、API無効時は準備中とする。任意動画uploadは扱わない。[生成API](../../docs/generation.md)・[掲載UI](../../docs/generation-publication-ui.md)

## 開発とビルド

検証版はFlutter 3.47.9 stable / Dart 3.13.7・Node 24。Dartはpubspec.lock、JSはviewer/package-lock.jsonで固定する。SDK/global packageをrepoへ追加しない。

```bash
cd apps/web
npm ci --prefix viewer
npm run build --prefix viewer
flutter --suppress-analytics pub get --enforce-lockfile
flutter --suppress-analytics run -d web-server --web-hostname 127.0.0.1 --web-port 8080 \
  --dart-define=RANGLE_DEMO=true
```

`http://127.0.0.1:8080/demo/` を開く。base hrefは `/demo/` 固定。製品入口/APIも使う確認はGo serverで行う。

### APIから動画・自由視点を開く

```bash
flutter --suppress-analytics build web --release --no-web-resources-cdn \
  --dart-define=RANGLE_DEMO=false \
  --dart-define=RANGLE_API_URL=/api/v1/clips
```

repo rootから[catalog/資産を配置](../../docs/api-delivery.md)して起動する。

```bash
go run ./cmd/rangle-server -ui-root apps/web/build/web \
  -catalog .local/catalog.json -asset-root .local/published
```

`http://127.0.0.1:4174/demo/` で動画再生 →「自由視点で見る」→ double tap/clickで再生 → dragで視点 →「動画に戻る」を確認する。時刻は下端seekbar、scene_urlはcatalog、model/frameは版付きHTTP資産。same-originのためlocalhostでも開ける。

feedは20件ずつ、metadata最大100件の前進window（[paging](../../docs/feed-pagination.md)）。不正API URL・catalog未設定・API失敗はerror/空状態。API URLはroot相対か、認証情報/fragmentなしのHTTP(S)絶対URLで、外部API/資産にはCORSが必要。

設定はcompile時固定で、`docker run -e RANGLE_DEMO=...` では変わらない。

| dart-define | 既定・用途 |
| --- | --- |
| `RANGLE_DEMO` | false。trueだけ内蔵sample |
| `RANGLE_API_URL` | 未指定/空はsame-origin `/api/v1/clips` |
| `RANGLE_PREVIEW_URL` / `RANGLE_SCENE_URL` | 明示demo専用。API modeはcatalog URL |

### APIを使わないデモ

`RANGLE_DEMO=true` を必須とし、自由視点用にHTTP(S) sceneを指定する。先頭動画の置換だけ `RANGLE_PREVIEW_URL` を追加する。overrideはHTTP(S)のみ、内蔵動画はVideoPlayerController.assetで再生する。

```bash
flutter --suppress-analytics build web --release --no-web-resources-cdn \
  --dart-define=RANGLE_DEMO=true \
  --dart-define=RANGLE_SCENE_URL=http://127.0.0.1:4174/samples/basketball/scene.json
```

buildはindex.html（Web UI）とlanding.html（入口）を生成する。repo rootから許可済みsceneを指定する。`-scene-dir` はsplat列用、RGS1は[catalog](../../docs/api-delivery.md)、公開範囲/cacheは[local配信](../../docs/local-server.md)に従う。

```bash
go run ./cmd/rangle-server -ui-root apps/web/build/web \
  -scene-dir /path/to/local/scene -scene-id basketball
```

入口は `http://127.0.0.1:4174/`、UIは `/demo/`。このpathはdemo modeの設定ではない。API/scene/asset URLへ `/demo/` を付けない。base-href追加/生成物移動は不要。scene/重みはGitへ加えず、URLへ秘密/認証情報を含めない。HTTPS appにはHTTPS資産、別originにはCORSが必要。

viewerはGaussianSplats3D 0.4.7 / three 0.160.0（MIT）をlocal bundleしCDNに依存しない。buildはviewer・frame/temporal Worker・THIRD_PARTY_LICENSES.txtを生成し、生成物はGit除外する。

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

| 項目 | 契約 |
| --- | --- |
| frames | 非空splat URL配列。相対URLはmanifest最終URLから解決。各fileはその時刻の実Gaussian |
| frameRate | 正の有限値。数/rateはmanifestに従い固定しない。2frame/25fpsは最終0.04秒、20frameは0.76秒 |
| temporal | framesの代わりに `"frameCount": 20, "temporal": { "url": "model.rgs" }`。RGS1は一度受信し時間/視点を更新する。一般4DGS checkpointは直接読めない |
| camera.referenceImageHeight | 任意64〜2160整数pixel。480×270学習なら270。drawing buffer高さを固定、幅は表示比率、CSS/縦画角/視点は維持。DPRと独立した描画条件で、高解像度ほど高画質の指定ではない。未指定はDPR。[比較](../../docs/archive/gaussian-quality.md) |
| camera.intrinsics | 任意pinhole `{ "width": 960, "height": 803, "fx": 915.1, "fy": 915.3, "cx": 476.4, "cy": 407.3 }`。左上原点、width/heightは校正寸法。fyから縦画角、fx/fy差・主点を反映。表示比変更は縦画角を保ち左右範囲を変更。未指定はfovYDegrees。補正/resize後のKを渡し、viewerは歪み補正しない |

seekは直列で最新要求へ集約しorbit cameraを保持する。失敗時は動画/retryを残し、遅い旧応答で復活させない。clip ID/revision/URL変更・離脱で状態を破棄し、GS cacheはactive1clipに限定する。

動画と3Dの引継ぎはnative currentTime/最後に実描画した3D時刻を採り、周期通知の遅れを避ける。通常progressでhandoffをやり直さず、明示seek/再生停止が最新要求を置換する。実描画後に切替え、戻る動画もseek/再生意図をrestoreしてから表示する。没入中の動画固定/保持は[UI契約](../../docs/immersive-ui.md#切り替えの契約)に従う。

公開codec `rangle-temporal-rgs1` の同origin/id/revisionに属する `/assets/{id}/{revision}/full/{preview.webm,scene.json}` は同入力区間・clip相対時刻0を共有する。確認済み旧basketball/生成結果だけ既知previewとの限定対応を使う。他は任意scene field `timeline: { "previewUrl": "preview.webm", "previewStartSeconds": 0, "sceneStartSeconds": 0 }` と解決済みpreview URLの完全一致で確認する。対応不明なら推測せず表示する。

時刻は秒のorigin差で変換し最寄source sampleへ丸め、動画長の割合は使わない。20sample/25fpsで0.56秒はframe14、動画末尾0.80秒は最終0.76秒へclampし調整を表示する。staticの範囲は1時刻。

iframeは `load(deferFirstFrame)` → prepared → `present(presentationId, frame, playing)` → 実描画後 `ready(presentationId)` とし旧要求で切替確定しない。非表示停止とユーザー再生意図を分け、LIVE hot replacement/VOD loopを維持する。

Workerはframe取得/parse/分割/packing、temporalでは初回準備と再生sortを担当する。1資産32MiB上限、mesh/GPU転送はmainに残る。viewerはsame-origin iframeのbody直下に作り、messageのorigin/source/channelを検証する。離脱でasync dispose+iframe破棄、非表示で描画停止。SharedArrayBuffer/GPU sortは無効。

## 検証

```bash
npm test --prefix viewer
npm run build --prefix viewer
flutter --suppress-analytics analyze
flutter --suppress-analytics test
flutter --suppress-analytics build web --no-web-resources-cdn
```

JSはmanifest・数/rate・最新seek・camera・失敗/離脱破棄をGPUなしで、Flutterはfeed/video・asset参照・切替/seek/retry/破棄を確認する。実WebGL/orbit/codec/HTTP/CORS/Worker解放は実資産のbrowser確認が必要。

ViewerAdapterは通常動画と自由視点のsourceを区別する。対応RGS1 codecではClip.manifestUrlからtransport/ABRを使い、それ以外は従来のClip.sceneUrlを使う。新しい複数区間codecでmanifestが欠ける場合は動画を維持し、先頭sceneだけへ読み替えない。Goの `/manifests/{clip_id}/{revision}.json` と描画sceneは別の形式として検証する。

## LIVEの自由視点

feedのLIVE一覧、`/demo/?live=配信ID` で開く。APIはRANGLE_API_URLと同originの `/api/v1/live` / `/api/v1/live/{id}`。戻る先はfeedまたは生成で、独立headerに表示する。

承認済みmode:live presetを[管理者生成画面](../../docs/generation.md)から開始し、既存job APIで状態/停止を扱う。準備中からLIVEを開け、履歴/直linkでも所有者に状態/停止を表示する。VOD掲載には入らない。

GPU起動と初snapshotは別段階。所有job稼働中・session未作成だけを準備中として再確認し、数分の可能性を示す。終端/不明IDの404は準備中にしない。画面を閉じてもjobは続き、明示停止が必要。

RANGLE_LIVE_MEDIA_ORIGIN指定時は一覧をAPI、選択後のpollをそのoriginの `/live-index/{id}.json` へ送る。同じsession形式・短cache/CORSが必要で、model/sceneも設定originに限定する。未指定localはAPI。

```bash
flutter --suppress-analytics build web --no-web-resources-cdn \
  --dart-define=RANGLE_API_URL=/api/v1/clips \
  --dart-define=RANGLE_LIVE_MEDIA_ORIGIN=https://media.example.test
```

表示中だけ1秒poll・同時HTTP1つ。失敗/準備中も既存modelと視点を保持し、取得先/サイズ/SHA検証後に同iframe/camera/操作状態へ更新する。GPU modelは表示+準備の最大2個。初期以外の更新失敗では旧modelを捨てない。

各区間は自動1回再生して最終frameで待つ。手動pauseは更新後も保ち、VOD loopは維持する。元映像範囲と表示区間captured_atからの概算遅延を示すが、端末時計差の影響を受ける。欠損補間・連続25fps・一定遅延は保証せず、生成/公開時間と品質は[配信側](../../docs/live-streaming.md)で評価する。

更新は固定校正/共通world座標が必要。固定SDK 0.4.7のmesh adapterを使い、新mesh描画後に旧資源を解放する。Nodeはcamera・最新集約・失敗保持・最終frame待機、DartはHTTP上限・ETag・再接続・表示継続を検証する。実GPU/端末遅延は別確認。
