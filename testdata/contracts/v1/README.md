# Media boundary contracts v1

74個の合成fixtureを、各言語の実際のparser・loader・publication gateへ渡す。画像・学習済みmodel・外部URLへの通信は含まない。`cases.json`がcase ID、入力file、適用profile、context、採否、正規化値の正本。JSONの重複fieldを保持するため、入力を読み直してserializeしてはいけない。過大JSONは524289 byteの空白、過大wireは既存RGSへNULを追加する`pad-to-size`の小さなrecipeで表す。

## Shapeと意味制約

`schemas/*.schema.json`は型・必須field・任意field・null/unknownの構造を定義する。RGS1は`schemas/rgs1.layout.json`。これらのschemaを新しいtest-only validatorで実行して合格扱いにはしない。実production validatorを実行する。

構造とは別に、productionコードが次を検証する。

- cameraの非退化性、有限値、float32で安定なintrinsics。正負どちらのupも有効。
- LIVEの時刻順、sourceとconfigured origin、session/revision path、attribution、固定world/camera、modelのSHA・byte数。
- LIVEのdropped_windowsは非負のJSON safe integer（最大2^53-1）。Go int64だけで受理してJS側で丸めさせない（L13）。
- RGS1はcanonical row順を保存。count最大500000、wire/raw最大32MiB。Z1の展開長・checksum・余剰byteも検証する。
- wire FPSはfloat32。sceneのFPSとは`float32(scene.frameRate)`で一致し、sample終端は`(N-1)/FPS`。格納durationのfloat32丸めは既存の`1e-5`許容内。scene/bridgeのclockは元のscene FPSを使う別profileで、duration割合からsampleを推定しない。
- timelineは任意の`{previewUrl,previewStartSeconds,sceneStartSeconds}`。anchorsは有限・非負。scene自体の採否と、expected preview URLへのcontextual bindingは別。URLが不一致でもsceneは有効になり得るが、handoff mappingはnull。LIVE gateにexpected preview URLを捏造しない。

## Consumer matrix

| profile / cases | Go production | Python production | JavaScript production | Dart production |
| --- | --- | --- | --- | --- |
| rgs: R01–R38 | publication temporalModel、generation.validateTemporalOutput | packing.unpack_temporal/decode_temporal | decodeTemporal→prepareTemporal | N/A: binaryを読まない |
| scene: S01–S13 | ValidateLiveScene | LIVE bundle gate経由のみ。独立scene validatorなし | SceneSession.manifestFrom→parseManifest、model timeline binding | N/A: scene JSONを読まない |
| live: L01–L13 | live.ParseIndex / rangle-live-check | producer object→publish-live.verify→同じGo CLI | N/A: indexを読まない | LiveApi.pollのraw HTTP JSON |
| manifest: M01–M06 | catalog.Load→stream.Manifest.Validate | N/A | N/A | N/A: transport manifestを読まない |
| bundle: B01–B02 | 実rangle-live-checkのasset SHA/size/scene検証 | 実publish-live.verify→Go CLI | 実temporal-workerのfetch/SHA/decoder | N/A |
| clock: C01–C02 | N/A | N/A | PlaybackClock / handoffTimeline | ViewerTimeline |

PythonのLIVE入力は既に構築済みobject。raw構文だけのL05（escaped duplicate）とL12（byte上限）は`producer_object:false`でN/Aとする。Pythonが独立に同じJSON意味validatorを実装したとは主張しない。Go CIは実Go CLIをbuildし、Pythonのproduction publisher経由も実行する。

RGSの共通profileは学習出力window（2–32 frames）。汎用Go publication/JS viewerは15000 framesまで扱うが、Python trainer/generationの入力・出力制限を緩めない。このcorpusは未列挙の全JSON/全形式について完全な等価性を証明するものではない。

Z1は従来の[単一gzip member契約](../../../docs/rgs-gzip-contract.md)を実decoderでも強制する。R15–R38は、追加/反復member、NUL padding、非gzip余剰、CRC/ISIZE/FHCRC、optional header、予約flag、宣言長・wire上限を固定する。正しいoptional headerとdeflate内のmagic byte列は受理する。これは既知の実decoder差を直す追加coverageで、v1 corpusによる全RGS wireの完全等価性の証明ではない。独自Deflate parserは追加しない。

`python3 testdata/contracts/v1/generate_rgs_gzip.py`で新しい合成caseを再生成できる。既存R01を元にし、実画像・学習済みmodelは用いない。R36は32MiB+1のwireを各adapterで生成する。R30/R38は小さな圧縮入力で32MiB超の展開を表し、R38はISIZEも偽装して展開中の上限を通すことができないか確認する。

## 実行と変更

通常の必須CIに含まれる。

```sh
go test -race ./...
go vet ./...
PYTHONPATH=ml python3 -m unittest discover -s ml/tests -v
npm test --prefix apps/web/viewer
# apps/webで既存Flutter環境を使用
flutter test --no-pub
```

既存Python↔JS codec oracleも継続する。新しいprofile・wire互換性の変更はv2へ分け、v1の失敗caseを削って通さない。新しいcaseを加えるときは各consumerの採否と適用外理由を確認する。

今回検出したdrift: S04（Goのoptional timeline拒否）、M02（catalogのescaped duplicate）、M06（null numericを0として採用）、S13（sceneとwireのFPSが1ULP異なってもGoが採用）、R11–R13（generationのprimitive未検証）。これらは信頼境界の検証を維持・強化して揃える。
