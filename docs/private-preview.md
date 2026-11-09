# 生成モデルの管理者向け試用

既存管理者で `/demo/experiments/index.html?scene=<id>` にログインし、抽出した各時刻の静止モデルを選んで視点を回す。連続再生・公開feed掲載は行わない。

modelは公開mediaでなくprivate catalog bucketの `private-scenes/<id>/` に置き、既存read-only mountからGoが読む。学習環境は接続しない。

```text
private-scenes/
  <id>/
    scene.json
    frame-000.splat
    frame-010.splat
```

scene v1の `frames` は相対名、`frameRate` は元fps。選択欄はファイル名の元frame番号から時刻を求め、抽出2枚を連続frameと扱わない。cameraは元校正から作り、正規化/軌跡の線形近似を加えない。

`-private-scene-root` は既定 `/mnt/catalog/private-scenes`、localで変更可、空文字で無効。directory不在でも起動し、取得は404。

`/api/v1/private-scenes/<id>/<file>` はGET/HEAD/Range/条件付き要求ごとに `AdminUser` で現在権限を確認する。匿名401・一般403、file/errorは `private, no-store` で公開CDNへ流さない。browserの準備modelは最大3枚、閉じると破棄する。小人数用で大規模性能の証拠ではない。

出典/利用条件/model確認後、model → 不変sceneの順に新規配置する。上書きせず更新は別ID。未確認素材を許諾台帳で承認済みにしない。

Webは32byte splat（位置/scale float32、RGB/opacity/quaternion8bit）で、学習floatと同一ではない。同じmodel/時刻/評価cameraで変換前後を比較し、差と未解決画質を記録する。公開/標準形式採用は別判断。

HTTPテストは認可/失効・path/サイズ・Range、単体は元時刻/cacheを確認する。実配備では匿名拒否・管理者取得・実bytes SHA一致を確認し、画面操作は別途確認する。
