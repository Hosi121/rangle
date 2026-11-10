# 自由視点への没入UI

通常動画を全面に表示し、再生/停止は映像の短いタップか左下、声援/共有は右、タイトル・競技・出典は下部へ置く。コメント/tagは「概要」から開く。検索・アカウント・共有リンクを維持する。

## 切り替えの契約

| 段階 | 動作 |
| --- | --- |
| 開始 | 中央を280ms長押し後、1200msで炎を充填。短tap・充填前release・24px超の移動では成立しない |
| 準備 | 長押し成立時の動画native時刻/再生意図を保存して静止し、動画を残してsceneを読む。「自由視点で見る」は充填を省く代替操作で、同buttonの長押しからも起動可能 |
| 入場 | 充填完了と実sceneの初回描画/seek完了を両方待ち、自動で入場。指releaseは条件にせず、timerで成功を偽装しない。時刻丸めはviewer adapterに従う |
| 演出 | 同時刻から開眼240ms・軌道線550ms。姿を2200msまで保持し、最後650msで演出だけfade（合計2850ms） |
| ズーム | 充填で動画を1→1.75倍へ加速拡大し、自由視点へ引き継ぐ。引く予備動作・ズーム復帰・自動カメラ移動はしない。直接共有URLでは適用しない |
| 復帰 | 演出なし。自由視点時刻へ動画をseek/再生意図復元後に表示。戻れる間は両者を保持し、card破棄で解放 |
| 移動 | 準備/自由視点中はfeed swipe・上下button/keyを無効化し、中央dragを視点へ渡す。取消/動画復帰で解除 |
| motion抑制 | 火の粉・揺れ・ズーム・開眼を省略し実準備完了で切替。振動は対応環境だけ |

充填は画面全体の火の粉・赤い光と元の炎形状の揺れを使う。PC wheel/trackpadは連続scroll1回で1本、380ms slide。240ms入力中断かつ移動完了後に次を受け付け、慣性で停止/連続jumpしない。指swipeは既存pagingを維持する。

動画/sceneの宣言済み時刻対応で変換し、restore完了後に切り替える。対応なしはscene0秒で停止し、動画時刻/意図を保つ。末尾超過は最終sampleへ合わせ調整を表示する。取消・retry・revision変更後の旧完了を反映しない。[時刻対応とiframe契約](../apps/web/README.md#scene-manifest)

## 画面の移動

feedはLIVE・検索・アカウントへ接続し、管理者はGS生成/LIVE管理へ移る（`/demo/?generate=1` も管理者専用）。中央の長押し説明は表示せず、長押しと代替buttonを維持する。

LIVE headerは高さ64px+上部安全領域とし、3D操作を覆わない。feed/直linkからは「フィードに戻る」、生成からは「生成に戻る」を準備/失敗時も同じ位置へ置く。LIVEは左下再生/停止と下部状態を使い、録画seekbarを出さない。生成側にはpreset・開始・状態・視聴・停止確認を残し、所有者の停止は映像準備中も表示する。出典dialogはiframe入力保護付き経路を使う。

## ロゴ

提供原本 `apps/web/assets/brand/rangle-editable.svg` の21pathを `brand_art.dart` のCanvasへ変換する。文字・炎・目・軌道線を保ち、gradientをFlutterで設定し、開眼/軌道線は別描画にする。

```bash
python apps/web/tool/generate_brand_art.py
dart format apps/web/lib/widgets/brand_art.dart
```

生成元変更後は `flutter analyze` を確認する。

## 声援と熱量バー

声援はいいねと別データで、ログイン後に連打できる。映像を48区間に分け、押した位置へ加算する。seekbarのorange濃度は区間の相対件数で、0件は着色しない。

受付直後にbutton付近へ火の粉10粒を出し、約850ms・上昇50px以内・最大4回重ねで消す。完了後は描画更新を止め、入力を遮らない。motion抑制では省略し、初回GET/他者増分では発火しない。

| API | データ |
| --- | --- |
| `GET /api/v1/clips/{id}/cheers` | 匿名可。`{ "bins": [48個の非負整数] }` |
| `POST /api/v1/clips/{id}/cheers` | [session/CSRF](accounts.md)・ユーザー頻度制限。`{ "request_id": "32桁の小文字hex", "bins": [48個の非負整数] }` |

POST合計は1〜64件。負数・余分field・異なる区間数を拒否する。約2秒でbatch送信し、失敗は同ID/内容で再送する。送信中tapは次batchへ分け、表示には未送信分を含め、失敗は詳細から再送可能。古いGETで送信済み件数を戻さない。

Firestoreは `clips/{id}/cheer_shards/{00..31}` を更新し、`cheer_receipts/{ユーザーIDとrequest_idのhash}` を同一transactionで作る。再送は加算せず、同ID/別内容は409。集計は最大32document。rawユーザーID・各tap時刻は公開しない。

receiptは7日後のexpires_atを持つが、**TTL未設定で自動削除されない**。保持/TTLは別途決定し、削除後の古いIDを永久重複排除しない。DB/IAM変更は含まず、既存communityへGo/Flutterを一緒に反映する。

他者件数はcard有効化/自分の送信応答で更新し、リアルタイム配信は未実装。通常破棄は可能な範囲で送信し、終了/logout/通信失敗後の破棄は未送信分を保証しない。logoutで別ユーザーへbatchを渡さない。大規模負荷・費用・分散不正連打は未検証。

## ローカル検証記録

### LIVEと生成画面の統合

2026-11-09、Flutter 3.47.9 / Dart 3.13.7。390×844・1440×900のFlutter描画/入力で充填・開眼・自由視点・即復帰を確認した。LIVE/生成の入力テストは320×640・390×844・844×390・1440×900で一覧・視聴・再生停止・出典・復帰・開始停止を確認した。320×640・844×390・2倍文字の既存テストはモーダル/操作領域を確認する。映像面はFakeViewer、PNGはローカル保持。

合成RGS1・一時job/userのlocal APIを本物のDart clientから呼び、preset・同ID再送・状態/LIVE取得・停止と、JS parserのmanifest/model SHA/primitiveを確認した。cloud GPU・実中継・撮影遅延の確認ではない。

Flutter 186件、Linux Go race/vet、viewer 50件、analyze、release buildは成功。Windowsの既存Go symlink権限とviewer3件のfile URL変換失敗はLinux containerで検証し、既存テストは変更していない。

browser自動画面取得はtimeoutで未完了。実HTML video/iframe重なり・GPU初描画・低速回線・touch体感/振動・履歴/iframe操作、Firestore実サービス/emulatorの声援transactionは未確認。合成テストをcloud運用/実機性能の成功とは扱わない。
