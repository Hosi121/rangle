# 生成モデルの管理者向け試用

`/demo/experiments/index.html?scene=<id>` で、取り出した各時刻のモデルを選び、視点を回せる。既存の管理者アカウントでログインする。これは時刻別の静止モデルを確認する画面で、連続再生や公開フィードへの掲載は行わない。

モデルは公開media bucketではなく、既存のprivate catalog bucketの `private-scenes/<id>/` に置く。Cloud Runの既存read-only mountからGoが読む。学習環境をサービスに接続しない。

```text
private-scenes/
  <id>/
    scene.json
    frame-000.splat
    frame-010.splat
```

`scene.json` は既存のscene v1形式を使い、`frames` に相対ファイル名、`frameRate` に元映像のfpsを記録する。選択欄はファイル名の元フレーム番号から時刻を計算するため、抜き出した2枚を連続する2フレームとして扱わない。初期カメラは元の校正から作り、座標の正規化や軌跡の線形近似を加えない。

`-private-scene-root` は既定で `/mnt/catalog/private-scenes`。ローカルでは別ディレクトリを指定でき、空文字で無効にできる。ディレクトリ未配置でもサーバー起動を妨げず、取得要求を404にする。

`/api/v1/private-scenes/<id>/<file>` は、GET・HEAD・Range・条件付き要求ごとに既存の `AdminUser` で現在の権限を確認する。匿名は401、一般アカウントは403。ファイル取得とエラー応答は `private, no-store` とし、公開CDNへ流さない。ブラウザ内の準備済みモデルは最大3枚だけ保持し、ページを閉じると破棄する。この小人数の確認用経路を大規模配信の性能実証とは扱わない。

モデル・出典・利用条件を確認し、モデルを先に、不変のscene manifestを最後に配置する。既存objectを上書きせず、内容を更新するときは別idを使う。公開許諾台帳に未確認の素材を承認済みとして記入しない。

Web表示には既存の32byte `.splat` を使う。位置とscaleはfloat32だが、RGB・opacity・quaternionは8bitであり、学習中のfloat属性と同一ではない。試用前に同じモデル・時刻・評価カメラで変換前後を比較し、差と未解決の画質問題を記録する。公開や標準配信形式の採用判断とは分ける。

認可・権限失効・パスとファイルサイズ・RangeのHTTPテスト、元時刻とブラウザ内キャッシュの単体テストを実施する。実配備先では匿名拒否と管理者取得、実bytesのSHA256一致を確認する。画面操作の確認は利用者が行う。
