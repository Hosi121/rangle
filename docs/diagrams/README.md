# アーキテクチャ図の再生成

[全体](overview.html)・[ML](ml-pipeline.html)・[配信](delivery.html)・[監視](observability.html)の4図。HTMLが閲覧/PNG出力の正本で、アイコン・フォントを内包する。

2026-11-10の実環境とstg実装を反映。[構成の説明と確認範囲](../cloud-architecture.md#今回の照合範囲)を参照。

## PNGだけを生成する

repo rootで実行する。確認版はPython3.14.9、Playwright1.64.1、Chromium1248。依存は一時venvに置き、runtime/CIへ追加しない。uvの代わりに同版のvenv/pipも使える。

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

HTMLは1600×900 CSS pixels、PNGは3200×1800。light/darkの8枚を生成する。PNG、一時環境、取得したicon/fontはcommitせず、確認後に片付ける。アプリのGUIテストとは別の静的render。

## 内容・配置からHTMLを作り直す

[stack-diagram](https://github.com/caru-ini/stack-diagram)のcommit `5af3b095fff7109c905c5284dc510dc06588bade`を使用。`build-architecture-views.py`で座標/内容を変更し、toolのtemplate/geometryと`build.py`で埋め込む。HTMLを直接直した場合もrecipeへ反映する。テンプレートは、この4図で使うgroup/edgeのCSSだけを取り込む。

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

[icon-sources.json](icon-sources.json)に取得元/SHAを記録する。再取得時のSHA変更は外観・出典を確認する。既存HTMLからのPNG出力には再取得不要。

Noto Sans CJK Regularのcollection0（JP）を使用字形だけWOFF2化し、tool付属のGeist/JetBrains Monoと内包する。Googleは[公式icon](https://cloud.google.com/icons)、Goは[公式青ロゴ](https://go.dev/blog/go-brand)、抽象処理は[Lucide](https://lucide.dev/)。色・形・比率と[第三者notice](THIRD-PARTY-NOTICES.txt)を保持する。

## 出力の確認

- 全8PNGの文字・線・group padding・dark時ロゴを確認する。
- HTMLの未解決token、外部script/CSS/画像依存、私的環境情報がないことを確認する。公開galleryへ送信しない。
- light/darkを別pageで読み、font/画像decode後にcanvasだけを撮影する。render時はCairoSVG2.9.1で埋込iconをinline SVG化し、Chromium cacheによる欠けを防ぐ。ID衝突を避け、HTML正本の公式SVG・配置・文字・線は維持する。
