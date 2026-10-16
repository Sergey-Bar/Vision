from playwright.sync_api import sync_playwright
import pathlib
jobs = [
    ("docs/showcase/diagrams/c1-live-arithmetic.html", "dist/showcase/media/diagrams/c1-live-arithmetic.png"),
    ("docs/showcase/diagrams/d1-surfaces.html", "dist/showcase/media/diagrams/d1-surfaces.png"),
]
with sync_playwright() as p:
    b = p.chromium.launch(channel="chrome", headless=True)
    ctx = b.new_context(viewport={"width": 1920, "height": 1200}, device_scale_factor=1.5)
    pg = ctx.new_page()
    for rel, dest in jobs:
        url = pathlib.Path(rel).resolve().as_uri()
        pg.goto(url, wait_until="load", timeout=30000)
        pg.wait_for_timeout(700)
        pathlib.Path(dest).parent.mkdir(parents=True, exist_ok=True)
        pg.screenshot(path=dest, full_page=True)
        print("exported:", dest)
    b.close()
