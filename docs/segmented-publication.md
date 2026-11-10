# 独立区間の公開前検証

`rangle-publish`は、承認台帳とローカルの公開予定bytesを照合する既存gateです。新しいtransport codec識別子`rangle-temporal-rgs1-segments-v1`で、単一`full`表現の複数独立区間を検証できます。manifestのversionは1、各modelのRGS1 D1/Z1 binaryと復号値は変えません。

既存`rangle-temporal-rgs1`は、従来どおり`full/model.rgs`1件とsample span `(N-1)/FPS`だけを受理します。過去の承認・retained revision・通常のsingle-model公開を新profileへ読み替えません。

## 固定したprofile

各modelは同じframeCount `N`・frameRate `FPS`を持ち、local時刻0から独立復号できます。camera・座標系・attributionは共通の既存scene v1を使い、`scene.json`の`frameCount=N`、`frameRate=FPS`、`temporal.url=model.rgs`です。modelの最終sampleは`(N-1)/FPS`のままです。

transport区間のcoverageは`N/FPS`、区間`i`の`start_seconds`は`i*N/FPS`です。最終sampleから次の境界までは最後のsampleを保持します。選択した区間のmodel URLはtransport manifestが正本です。sceneだけを直接開く旧viewerは先頭modelしか開ける前提にせず、対応clientへ接続するまでは通常動画を維持します。

| 資産 | canonical名 |
| --- | --- |
| manifest | `/manifests/{clip_id}/{revision}.json` |
| 共通scene・動画 | `/assets/{clip_id}/{revision}/full/scene.json`、`preview.webm` |
| 区間0 | `model.rgs`、`metrics.json` |
| 区間i（i≥1） | `model-%05d.rgs`、`metrics-%05d.json`。例：`model-00001.rgs` |
| 既存held-out診断画像 | `heldout-{整数}-frame-{整数}.webp` |

各segmentは`independent=true`で、size・SHA256はその公開model bytesに一致させます。すべてのsegmentが同じN/FPSであることを実RGS1 decoderで確認し、可変frameCountや複数品質の公開はこのgateでは受理しません。previewのframe数は`segment_count*N`、動画のdurationは`segment_count*N/FPS`です。

各metricsには既存のdynamic identity・source/license・input hash・model/raw bytesとSHA256・local frame timesに加え、次を必須にします。欠落・nullや違う値を拒否します。

```json
{
  "segment_index": 1,
  "global_start_seconds": 0.08
}
```

この例はN=2、FPS=25の区間1です。`frame_times_seconds`は`[0, 0.04]`というlocal時刻で、`duration_seconds`も既存metricsのsample span `0.04`を維持します。transportのcoverage `0.08`とは区別します。各windowの`input_sha256`はそれぞれ承認した入力を指し、別の独立windowと同じhashであるとは要求しません。

## 公開予定snapshotを作る

既に独立した同N/FPSのmodel・対応metrics・共通cameraのscene・全区間のpreviewを用意します。checkpointの配列を切る、時刻を書き換える、同じmodelを並べて学習済み連続映像と扱うことは、このgateの実装には含みません。実際の区間境界の画質は画像で別に確認します。

明示的に承認した新revisionのmanifestへ、canonical URL・global区間・実size/SHAを記録します。catalog内のmanifestとstandalone manifestは同じ内容にし、catalogのpreview/scene URLも同じ固定HTTPS media origin・clip/revisionへ閉じます。source/license/変更内容/attribution/承認actor・basis・caveatは従来の台帳へ保持します。承認を自動生成せず、単なるhash計算を権利の確認とは扱いません。

台帳`files`はmanifest・scene・preview・各model/metricsを漏れなく列挙し、必要な既存held-out画像だけを追加します。余ったindexed modelやmetrics、未宣言file・symlink・学習状態は拒否します。従来の1revision最大64file、model wire/raw各32MiB、snapshot合計512MiB等の上限を維持するため、held-out画像なしでも最大30区間です。`peak_decoded_bytes`/`decode_time_ms`は端末の実測値へ別途更新する必要があり、このgateは値の正しさを計測しません。

```sh
go run ./cmd/rangle-publish \
  -catalog /private/candidate-catalog.json \
  -approvals /private/approvals.json \
  -root /private/public-assets \
  -origin https://media.example.com \
  -receipt /private/publication-receipt.json \
  -timeout 15m
```

コマンドは有限のローカル検証と非上書きreceiptだけを作り、upload・catalog昇格・cloud変更はしません。`-remote`を明示した場合だけ既存の固定originから各fileも取得します。公開や配備の承認は別に必要です。

## 確認範囲

productionの`Verify`へ2区間の合成RGS1と合成WebM containerを渡し、採否・承認proof・改ざん拒否を検証します。動画containerのテストは実video frameの復号成功を示しません。旧single-profile・共通fixtureの検証も継続します。

```sh
go test -race ./...
go vet ./...
```

実学習資産の境界連続性、実端末のworking set/復号時間、実回線の初回開始・再バッファ・bytes比較は未測定です。複数区間gateの存在だけで[#27](https://github.com/Hosi121/rangle/issues/27)の実端末・画質・転送量の受入完了とはしません。
