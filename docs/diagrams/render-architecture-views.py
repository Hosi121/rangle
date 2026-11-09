#!/usr/bin/env python3
"""Export a self-contained architecture HTML as light/dark PNGs."""

import argparse
import base64
import re
from pathlib import Path

import cairosvg
from playwright.sync_api import sync_playwright


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--scale", type=float, default=2)
    args = parser.parse_args()
    source = args.source.read_text()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    # Inline vector paths to bypass Chromium's image cache. Cairo resolves each
    # SVG's own styles before embedding; unique IDs keep clip paths independent.
    # The portable HTML source keeps the original embedded image assets.
    icons = {}
    sequence = 0

    def inline_icon(match):
        nonlocal sequence
        uri = match.group("uri")
        if uri not in icons:
            data = base64.b64decode(uri.split(",", 1)[1])
            vector = cairosvg.svg2svg(bytestring=data).decode()
            icons[uri] = vector[vector.index("<svg") :]
        sequence += 1
        vector = icons[uri]
        prefix = f"export-icon-{sequence}-"
        for identifier in re.findall(r'\bid="([^"]+)"', vector):
            vector = vector.replace(
                f"url(#{identifier})", f"url(#{prefix}{identifier})"
            )
            vector = vector.replace(
                f'href="#{identifier}"', f'href="#{prefix}{identifier}"'
            )
        vector = re.sub(
            r'\bid="([^"]+)"', lambda item: f'id="{prefix}{item[1]}"', vector
        )
        plate = " plate" if 'class="plate"' in match.group() else ""
        return f'<span class="inline-icon{plate}" aria-hidden="true">{vector}</span>'

    source = re.sub(
        r'<img\b[^>]*src="(?P<uri>data:image/svg\+xml;base64,[A-Za-z0-9+/=]+)"[^>]*>',
        inline_icon,
        source,
    )
    # Export the canvas without scrolling past the surrounding page chrome.
    source = source.replace(
        "</style>",
        "body{padding:0}.head,.views,.foot,details{display:none}"
        ".scroller{border:0;border-radius:0}.canvas{border-radius:0}"
        ".inline-icon{display:block}.inline-icon svg{display:block;width:100%;height:100%}"
        ".tile .inline-icon{width:43px;height:43px}"
        ".g-label .inline-icon{width:14px;height:14px}"
        ".group.gcp>.g-label .inline-icon{width:20px;height:20px}"
        '[data-theme="dark"] .g-label .inline-icon.plate{background:#fff;border-radius:3px;'
        "padding:2px;width:18px;height:18px}</style>",
        1,
    )
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            args=[
                "--no-sandbox",
                "--disable-gpu",
                "--disable-dev-shm-usage",
                "--font-render-hinting=none",
            ]
        )
        try:
            for theme in ("light", "dark"):
                # A fresh page avoids stale SVG compositing after theme changes.
                page = browser.new_page(
                    viewport={"width": 1700, "height": 1000},
                    device_scale_factor=args.scale,
                )
                try:
                    document = source
                    if theme == "dark":
                        document = re.sub(
                            r"<html\b", '<html data-theme="dark"', document, count=1
                        )
                    page.set_content(document, wait_until="load")
                    page.evaluate(
                        """async () => {
                            await document.fonts.ready;
                            await Promise.all([...document.images].map(image => image.decode()));
                        }"""
                    )
                    output = args.output
                    if theme == "dark":
                        output = output.with_name(output.stem + "-dark" + output.suffix)
                    page.locator(".canvas").screenshot(
                        path=str(output), animations="disabled"
                    )
                    print(f"rendered {output}")
                finally:
                    page.close()
        finally:
            browser.close()


if __name__ == "__main__":
    main()
