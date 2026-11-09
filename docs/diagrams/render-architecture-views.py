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
    # Rasterize embedded brand SVGs before Chromium sees them. Its SVG image
    # cache can produce clipped/empty icons even in a fresh dark-theme page.
    # The HTML source keeps the original SVGs; PNG export preserves their aspect.
    icons = {}

    def raster_icon(match):
        uri = match.group()
        if uri not in icons:
            data = base64.b64decode(uri.split(",", 1)[1])
            png = cairosvg.svg2png(bytestring=data, output_width=512)
            icons[uri] = "data:image/png;base64," + base64.b64encode(png).decode()
        return icons[uri]

    source = re.sub(r"data:image/svg\+xml;base64,[A-Za-z0-9+/=]+", raster_icon, source)
    # Export the canvas without scrolling past the surrounding page chrome.
    source = source.replace(
        "</style>",
        "body{padding:0}.head,.views,.foot,details{display:none}"
        ".scroller{border:0;border-radius:0}.canvas{border-radius:0}"
        ".g-label img{object-fit:contain}</style>",
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
