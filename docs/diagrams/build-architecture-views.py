#!/usr/bin/env python3
"""Rebuild the four HTML diagrams using the pinned stack-diagram tool.

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


def node(icon, name, role, x, y, badge=None):
    access = f'<div class="badge {badge[1]}">{badge[0]}</div>' if badge else ""
    return f"""<div class="node" style="left:{x - 110}px;top:{y}px">
      <div class="tile"><img src="%%ICON_{icon}%%" alt=""></div>
      <div class="name"><span>{name}</span></div>
      <div class="role"><span>{role}</span></div>{access}</div>"""


def group(name, x, y, w, h, icon=None, style="gb", boundary=None):
    mark = f'<img class="plate" src="%%ICON_{icon}%%" alt="">' if icon else ""
    detail = f'<div class="boundary">{boundary}</div>' if boundary else ""
    return f"""<div class="group {style}" style="left:{x}px;top:{y}px;width:{w}px;height:{h}px">
      <div class="g-label">{mark}{name}</div>{detail}</div>"""


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
        group("Google Cloud", 350, 110, 1200, 730, "gcp", "gcp"),
        group("運用・監視", 650, 635, 850, 190, "monitoring"),
    ]
    nodes = [
        node("flutter", "Flutter Web / JS", "視聴UI / 3D viewer", 130, 190),
        node("cloudflare", "Cloudflare DNS", "DNS only", 130, 455),
        node("lb", "Cloud Load Balancing", "公開VIP 34.96.101.8/32", 490, 190,
             ("公開HTTPS / Cloud Armor", "public")),
        node("run", "Cloud Run · Go API", "Tokyo / max2 / concurrency 8", 790, 190,
             ("LB ingress / app auth", "public")),
        node("firestore", "Firestore · 2 DB", "Community / Generation", 1080, 190,
             ("PITR / daily backup", "iam")),
        node("gcs", "Cloud Storage", "private inputs / catalog", 1360, 190,
             ("IAM / PAP", "iam")),
        node("cdn", "Cloud CDN", "Index: 1s / Media: 24h", 490, 455,
             ("公開HTTPS / edge policy", "public")),
        node("gcs", "Cloud Storage", "private media", 790, 455,
             ("IAM / CDN cache-fill", "iam")),
        node("vertex", "Vertex AI", "CustomJob / Spot T4×1", 1080, 455,
             ("IAM", "iam")),
        node("registry", "Artifact Registry", "Go API / Manager image", 1360, 455,
             ("IAM", "iam")),
        node("githubactions", "GitHub Actions", "Build / Deploy / WIF", 130, 700),
        node("run", "Generation Manager", "生成 / 進捗 / 公開 / 削除", 490, 680,
             ("IAM / max1 / concurrency 20", "iam")),
        node("logging", "Cloud Logging", "30 days / Progress view", 790, 680),
        node("monitoring", "Cloud Monitoring", "Metrics / logs", 1080, 680),
        node("workflows", "Scheduler / Workflows", "Guard: 15min / Sync: 2min", 1360, 680),
    ]
    edges = [
        edge("M172 226 H448", "HTTPS", 305, 214, both=True),
        edge("M532 226 H748", "app / gzip UI", 640, 214, both=True),
        edge("M832 226 H1038", "community DB", 937, 214, kind="n", both=True),
        edge("M130 346 V449", "DNS", 160, 398, kind="n", both=True),
        edge("M490 346 V449", "media", 530, 400, both=True),
        edge("M532 491 H748", "cache fill", 638, 479, both=True),
        edge("M815 184 V174 H1360 V184", "catalog read-only", 1120, 165, kind="n"),
        edge("M532 734 H590 V420 H1080 V449", "Job control", 1055, 408, kind="n"),
        edge("M1360 346 V380 H1175 V430 H1080 V449", "input get", 1290, 370),
        edge("M748 250 H640 V670 H550 V690 H532", "ID token / principal", 715, 402, kind="n"),
        edge("M532 716 H610 V360 H1080 V346", "生成DB", 1110, 376, kind="n", both=True),
        edge("M1038 491 H832", "model / scene", 935, 479),
        edge("M1318 491 H1122", "image pull", 1220, 479, kind="n"),
        edge("M172 736 H280 V854 H1520 V620 H1360 V611", "image push", 234, 844, kind="n dash"),
        edge("M172 716 H310 V380 H790 V346", "candidate / traffic", 580, 369, kind="n dash"),
        edge("M172 756 H340 V736 H448", "manager", 393, 724, kind="n dash"),
        edge("M448 250 H370 V622 H620 V716 H748", kind="n telemetry"),
        edge("M448 515 H370 V622 H620 V716 H748", "Application / worker / LB logs", 520, 650, kind="n telemetry"),
        edge("M748 264 H700 V622 H620 V716 H748", kind="n telemetry"),
        edge("M1080 611 V622 H620 V716 H748", kind="n telemetry"),
        edge("M1360 674 V622 H620 V716 H748", "Operation logs", 1280, 610, kind="n telemetry"),
        edge("M532 700 H620 V716 H748", kind="n telemetry"),
        edge("M832 716 H1038", "_AllLogs", 935, 704, kind="n telemetry"),
        edge("M832 264 H930 V655 H1080 V674", "Metrics", 1030, 644, kind="n telemetry"),
    ]
    return groups, nodes, edges


def ml_pipeline():
    groups = [
        group("Vertex AI CustomJob · Tokyo · Spot T4×1", 910, 120, 620, 275, "vertex", "gcp"),
        group("LIVE Publisher", 1240, 420, 290, 235, "go"),
    ]
    nodes = [
        node("admin", "運営 / 団体担当者", "session / 役割 / CSRF", 130, 190),
        node("run", "公開 Go API", "認証 / リクエスト転送", 415, 190),
        node("firestore", "Firestore · Generation", "Job / owner / preset", 700, 190),
        node("source", "校正・同期済み素材", "複数カメラ録画＋seed", 130, 490),
        node("python", "Python 前処理", "NPZ / Video bundle", 415, 490),
        node("run", "Generation Manager", "生成 / 進捗 / 公開 / 削除", 700, 345),
        node("gcs", "Cloud Storage", "private inputs", 700, 490),
        node("pytorch", "offline · PyTorch", "gsplat / 4 vCPU", 1040, 225),
        node("pytorch", "live · PyTorch", "逐次更新 / 16 vCPU", 1380, 225),
        node("gcs", "Cloud Storage", "generated/ / scene-last", 1040, 490),
        node("go", "Go Publisher", "RGS1検証 / Index CAS", 1380, 490),
        node("logging", "Training Progress", "Cloud Logging view", 415, 700),
        node("go", "公開・削除", "Generation Manager", 1040, 700),
        node("cdn", "Cloud CDN", "Cache invalidation", 700, 700),
        node("gcs", "Cloud Storage", "live/ / live-index/", 1380, 700),
    ]
    edges = [
        edge("M172 226 H373", "生成要求", 272, 214, kind="n"),
        edge("M457 246 H570 V381 H658", "ID token / principal", 545, 369, kind="n"),
        edge("M742 397 H820 V226 H742", "Jobs", 780, 385, kind="n", both=True),
        edge("M742 365 H875 V185 H1040 V219", "CustomJob", 960, 173, kind="n"),
        edge("M1040 185 H1380 V219", "Job mode", 1210, 176, kind="n"),
        edge("M172 526 H373", "準備", 273, 514),
        edge("M457 526 H658", "Upload", 557, 514),
        edge("M738 526 H840 V261 H998", "NPZ", 917, 249),
        edge("M742 546 H855 V425 H1180 V261 H1338", "Video bundle / PTS", 975, 414),
        edge("M1040 381 V484", "export", 1086, 439),
        edge("M1380 381 V410 H1210 V526 H1338", "local snapshot", 1320, 402),
        edge("M1040 646 V694", "Validate", 1097, 680),
        edge("M742 416 H875 V716 H998", kind="n"),
        edge("M998 736 H742", "Invalidate", 860, 754, kind="n"),
        edge("M1380 646 V694", "Upload / CAS", 1450, 680),
        edge("M457 736 H565 V381 H658", "Progress query", 540, 690, kind="n"),
        edge("M1040 381 H885 V666 H415 V694", "Training logs", 770, 654),
    ]
    return groups, nodes, edges


def delivery():
    groups = [
        group("Cloud Storage · Media", 405, 145, 250, 475, "gcs", "gcp"),
        group("Cloud CDN", 725, 145, 260, 475, "cdn", "gcp"),
        group("Browser", 1090, 145, 495, 700, "flutter"),
    ]
    nodes = [
        node("run", "Cloud Run · Go API", "UI gzip / feed / manifest URL", 155, 215),
        node("gcs", "private catalog", "Read-only FUSE / metadata", 155, 470),
        node("index", "LIVE index", "live-index/{id}.json", 530, 215),
        node("gcs", "Immutable Media", "assets / generated / live", 530, 470),
        node("cdn", "Cloud CDN · 1s", "/live-index/*", 855, 215),
        node("cdn", "Cloud CDN · 24h", "manifest / scene / video / model", 855, 470),
        node("flutter", "Flutter Web", "UI / Video / LIVE polling", 1220, 215),
        node("source", "HTML video", "動画再生 / Seek preview", 1450, 215),
        node("javascript", "JS iframe viewer", "manifest / ABR / 視点・時刻", 1220, 470),
        node("package", "Cache Storage", "2 slots × 16MiB / TTL 10min", 1450, 470,
             ("Public VOD / SHA-256", "iam")),
        node("worker", "Web Worker", "RGS1 decode / sort", 1220, 680),
        node("render", "WebGL", "Main thread / GPU rendering", 1450, 680),
        node("check", "Publisher", "Immutable upload / CAS", 155, 705),
    ]
    edges = [
        edge("M155 209 V110 H1220 V209", "UI gzip / feed / manifest URL", 670, 99, kind="n"),
        edge("M155 464 V371", "metadata", 204, 414, kind="n"),
        edge("M572 251 H813", "JSON", 692, 239, kind="n"),
        edge("M897 251 H1178", "poll", 1040, 239, kind="n"),
        edge("M1262 251 H1408", "再生UI", 1335, 239, kind="n"),
        edge("M572 506 H813", "immutable bytes", 690, 494),
        edge("M897 506 H1178", "VOD manifest / segment", 1037, 494),
        edge("M855 464 V405 H1570 V271 H1492", "preview video", 1270, 393),
        edge("M1220 371 V464", "bridge", 1260, 447, kind="n"),
        edge("M1262 506 H1408", "VOD cache", 1335, 494, both=True),
        edge("M1220 626 V674", "ArrayBuffer", 1265, 660),
        edge("M897 526 H1040 V716 H1178", "LIVE / scene", 1060, 705),
        edge("M1262 716 H1408", "buffer / indexes", 1335, 704),
        edge("M1262 526 H1335 V650 H1450 V674", "camera / time", 1410, 639, kind="n"),
        edge("M197 741 H345 V251 H488", "② CAS", 393, 239, kind="n"),
        edge("M155 699 V651 H530 V626", "① Media upload", 455, 642),
    ]
    return groups, nodes, edges


def observability():
    groups = [
        group("Log Sources", 45, 125, 300, 690, "gcp", "gcp"),
        group("Cloud Logging", 465, 285, 585, 530, "logging", "gcp"),
        group("Monitoring / Query", 1110, 125, 445, 690, "monitoring", "gcp"),
    ]
    nodes = [
        node("run", "Cloud Run · Go", "UI / API / Generation Manager", 170, 185),
        node("vertex", "Vertex AI · Python", "Trainer / LIVE worker", 170, 340),
        node("lb", "LB / Cloud CDN", "Media / app / candidate", 170, 495,
             ("App / candidate logs: 10%", "iam")),
        node("workflows", "Scheduler / Workflows", "Guard: 15min / Sync: 2min", 170, 650),
        node("logging", "Log Router", "rangle-stg-logs", 630, 365),
        node("logging", "Logging bucket", "rangle-stg-logs", 920, 365,
             ("Tokyo / 30 days", "iam")),
        node("logging", "generation-progress", "ml_job / workerpool0-0", 920, 650),
        node("monitoring", "Cloud Monitoring", "Metrics / logs", 1330, 200),
        node("logging", "Logs Explorer", "job_id / session_id", 1330, 415),
        node("run", "Generation Manager", "進捗集計 / Progress API", 1330, 630,
             ("IAM / viewAccessor", "iam")),
    ]
    edges = [
        edge("M212 221 H395 V401 H588", "Request logs", 302, 209),
        edge("M212 376 H395 V401 H588", "Worker logs", 302, 364),
        edge("M212 531 H395 V401 H588", "Request / cache logs", 302, 519),
        edge("M212 686 H395 V401 H588", "Execution logs", 302, 674),
        edge("M672 401 H878", "Log sink", 775, 389),
        edge("M962 401 H1080 V236 H1288", "view: _AllLogs", 1170, 224),
        edge("M962 421 H1100 V451 H1288", "_AllLogs", 1190, 439),
        edge("M920 525 V644", "Log view", 1000, 595),
        edge("M962 686 H1190 V666 H1288", "Training progress", 1125, 654),
        edge("M170 179 V169 H325 V99 H1330 V194", "Cloud Run metrics · Requests / p95", 800, 87, kind="n"),
    ]
    return groups, nodes, edges


EXTRA_CSS = """
@font-face {font-family:'Diagram JP';src:url(data:font/woff2;base64,CJK_FONT) format('woff2');font-weight:100 900;font-display:block}
:root {--font-display:'Geist','Diagram JP',sans-serif;--font-body:'Diagram JP',sans-serif;--font-mono:'JetBrains Mono','Diagram JP',monospace;--color-gcp:#4285f4;--color-white:#fff;--radius-card:8px;}
body {padding:24px;}.page {max-width:none;}.head {max-width:1600px;}.lede {max-width:600px;}
.canvas {width:1600px;height:900px;}.canvas-stamp {position:absolute;right:26px;top:28px;font-size:12px;color:var(--color-neutral);}svg.edges {width:1600px;height:900px;}
.node {width:220px;}.tile {width:72px;height:72px;}.tile img {width:43px;height:43px;}
/* Avoid SVG compositing artifacts in Chromium's dark PNG output. */
.tile,.tile img {color-scheme:light;}
.node .name span,.node .role span {backdrop-filter:none;-webkit-backdrop-filter:none;}
.node .name {font-size:17px;line-height:23px;margin-top:9px;white-space:nowrap;}
.node .role {font-family:var(--font-body);font-size:14px;line-height:20px;letter-spacing:0;margin-top:3px;}
.node .badge {display:inline-block;margin-top:4px;padding:0 6px;border:1px solid var(--color-neutral);border-radius:4px;font-size:12px;line-height:18px;color:var(--color-neutral);background:var(--color-paper);}
.node .badge.public {border-color:var(--color-accent);color:var(--color-accent);}
.boundary {position:absolute;right:18px;top:14px;font-size:15px;color:var(--color-neutral);white-space:nowrap;}
.g-label {font-size:16px;}.g-label img {object-fit:contain;}.group.gcp {border-color:var(--color-gcp);}
.e {stroke-width:2.2;}.n {stroke-width:1.7;}.el {font-size:14px;letter-spacing:0;stroke-width:6px;}
.telemetry {stroke-dasharray:1 5;stroke-linecap:round;}
.canvas-title {position:absolute;left:46px;top:25px;font-family:var(--font-display);font-size:30px;font-weight:700;}
.inside-legend {position:absolute;left:46px;bottom:14px;font-size:13px;color:var(--color-muted);}
.inside-legend b {color:var(--color-accent);}.foot {max-width:1600px;}
.diagram-note {position:absolute;font-size:14px;line-height:26px;color:var(--color-neutral);}
.views {display:flex;gap:20px;flex-wrap:wrap;margin-bottom:16px;font-size:14px;}
.views a {color:var(--color-neutral);text-underline-offset:3px;}
.views a[aria-current] {color:var(--color-accent);font-weight:700;}
"""


def diagram_css(template):
    """Retain the group and edge styles used by the four diagrams."""
    css = re.search(r"<style>(.*?)</style>", template, re.DOTALL).group(1)
    css = re.sub(r"/\*.*?\*/", "", css, flags=re.DOTALL)
    lines = []
    for line in css.splitlines():
        group_style = re.match(r"\s*\.group\.([\w-]+)", line)
        edge_style = re.match(r"\s*path\.([\w-]+)", line)
        if group_style and group_style[1] not in {"gcp", "gb"}:
            continue
        if edge_style and edge_style[1] not in {"e", "n", "telemetry"}:
            continue
        lines.append(line.rstrip())
    return "\n".join(lines)


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
    css = diagram_css(template)
    notices = html.escape(
        (Path(__file__).parent / "THIRD-PARTY-NOTICES.txt").read_text()
    )
    definitions = [
        ("overview", "全体構成", "視聴・生成・配信と運用監視", overview),
        ("ml-pipeline", "ML処理", "動画生成・公開とLIVE処理", ml_pipeline),
        ("delivery", "配信処理", "CDNとブラウザの配信・キャッシュ", delivery),
        (
            "observability",
            "ログ監視",
            "ログ収集・監視と生成進捗",
            observability,
        ),
    ]
    drafts = []
    for slug, title, subtitle, factory in definitions:
        groups, nodes, edges = factory()
        ci_legend = (
            "　 破線: CI/CD　 点線: ログ・メトリクス"
            if slug == "overview"
            else ""
        )
        legend = (
            "<b>青: ログ</b>　 灰: メトリクス"
            if slug == "observability"
            else f"<b>青: データ</b>　 灰: 制御{ci_legend}"
        )
        navigation = "".join(
            f'<a href="{other_slug}.html"'
            f"{' aria-current="page"' if other_slug == slug else ''}>{other_title}</a>"
            for other_slug, other_title, _, _ in definitions
        )
        canvas = f"""<div class="canvas-title">rangle / {title}</div>
        <div class="canvas-stamp">2026-11-10</div>
        {"".join(groups)}<svg class="edges" viewBox="0 0 {WIDTH} {HEIGHT}" aria-hidden="true"><defs>
        <marker id="ar" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path d="M0 0 L10 5 L0 10 z" fill="context-stroke"/></marker>
        </defs>{"".join(edges)}</svg>{"".join(nodes)}
        <div class="inside-legend">{legend}</div>"""
        document = f'''<!DOCTYPE html><html lang="ja"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>rangle / {title}</title>
        <style>{css}{EXTRA_CSS}</style></head><body><main class="page"><header class="head"><div><div class="kicker">rangle · architecture views · 2026-11-10</div><h1>{title}</h1></div><p class="lede">2026-11-10のクラウド構成。{subtitle}。</p></header>
        <nav class="views" aria-label="アーキテクチャ図">{navigation}</nav>
        <div class="scroller"><div class="canvas" role="img" aria-label="{html.escape(title + "。" + subtitle)}">{canvas}</div></div>
        <footer class="foot"><span>rangle · 構成の説明: <a href="../cloud-architecture.md">クラウド構成</a></span><span>icons: Google Cloud product icons / Go authors / svgl / devicon / Lucide · logos are their owners’ trademarks<br>fonts: Geist / JetBrains Mono / subset Noto Sans CJK JP (SIL OFL 1.1) · made with <a href="https://github.com/caru-ini/stack-diagram">stack-diagram</a></span></footer>
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
