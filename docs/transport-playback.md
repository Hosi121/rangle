# Transport manifestからの再生

`rangle-temporal-rgs1-segments-v1`と単一旧profile `rangle-temporal-rgs1`のAPI clipは、`manifest_url`を保持し、manifest→ABR選択→実segment取得/size・SHA照合→既存RGS1 Worker/描画器へ接続する。新segments codecでmanifestが欠ける場合は動画を維持し、先頭sceneだけを全clipとして再生しない。旧codecのmanifestなしscene、他codec、生成結果、LIVEは従来経路を使う。動画previewと、失敗時の動画復帰・再試行も維持する。

正本はGoの`internal/stream/manifest.go` v1と`internal/abr`。JS parserは元manifest URLとAPIのclip/revision/codecにidentityを結び付け、redirectを拒否する。transport URL/期待identityがある応答をscene形式へ降格せず、manifestはheaderとstreamの両方で1MiBへ制限する。path/size/hash/独立性/整列timelineを検証し、選択は同じdual EWMA、初期500kbps・安全率0.8、buffer/decode/working-setの制約、上昇headroom/連続確認を使う。これらは既存Goの初期値で、対象端末・回線の測定値ではない。`peak_decoded_bytes`等の広告を実測済みとは扱わない。hard-feasible品質がなければ描画せずエラーを通知し、既存の動画画面を保持する。

## 区間と時計

新profileでは全segmentのN/frameRate/camera/attributionを共通とし、各RGS1の時刻は0始まり。manifestのstartは`index*N/FPS`、coverageは`N/FPS`。描画はglobal frameから区間とlocal frameを求め、各区間の最後のsampleを次の境界まで保持する。initは既存scene v1で、`temporal.url=model.rgs`は先頭segmentと一致する。後続modelのURLはmanifestを正本とする。新binaryや追加量子化は導入しない。

旧profileは1segmentのみ、既存の`(N-1)/FPS` durationを検証する。表示時計のsample数・loop周期を新profileのcoverageへ読み替えない。previewとの時計は既存のpublisher binding/mappingがある場合だけ引き継ぎ、URL/titleの類似から推測しない。

## 取得・交換・取消

表示中meshは1つ、次のencoded segmentは最大1つ。品質は独立区間の取得前に選び、現在の区間を保持したまま次の候補を用意する。交換は既存TemporalSessionの別mesh準備・最初のdraw成功後のcommitを使い、canvas/camera/controlsを保つ。seekは任意区間から開始でき、古い候補は取消/epochで棄却する。離脱はfetch・Worker・候補・viewerを解放する。

公開VODの検証済みencoded segmentは、Cache Storageの`rangle-vod-segments-v2`でviewer iframeをまたいで再利用する。保存先は固定2枠・各16MiB以下、encoded payload合計32MiB以下。最大10分で失効し、古い枠から入れ替える。全clipの履歴やdecoded/GPUデータは保持しない。16MiBを超えるmodelは保存を省き、既存の32MiB上限・通常取得を使う。Web Locksで同一origin内の操作を直列化し、iframe破棄で古いwriteが後から完了しても固定枠のpayload上限を維持する。Cache API内部の一時copy・metadata・物理disk全体の使用量を32MiBと主張するものではない。

保存対象は`/assets/<clip>/<revision>/<quality>/*.rgs`の公開VODだけで、LIVE・生成結果・private previewは含まない。レスポンスが明示的に`public,max-age=...`で、`Age`または`Date`を参照できる場合だけ保存する。header受信時に残鮮度と10分の短い方で絶対期限を確定し、転送・hash・storage待ちで延長しない。CDNのmedia matcherは既存CORS公開metadataへ`Age`/`Date`を追加する。`private`/`no-store`/`no-cache`応答、失効・不明な鮮度は保存しない。再利用時もsource URL・size・SHAを照合し、破損entryは破棄して取得し直す。

Cache Storage/Web Locksが利用できない場合、容量不足・拒否・1秒のcache I/O期限に達した場合は通常取得へ戻す。Cache APIのwriteは取消できないため、待機終了後もwrite完了までlockを保持し、完了時に取消・失効した候補を破棄する。cacheが利用できること自体を再生成功の条件にしない。

cache miss時のasset要求は`credentials: omit`・redirect拒否・`cache: no-store`で実転送し、成功したsize/SHA一致の転送だけをABRへ観測する。明示した端末cache hitは帯域sampleへ混ぜない。CDN hitは端末へpayloadを転送するので、その時間を観測に含める。取消/失敗・非有限/0以下のtimingは採用せず、hash/cache lookup/cache保存はnetwork計時から除く。init/segment取得は各60秒のdeadline、timeout/取消ではbody readerをcancelし、遅い応答を採用しない。

## 確認範囲

Node testはGo collapse/recovery vectorと同じ選択、初期/資源制約、parser拒否、実fetchからrenderer境界へのbytes引渡し、global seek、改ざん拒否と解放を確認する。cacheは固定枠/byte上限、失効、quota/拒否時のfallback、改ざん時の再取得、遅いwriteと取消、Worker転送後の再利用、cache hitによるABR推定値の非更新を確認する。Flutter testはAPI metadata保持と既存の動画・生成・LIVE操作を確認する。Nodeの描画境界fakeだけを実WebGL描画の成功とは扱わない。

実browserの320/1440表示は統合時に別途記録する。対象スマホのCPU+GPU全体予算、WAN QoE、複数品質の実素材評価、学習済み区間間の視覚的連続性、SLO/10万CCUは未検証。#27の実測budget、#29/#31/#35をこの接続だけで完了扱いにしない。
