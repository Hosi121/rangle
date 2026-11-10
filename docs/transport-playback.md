# Transport manifestからの再生

`rangle-temporal-rgs1-segments-v1`と単一旧profile `rangle-temporal-rgs1`のAPI clipは、`manifest_url`を保持し、manifest→ABR選択→実segment取得/size・SHA照合→既存RGS1 Worker/描画器へ接続する。新segments codecでmanifestが欠ける場合は動画を維持し、先頭sceneだけを全clipとして再生しない。旧codecのmanifestなしscene、他codec、生成結果、LIVEは従来経路を使う。動画previewと、失敗時の動画復帰・再試行も維持する。

正本はGoの`internal/stream/manifest.go` v1と`internal/abr`。JS parserは元manifest URLとAPIのclip/revision/codecにidentityを結び付け、redirectを拒否する。transport URL/期待identityがある応答をscene形式へ降格せず、manifestはheaderとstreamの両方で1MiBへ制限する。path/size/hash/独立性/整列timelineを検証し、選択は同じdual EWMA、初期500kbps・安全率0.8、buffer/decode/working-setの制約、上昇headroom/連続確認を使う。これらは既存Goの初期値で、対象端末・回線の測定値ではない。`peak_decoded_bytes`等の広告を実測済みとは扱わない。hard-feasible品質がなければ描画せずエラーを通知し、既存の動画画面を保持する。

## 区間と時計

新profileでは全segmentのN/frameRate/camera/attributionを共通とし、各RGS1の時刻は0始まり。manifestのstartは`index*N/FPS`、coverageは`N/FPS`。描画はglobal frameから区間とlocal frameを求め、各区間の最後のsampleを次の境界まで保持する。initは既存scene v1で、`temporal.url=model.rgs`は先頭segmentと一致する。後続modelのURLはmanifestを正本とする。新binaryや追加量子化は導入しない。

旧profileは1segmentのみ、既存の`(N-1)/FPS` durationを検証する。表示時計のsample数・loop周期を新profileのcoverageへ読み替えない。previewとの時計は既存のpublisher binding/mappingがある場合だけ引き継ぎ、URL/titleの類似から推測しない。

## 取得・交換・取消

表示中meshは1つ、次のencoded segmentは最大1つ。品質は独立区間の取得前に選び、現在の区間を保持したまま次の候補を用意する。交換は既存TemporalSessionの別mesh準備・最初のdraw成功後のcommitを使い、canvas/camera/controlsを保つ。seekは任意区間から開始でき、古い候補は取消/epochで棄却する。離脱はfetch・Worker・候補・viewerを解放する。履歴全体のasset cacheは作らず、戻りseekは再取得する。

新transport経路のasset要求は`credentials: omit`・redirect拒否・`cache: no-store`で取得する。これはブラウザcache copyを帯域sampleへ混ぜないためのclient取得方針で、CDN/originの公開cache設定や旧scene経路を変更しない。CDN cache hitでも端末へpayloadが転送されるので、その転送時間は観測に含める。成功しsize/hashが合う実転送だけをsampleにし、取消/失敗・非有限/0以下の不明なtimingは採用しない。hash処理はnetwork計時から除く。宣言sizeは通信前に検査し、init/segment取得は各30秒のdeadlineを設ける。timeout/取消ではbody readerをcancelし、遅い応答を採用しない。

## 確認範囲

Node testはGo collapse/recovery vectorと同じ選択、初期/資源制約、parser拒否、実fetchからrenderer境界へのbytes引渡し、global seek、逆seek再取得、改ざん拒否と解放を確認する。Flutter testはAPI metadata保持と既存の動画・生成・LIVE操作を確認する。Nodeの描画境界fakeだけを実WebGL描画の成功とは扱わない。

実browserの320/1440表示は統合時に別途記録する。対象スマホのCPU+GPU全体予算、WAN QoE、複数品質の実素材評価、学習済み区間間の視覚的連続性、SLO/10万CCUは未検証。#27の実測budget、#29/#31/#35をこの接続だけで完了扱いにしない。
