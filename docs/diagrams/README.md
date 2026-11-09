# アーキテクチャ図の再生成

[全体](overview.html)・[ML処理](ml-pipeline.html)・[配信処理](delivery.html)・[ログ監視](observability.html)は、発表用presentation構成を分けた4図。各HTMLが閲覧・PNG出力の正本で、ネットワークなしで開ける。PNGは別の描画コードから作らず、同じHTMLを静的renderする。旧`cloud-architecture.svg/png/py`は2026-11-08の初期配備図として残す。

内容と配置を変更するときだけ`build-architecture-views.py`からHTMLを再生成する。この小さいrecipeは、指定した[stack-diagram](https://github.com/caru-ini/stack-diagram)のtemplate / geometryを基に座標を定義し、同repoの`build.py`でアイコン・フォントを埋め込む。HTMLだけを直接変更した場合は、recipeにも対応する変更を入れてから再生成する。生成後にlight/darkを目視する。

## PNGだけを生成する

HTML生成に使用したtool commitは **`5af3b095fff7109c905c5284dc510dc06588bade`**。PNG出力はrepoの`render-architecture-views.py`を使い、Python 3.14.9、Playwright 1.64.1、Chromium revision 1248で確認した。必要な依存は一時venvに置き、projectのruntimeやCI依存は増やさない。以下はrepo rootで実行する。`uv`がない環境では通常のvenvとpipでも同じversionを使用できる。

```bash
diagram_work=$(mktemp -d)
git clone https://github.com/caru-ini/stack-diagram.git "$diagram_work/stack-diagram"
git -C "$diagram_work/stack-diagram" checkout --detach 5af3b095fff7109c905c5284dc510dc06588bade
uv venv "$diagram_work/venv" --python 3.14
uv pip install --python "$diagram_work/venv/bin/python" 'playwright==1.64.1' 'fonttools[woff]==4.61.1' 'cairosvg==2.9.1'
"$diagram_work/venv/bin/python" -m playwright install chromium
mkdir -p .local/qa/architecture-views
for diagram in overview ml-pipeline delivery observability; do
  "$diagram_work/venv/bin/python" \
    docs/diagrams/render-architecture-views.py \
    "docs/diagrams/$diagram.html" ".local/qa/architecture-views/$diagram.png" --scale 2
done
```

1枚1600×900 CSS pixels、PNGは3200×1800。`*-dark.png`も作る。PNG・一時環境・loose icon・font binaryはcommitしない。今回は既存のChromium cacheを利用し、アプリの操作やGUIテストは行っていない。

## 内容・配置からHTMLを作り直す

上で取得したtoolを使い、`fetch_icons.py`で公式製品アイコンとLucideを取得する。パッケージ全体・個別アイコンは作業用directoryに留める。[icon-sources.json](icon-sources.json)は今回の取得元・SHAであり、アイコン本体ではない。再取得先の更新でSHAが変わったら、外観と出典を確認する。既存HTMLからのPNG生成は再取得の影響を受けない。

```bash
python3 "$diagram_work/stack-diagram/skills/stack-diagram/scripts/fetch_icons.py" \
  "$diagram_work/icons" \
  'gcp:google-cloud' 'run:gcp/cloud-run' 'lb:gcp/load-balancing' \
  'cdn:gcp/cloud-cdn' 'gcs:gcp/cloud-storage' 'firestore:gcp/firestore' \
  'vertex:gcp/vertex-ai' 'registry:gcp/artifact-registry' \
  'logging:gcp/cloud-logging' 'monitoring:gcp/cloud-monitoring' 'workflows:gcp/workflows' \
  flutter 'go=https://go.dev/images/go-logo-blue.svg' \
  python pytorch javascript cloudflare githubactions \
  'admin:lucide/user-cog' 'source:lucide/clapperboard' \
  'package:lucide/package-check' 'worker:lucide/cpu' 'render:lucide/box' \
  'index:lucide/list-tree' 'check:lucide/shield-check'
"$diagram_work/venv/bin/python" docs/diagrams/build-architecture-views.py \
  --tool-root "$diagram_work/stack-diagram" --icons "$diagram_work/icons" \
  --work-dir "$diagram_work/build" \
  --cjk-font /usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc
```

JPフォントはNoto Sans CJK Regularのcollection 0（`Noto Sans CJK JP`）を使う。使用字形だけをWOFF2へsubset化してHTMLに内包し、Geist・JetBrains Monoもtool付属版を埋め込む。別fontへ変える場合は字形とlicenseを確認する。[第三者notice](THIRD-PARTY-NOTICES.txt)はHTMLの折り畳み欄にも埋め込んでいる。Google製品アイコンは[公式package](https://cloud.google.com/icons)、Goは[公式ブランド素材](https://go.dev/blog/go-brand)の青版、Flutter/Python等はfetch scriptのbrand source、抽象処理は[Lucide](https://lucide.dev/)を使用。ロゴの色・形は変更しない。

最終確認では、全8PNGの文字・線・group padding・dark時のロゴを読む。HTMLに未解決token、外部script/CSS/画像依存、私的環境情報がないことも確認する。公開galleryへの送信は行わない。

PNG出力はlight/darkを別pageへ読み込み、フォントと全画像のdecode完了を待って撮影する。PNG専用のdocumentではページ見出し・nav・footer等を非表示にしてcanvas全体をviewport内へ収める。Chromiumのimage cacheによる欠けを避けるため、出力時だけ埋め込みアイコンをCairoSVG 2.9.1で正規化してinline SVG要素へ展開する。色・縦横比を維持し、clip path等のIDは要素ごとに分ける。HTMLの正本には公式SVGを保持し、canvasの配置・文字・線は共通である。閲覧用HTMLのページ要素は残す。
