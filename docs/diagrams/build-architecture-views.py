#!/usr/bin/env python3
"""Rebuild the three HTML diagrams using the pinned stack-diagram tool.

The HTML files are the portable source for PNG rendering. This small layout
recipe is only needed when changing their content; see README.md.
"""

import argparse
import base64
import html
import re
import subprocess
import sys
from pathlib import Path

PIN = "5af3b095fff7109c905c5284dc510dc06588bade"
WIDTH, HEIGHT = 1600, 900


def node(icon, name, role, x, y):
    return f"""<div class="node" style="left:{x - 110}px;top:{y}px">
      <div class="tile"><img src="%%ICON_{icon}%%" alt=""></div>
      <div class="name"><span>{name}</span></div>
      <div class="role"><span>{role}</span></div></div>"""


def group(name, x, y, w, h, icon=None, style="gb"):
    mark = f'<img class="plate" src="%%ICON_{icon}%%" alt="">' if icon else ""
    return f"""<div class="group {style}" style="left:{x}px;top:{y}px;width:{w}px;height:{h}px">
      <div class="g-label">{mark}{name}</div></div>"""


def edge(path, text="", x=0, y=0, kind="e", both=False):
    start = ' marker-start="url(#ar)"' if both else ""
    label = (
        f'<text class="el {"a" if kind == "e" else ""}" x="{x}" y="{y}" text-anchor="middle">{text}</text>'
        if text
        else ""
    )
    return f'<path class="{kind}" d="{path}"{start} marker-end="url(#ar)"/>{label}'


def overview():
    groups = [
        group("Google Cloud · 発表用 presentation", 350, 110, 1200, 565, "gcp", "gcp")
    ]
    nodes = [
        node("flutter", "Flutter Web", "ブラウザUI / JS viewer", 130, 190),
        node("cloudflare", "Cloudflare DNS", "DNS only", 130, 455),
        node("lb", "Cloud Load Balancing", "HTTPS / app・media", 490, 190),
        node(
            "run",
            "Cloud Run · Go",
            "Tokyo / UI・API",
            790,
            190,
        ),
        node(
            "firestore",
            "Firestore",
            "交流・共通実行台帳",
            1080,
            190,
        ),
        node(
            "gcs",
            "Cloud Storage",
            "private inputs / catalog",
            1360,
            190,
        ),
        node(
            "cdn",
            "Cloud CDN",
            "index 1秒 / 資産24時間",
            490,
            455,
        ),
        node(
            "gcs",
            "Cloud Storage",
            "private media",
            790,
            455,
        ),
        node(
            "vertex",
            "Vertex AI",
            "CustomJob / Spot T4 × 1",
            1080,
            455,
        ),
        node(
            "registry",
            "Artifact Registry",
            "digest固定image",
            1360,
            455,
        ),
        node(
            "githubactions",
            "GitHub Actions",
            "build・配備 / WIF",
            130,
            700,
        ),
    ]
    edges = [
        edge("M172 226 H448", "HTTPS", 305, 214, both=True),
        edge("M532 226 H748", "app", 640, 214, both=True),
        edge("M832 226 H1038", "transaction", 937, 214, kind="n", both=True),
        edge("M130 346 V449", "DNS", 160, 398, kind="n", both=True),
        edge("M490 346 V449", "media", 530, 400, both=True),
        edge("M532 491 H748", "cache fill", 638, 479, both=True),
        edge("M790 184 V158 H1360 V184", "catalog read-only", 1080, 149, kind="n"),
        edge(
            "M832 244 H945 V414 H1080 V449",
            "job開始 / 状態 / 中止",
            1055,
            402,
            kind="n",
        ),
        edge("M1360 346 V380 H1175 V430 H1080 V449", "input get", 1290, 370),
        edge("M1038 491 H832", "model / scene", 935, 479),
        edge("M1318 491 H1122", "image pull", 1220, 479, kind="n"),
        edge("M172 736 H1360 V611", "image push", 1010, 724, kind="n dash"),
        edge(
            "M172 716 H310 V380 H790 V346",
            "candidate → smoke → traffic",
            580,
            369,
            kind="n dash",
        ),
    ]
    return groups, nodes, edges


def ml_pipeline():
    groups = [
        group(
            "Vertex AI CustomJob · Tokyo · Spot T4 × 1",
            910,
            120,
            620,
            275,
            "vertex",
            "gcp",
        ),
        group("LIVE worker内の公開処理", 1240, 420, 290, 235, "go"),
    ]
    nodes = [
        node("admin", "管理者", "承認済みpreset", 130, 190),
        node(
            "run",
            "Cloud Run · Go API",
            "認証 / 生成受付",
            415,
            190,
        ),
        node(
            "firestore",
            "Firestore",
            "active 1件 / 共通台帳",
            700,
            190,
        ),
        node(
            "source",
            "校正・同期済み素材",
            "複数カメラ録画＋seed",
            130,
            490,
        ),
        node(
            "python",
            "Python 前処理",
            "NPZ / 校正動画bundle",
            415,
            490,
        ),
        node("gcs", "Cloud Storage", "private inputs", 700, 490),
        node(
            "pytorch",
            "offline · PyTorch",
            "gsplat / 4 vCPU",
            1040,
            225,
        ),
        node(
            "pytorch",
            "live · PyTorch",
            "逐次更新 / 16 vCPU",
            1380,
            225,
        ),
        node(
            "gcs",
            "Cloud Storage",
            "generated/ / scene-last",
            1040,
            490,
        ),
        node(
            "go",
            "検証・ADC publisher",
            "RGS1検証 / index CAS",
            1380,
            490,
        ),
        node(
            "go",
            "所有者の掲載操作",
            "Go検証 → feed掲載",
            1040,
            700,
        ),
        node("gcs", "Cloud Storage", "live/ / live-index/", 1380, 700),
    ]
    edges = [
        edge("M172 226 H373", "preset", 272, 214, kind="n"),
        edge("M457 226 H658", "予約・同期", 556, 214, kind="n", both=True),
        edge(
            "M415 184 V110 H875 V185 H1040 V219",
            "CustomJob",
            650,
            99,
            kind="n",
        ),
        edge("M1040 185 H1380 V219", "modeで分岐", 1210, 176, kind="n"),
        edge("M172 526 H373", "準備", 273, 514),
        edge("M457 526 H658", "SHA付き", 557, 514),
        edge("M738 526 H840 V261 H998", "NPZ", 917, 249),
        edge("M700 484 V425 H1180 V261 H1338", "bundle / 元PTS", 944, 414),
        edge("M1040 381 V484", "export", 1086, 439),
        edge("M1380 381 V410 H1210 V526 H1338", "local snapshot", 1320, 402),
        edge("M1040 646 V694", "掲載要求", 1092, 680, kind="n"),
        edge("M1380 646 V694", "公開順を検証", 1450, 680),
    ]
    return groups, nodes, edges


def delivery():
    groups = [
        group("同じprivate media bucket", 405, 145, 250, 475, "gcs", "gcp"),
        group("Cloud CDN / 2 policies", 725, 145, 260, 475, "cdn", "gcp"),
        group("ブラウザ · Flutter Web + JS iframe", 1090, 145, 495, 700, "flutter"),
    ]
    nodes = [
        node("run", "Cloud Run · Go API", "feed / manifest / LIVE", 155, 215),
        node(
            "gcs",
            "private catalog",
            "VOD / LIVE metadata",
            155,
            470,
        ),
        node(
            "index",
            "LIVE index",
            "live-index/{id}.json",
            530,
            215,
        ),
        node(
            "gcs",
            "不変scene / model",
            "live/{id}/{revision}/…",
            530,
            470,
        ),
        node(
            "cdn",
            "Cloud CDN · 1秒",
            "/live-index/*",
            855,
            215,
        ),
        node(
            "cdn",
            "Cloud CDN · 24時間",
            "不変URL / cache fill",
            855,
            470,
        ),
        node(
            "flutter",
            "Flutter Web",
            "一覧 / index poll",
            1220,
            215,
        ),
        node(
            "javascript",
            "JS iframe viewer",
            "scene / camera / 時刻",
            1220,
            470,
        ),
        node(
            "worker",
            "Web Worker",
            "RGS1復号 / ソート",
            1220,
            680,
        ),
        node("render", "WebGL", "GPU描画", 1450, 680),
        node(
            "check",
            "検証済みpublisher",
            "不変資産 / pointer CAS",
            155,
            705,
        ),
    ]
    edges = [
        edge(
            "M155 209 V110 H1220 V209",
            "feed / manifest / LIVE discovery",
            670,
            99,
            kind="n",
        ),
        edge("M155 464 V371", "metadata", 204, 414, kind="n"),
        edge("M572 251 H813", "小さいJSON", 692, 239, kind="n"),
        edge("M897 251 H1178", "poll", 1040, 239, kind="n"),
        edge("M572 506 H813", "immutable bytes", 690, 494),
        edge("M897 506 H1178", "scene.json", 1036, 494),
        edge("M1220 371 V464", "prepare / replace", 1320, 420, kind="n"),
        edge("M1220 626 V674", "load", 1253, 660, kind="n"),
        edge("M897 526 H1040 V716 H1178", "model.rgs", 1055, 705),
        edge("M1262 716 H1408", "buffer", 1335, 704),
        edge("M1262 526 H1450 V674", "camera / time", 1380, 548, kind="n"),
        edge("M197 741 H345 V251 H488", "② CAS", 393, 239, kind="n"),
        edge("M155 699 V651 H530 V626", "① model → scene", 455, 642),
    ]
    return groups, nodes, edges


EXTRA_CSS = """
@font-face {font-family:'Diagram JP';src:url(data:font/woff2;base64,CJK_FONT) format('woff2');font-weight:100 900;font-display:block}
:root {--font-display:'Geist','Diagram JP',sans-serif;--font-body:'Diagram JP',sans-serif;--font-mono:'JetBrains Mono','Diagram JP',monospace;--color-gcp:#4285f4;--color-white:#fff;--radius-card:8px;}
body {padding:24px;}.page {max-width:none;}.head {max-width:1600px;}.lede {max-width:600px;}
.canvas {width:1600px;height:900px;}svg.edges {width:1600px;height:900px;}
.node {width:220px;}.tile {width:72px;height:72px;}.tile img {width:43px;height:43px;}
.node .name {font-size:17px;line-height:23px;margin-top:9px;white-space:nowrap;}
.node .role {font-family:var(--font-body);font-size:14px;line-height:20px;letter-spacing:0;margin-top:3px;}
.g-label {font-size:16px;}.group.gcp {border-color:var(--color-gcp);}
.e {stroke-width:2.2;}.n {stroke-width:1.7;}.el {font-size:14px;letter-spacing:0;stroke-width:6px;}
.canvas-title {position:absolute;left:46px;top:25px;font-family:var(--font-display);font-size:30px;font-weight:700;}
.inside-legend {position:absolute;left:46px;bottom:14px;font-size:13px;color:var(--color-muted);}
.inside-legend b {color:var(--color-accent);}.foot {max-width:1600px;}
"""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tool-root", type=Path, required=True)
    parser.add_argument("--icons", type=Path, required=True)
    parser.add_argument("--work-dir", type=Path, required=True)
    parser.add_argument("--cjk-font", type=Path, required=True)
    args = parser.parse_args()
    actual = subprocess.check_output(
        ["git", "-C", str(args.tool_root), "rev-parse", "HEAD"], text=True
    ).strip()
    if actual != PIN:
        parser.error(f"stack-diagram must be pinned to {PIN}")
    skill = args.tool_root / "skills/stack-diagram"
    template = (skill / "assets/template.html").read_text()
    css = re.search(r"<style>(.*?)</style>", template, re.DOTALL).group(1)
    notices = html.escape(
        (Path(__file__).parent / "THIRD-PARTY-NOTICES.txt").read_text()
    )
    definitions = [
        ("overview", "全体構成", "視聴・生成・配備を分ける", overview),
        ("ml-pipeline", "ML処理", "管理者生成と到着画像によるLIVE更新", ml_pipeline),
        ("delivery", "配信処理", "メタデータ制御と不変モデルのデータ経路", delivery),
    ]
    drafts = []
    for slug, title, subtitle, factory in definitions:
        groups, nodes, edges = factory()
        ci_legend = "　 破線: CI/CD" if slug == "overview" else ""
        canvas = f"""<div class="canvas-title">rangle / {title}</div>
        {"".join(groups)}<svg class="edges" viewBox="0 0 {WIDTH} {HEIGHT}" aria-hidden="true"><defs>
        <marker id="ar" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path d="M0 0 L10 5 L0 10 z" fill="context-stroke"/></marker>
        </defs>{"".join(edges)}</svg>{"".join(nodes)}
        <div class="inside-legend"><b>青: データ</b>　 灰: 制御{ci_legend}</div>"""
        document = f'''<!DOCTYPE html><html lang="ja"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>rangle / {title}</title>
        <style>{css}{EXTRA_CSS}</style></head><body><main class="page"><header class="head"><div><div class="kicker">rangle · architecture views · 2026-11-10</div><h1>{title}</h1></div><p class="lede">発表用presentation構成の実装を図示。クラウドの現在状態を照会した図ではなく、repo内のIaC・code・検証記録に基づきます。</p></header>
        <div class="scroller"><div class="canvas" role="img" aria-label="{html.escape(title + "。" + subtitle)}">{canvas}</div></div>
        <footer class="foot"><span>rangle · 3図の入口と条件: <a href="../cloud-architecture.md">クラウド構成</a></span><span>icons: Google Cloud product icons / Go authors / svgl / devicon / Lucide · logos are their owners’ trademarks<br>fonts: Geist / JetBrains Mono / subset Noto Sans CJK JP (SIL OFL 1.1) · made with <a href="https://github.com/caru-ini/stack-diagram">stack-diagram</a></span></footer>
        <details><summary>Third-party notices</summary><pre style="white-space:pre-wrap;max-width:1600px">{notices}</pre></details></main></body></html>'''
        drafts.append((slug, document))
    args.work_dir.mkdir(parents=True, exist_ok=True)
    text = "".join(re.sub(r"<[^>]+>", "", doc) for _, doc in drafts)
    chars = args.work_dir / "glyphs.txt"
    chars.write_text(text)
    subset = args.work_dir / "DiagramJP.woff2"
    subprocess.run(
        [
            sys.executable,
            "-m",
            "fontTools.subset",
            str(args.cjk_font),
            "--font-number=0",
            f"--text-file={chars}",
            "--flavor=woff2",
            f"--output-file={subset}",
            "--layout-features=*",
            "--name-IDs=*",
            "--name-languages=*",
        ],
        check=True,
    )
    cjk = base64.b64encode(subset.read_bytes()).decode()
    for slug, document in drafts:
        draft = args.work_dir / f"{slug}.draft.html"
        draft.write_text(document.replace("CJK_FONT", cjk))
        subprocess.run(
            [
                sys.executable,
                str(skill / "scripts/build.py"),
                str(draft),
                str(args.icons),
                str(Path(__file__).parent / f"{slug}.html"),
                "--fonts",
                str(skill / "assets/fonts"),
            ],
            check=True,
        )


if __name__ == "__main__":
    main()
