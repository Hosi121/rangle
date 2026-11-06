# rangle ABRと配信最適化の調査

2026年11月7日時点の一次資料を基に、一般的な動画配信の仕組みをrangleに適用する方針を整理する。目標は、通信と端末の制約の中で、短いハイライトを早く見始め、視点操作中も再生を続けられることである。

ABRは、品質別に用意した資産から次に取得するものを選ぶ制御である。圧縮、区間分割、キャッシュ、先読みと組み合わせて設計する。採用によって必ず転送や描画が速くなるとは置かず、通常の一括取得や固定品質とも比較する。

## 初期の選定

| 層 | 方針 | 現段階の扱い |
| --- | --- | --- |
| 通常動画のプレビュー | HLSまたはDASHと既存プレイヤーを使う | フォーマットとモバイル再生の選定対象 |
| 動的Gaussian資産 | 共通の時間軸を持つ品質別・区間別HTTPオブジェクト | 初期のマニフェスト契約をGoで実装 |
| 品質選択 | 帯域推定とバッファの余裕を使い、復号・メモリの制約も満たす | Goで決定規則の基準実装を作る |
| キャッシュ | 版付きURL、明示的なCache-Control、ETag、必要に応じてRange | ローカルHTTPサーバーで挙動を検証 |
| 配信先 | Cloud StorageとCloud CDN | 実際のGoogle Cloud構成・測定は次段階 |

Android Media3はHLSやDASHを扱うが、コンテナや内部の映像形式にも対応条件がある。したがって、Gaussianの任意のバイナリにプレイリストを付けるだけで、既存の動画プレイヤーが自由視点描画まで行うとは扱わない。プレビューでは既存の動画配信を利用し、Gaussian用の復号・描画を別に接続する。[Media3の対応形式](https://developer.android.com/media/media3/exoplayer/supported-formats)

## ABRの選択肢

dash.jsは、通信量に基づくThroughputRule、バッファに基づくBolaRule、バッファ不足・ダウンロード中断・描画落ちなどに対応する規則を提供している。現行の説明では、throughputとBOLAをバッファに応じて切り替える構成が示されている。[dash.jsのABR](https://dashif.org/dash.js/pages/usage/abr/)、[設定](https://dashif.org/dash.js/pages/usage/abr/settings.html)

| 方法 | 判断に使うもの | rangleでの使いどころ |
| --- | --- | --- |
| 固定品質 | 事前に決めた品質 | 比較基準として残す |
| Throughput型 | 過去の転送から推定した帯域 | コールドスタート後の小さな基準実装に向く |
| Buffer型 | バッファ量と品質の効用 | 長く連続再生する条件で比較する候補 |
| Hybrid型 | 帯域とバッファ、切り替え履歴 | 初期の選択規則の方針 |
| 描画制約を含む制御 | 上記に復号時間・メモリ・フレーム落ちを追加 | モバイルでの4DGS配信に必要な拡張 |

BOLAはバッファ量を用いる制御として定式化された研究である。バッファを参照した独自の簡単な規則を実装するだけではBOLAの実装とは呼ばない。初期実装はthroughputを中心にした基準とし、BOLAとの比較は別段階で行う。[BOLA論文](https://arxiv.org/abs/1601.06748)

## 帯域推定

GoogleのShaka Playerの公開実装は、異なる時定数のEWMAを使い、その小さい方を帯域推定として採用している。低下には早く反応し、上昇には慎重に追従する設計の参照になる。サンプルの転送時間とbytesの単位も公開実装で確認できる。[Shakaの推定器](https://github.com/shaka-project/shaka-player/blob/main/lib/abr/ewma_bandwidth_estimator.js)

rangleの初期実装も、この一般的な構成を参照する。推定に使うサンプルは、正常に完了したネットワーク取得に限定する。クライアント内のキャッシュ応答や中断された取得を同じように平均すると、後の取得に使える帯域を誤って見積もるためである。Shakaにも、キャッシュ応答を疑う短い取得時間を除外する処理がある。[ShakaのABR管理](https://github.com/shaka-project/shaka-player/blob/main/lib/abr/simple_abr_manager.js)

CDNのキャッシュヒットは、実際にクライアントまでネットワーク転送されるため、クライアント内のキャッシュヒットとは分ける。小さい区間では往復遅延、同時取得では接続間の帯域共有も影響する。初期の単純な推定器を、回線の物理帯域を正確に測るものとは見なさない。

dash.jsにはEWMAに加え、調和平均やbytesによる重み付き平均などの選択肢がある。推定器だけを変え、同じ入力系列と品質規則で比較できるようにする。[throughput計算](https://dashif.org/dash.js/pages/usage/abr/throughput-calculation.html)

## バッファと端末の制約

dash.jsのInsufficientBufferRuleは、推定throughputに余裕を持たせ、取得に使えるバッファを考慮する。rangleでも、広告された平均bitrateだけでなく、次区間の実サイズと初期化資産を取得できる時間を確認する。[InsufficientBufferRule](https://dashif.org/dash.js/pages/usage/abr/insufficient-buffer-rule.html)

設計上は次の所要時間を見積もる。通信の見積もりと復号の実測値がそろうまでは、判断の仮説として扱う。

```text
準備時間 = ネットワーク取得時間 + 初期化・復号時間 + GPUへの転送時間
取得量   = 次区間のbytes + まだ取得していない初期化資産のbytes
```

バッファは「ネットワーク取得済み」と「描画可能になるまで準備済み」を区別する。復号が詰まっている状態で、取得済みのbytesをそのまま再生可能な秒数と見なさない。

4DGS向けの品質選択では、圧縮後のサイズだけでなく、展開後の属性、復号中の作業領域、描画バッファも考慮する。初期Go実装ではメモリと復号時間をメタデータ上の上限として扱う。実機のGPU時間、フレーム落ち、熱状態を取り込む部分は次段階にする。dash.jsにもDroppedFramesRuleがあり、通信以外の描画条件を品質選択へ反映する設計の参照になる。[DroppedFramesRule](https://dashif.org/dash.js/pages/usage/abr/dropped-frames-rule.html)

## 品質ラダーと切り替え境界

品質ラダーは同じプレーを複数の通信量・端末負荷で表現する資産の組である。Gaussianの数、属性の量子化、外観の表現量などを候補とし、プレーを理解するのに必要な見え方との関係を評価する。品質とbitrateが単純に比例するとは仮定せず、初期の合成データではbitrate順のラダーを使って制御だけを確かめる。

HLSは、variantの帯域情報や、他区間の情報なしに復号可能な区間を示す仕組みを定義する。Appleのauthoring仕様は、品質間で区間境界をそろえることも推奨している。rangleの初期契約も時間軸と独立復号の条件を固定するが、JSONマニフェストをHLS準拠とは呼ばない。[RFC 8216](https://www.rfc-editor.org/rfc/rfc8216)、[Appleのauthoring仕様](https://developer.apple.com/streaming/hls-authoring-specification-for-apple-devices.html)

品質切り替えでは、初期化情報、区間開始状態、座標系、時刻、Gaussianの対応関係が必要になる。異なる品質の更新差分を自由に混ぜられるとは仮定しない。初期版は独立に開始できる区間で切り替える。細かく分割して増える基準情報とHTTP要求の費用も計測する。

## 短いフィードで優先する最適化

数秒のハイライトでは、長い動画を前提にした大きなバッファが溜まる前に視聴が終わる可能性がある。まず次の順で、開始時間と無駄な取得を評価する。

1. コールドスタート時に低い品質を選び、操作可能になるまでの取得量を減らす。
2. 現在のクリップを優先し、次のクリップの先読み量・同時取得数に上限を置く。
3. スワイプ時に不要になった取得と復号を中断し、資産を解放する。
4. 区間の取得が遅いとき、取得を続けるか低品質へ切り替えるかを判断する。
5. 帯域回復時は、十分なバッファと余裕ができてから品質を上げる。

4の中断は、既に転送した量と低品質の再取得量を含めて判断する。dash.jsのAbandonRequestRuleとSwitchHistoryRuleを参照する。初期Go実装の品質選択と、実HTTPクライアントの取得中断・先読みスケジューラーは別の部品として扱う。[中断規則](https://dashif.org/dash.js/pages/usage/abr/abandon-request-rule.html)、[切り替え履歴](https://dashif.org/dash.js/pages/usage/abr/switch-history-rule.html)

区間長には一律の最適値を置かない。0.5秒・1秒・2秒などの候補を、実際の学習単位と復号方式に合わせて比較する。短い区間は早期開始と切り替えを助ける一方、要求回数や基準情報を増やす。全体が十分小さいクリップでは、一括取得が有利な可能性も比較に含める。

視点に応じてデータを優先する方法は発展候補とする。360度動画のviewport制御にも研究があるが、自由に位置を変えるGaussian描画では、遮蔽や視点変更時の不足データを別に扱う必要がある。最初は視点によらず必要な最低品質を確保する構成を優先する。[BOLA360論文](https://arxiv.org/abs/2309.04023)

## CDNとHTTP

Cloud CDNはHTTPヘッダーでキャッシュを制御し、MIMEによる既定のキャッシュ対象も定めている。GaussianのバイナリやJSONでは、ファイル拡張子だけに頼らず、意図したCache-ControlとContent-Typeを設定する。[Cloud CDNのキャッシュ](https://docs.cloud.google.com/cdn/docs/caching)

配信資産には内容の版を含むURLと強いETagを付け、公開済みURLのbytesを上書きしない。今回のデモは版付きの固定マニフェストを返す。将来のフィードや最新版への参照には、資産と同じ長期不変キャッシュを無条件に付けない。

Rangeは大きいオブジェクトの一部取得に使えるが、区間ごとに小さなオブジェクトへ分ける方法と比較する。Cloud CDNのRangeキャッシュには、originの応答ヘッダーとキャッシュ充填の条件がある。ローカルで206が返ることと、CDNで期待したヒット率が出ることは別に確かめる。[Rangeの条件](https://docs.cloud.google.com/cdn/docs/caching#byte_range_requests)

JSONマニフェスト等にはCDNの動的圧縮も候補になる。ただしCloud CDNが圧縮した応答ではRangeが使われなくなり、強いETagも弱い形式へ変換される。バイナリのオフセットやチェックサムを使う資産は、保存時の符号化とHTTPのContent-Encodingを区別し、必要に応じてno-transformや配信先の設定で変換を抑える。[動的圧縮の条件](https://docs.cloud.google.com/cdn/docs/dynamic-compression)

HTTP/2・HTTP/3、接続再利用、要求の並行数も比較対象にする。Google Cloudの外部Application Load Balancerにはクライアント側のHTTP/3対応があるが、バックエンドまで同じプロトコルになるわけではない。初期のGoデモはローカルHTTPであり、この経路の優位を測定したものではない。[HTTP/3の対応範囲](https://docs.cloud.google.com/load-balancing/docs/https#http3_support)

計測項目はCMCDも参照する。dash.jsは、クライアント情報をHTTPヘッダーまたはクエリで要求へ添付する仕組みを提供する。将来の計測導入ではキャッシュキーへの影響を確認し、セッションごとの値で共有資産のキャッシュを分断しない設計にする。初期サーバーはクエリによる別名の資産要求を受け付けず、CMCDの収集はまだ行わない。[CMCDの説明](https://dashif.org/dash.js/pages/usage/cmcd.html)

## 比較計画

まず次の基準を同じデータ・品質条件で比較する。合成系列のテストは制御の挙動を確かめるもので、実際の4DGS映像の高速化実績とは分ける。

| 比較対象 | 確かめること |
| --- | --- |
| 全区間の一括取得と固定品質 | 開始時間、途中停止、途中離脱時の無駄なbytes |
| 区間取得と固定品質 | 分割そのものの効果と要求・初期化の費用 |
| Throughputを使うABR | 帯域の悪化・回復への反応 |
| 帯域とバッファを使うABR | 再生停止と品質変動の関係 |
| 復号・メモリ制約付きABR | 帯域は足りるが端末が処理できない場合の動作 |

指標は、プレビュー開始時間、自由視点での最初の描画までの時間、停止回数と時間、品質の推移と切り替え回数、取得済みで未使用のbytes、CPU/GPUメモリ、復号時間、フレーム時間とする。ネットワークの帯域・RTT・損失、クライアントとCDNのキャッシュ状態を記録する。

実際の画質ラダー、端末、実験予算が決まるまでは、画質を落とした結果を同品質の高速化として報告しない。区間境界での見た目、時間的なちらつき、ボールや手足の保持も別に確認する。

## 今回の実装範囲

共通の型と分担は [配信部品の初期契約](streaming-contract.md) に記す。GoのABR制御、合成系列のシミュレーター、合成区間を返すHTTPサーバーを実装し、ローカルのテストで接続を確認する。

実映像の品質別符号化、取得中の中断・先読みクライアント、BOLA、HLS/DASHのパッケージ化、Google Cloudへのデプロイ、モバイル復号は今回の完了範囲には含めず、必要な次の部品として残す。
