# Capture the showcase hub's pages as PNGs for the media pack.
# Part of the showcase media pipeline (docs/showcase/); not on the gate path.
# Run: py -3 docs/showcase/tools/capture_shots.py   (hub running on :8080)
from pathlib import Path
from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[3]
BASE = "http://127.0.0.1:8080"
OUT = ROOT / "dist" / "showcase" / "media" / "screens"
OUT.mkdir(parents=True, exist_ok=True)

PAGES = [
    ("hub-home", "/"),
    ("live", "/live"),
    ("pipeline", "/pipeline"),
    ("trust-1-intact", "/trust"),
    ("census", "/census"),
    ("operations", "/operations"),
    ("lab", "/lab"),
    ("console", "/console"),
    ("rundoc-live-report", "/rundoc/live-report"),
    ("rundoc-poc-report", "/rundoc/poc-report"),
    ("rundoc-poc-dashboard", "/rundoc/poc-dashboard"),
]


def settle(page, ms=1200):
    page.wait_for_timeout(ms)


def wait_result(page, timeout_ms=60000):
    page.wait_for_function(
        "() => { const el = document.querySelector('#run-result'); "
        "return el && el.innerText.trim().length > 60; }",
        timeout=timeout_ms,
    )
    settle(page, 400)


def main():
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome", headless=True)
        ctx = browser.new_context(viewport={"width": 1440, "height": 900}, device_scale_factor=1.5)
        page = ctx.new_page()
        # Console noise is fine; capture page errors so a broken page is named.
        errors = []
        page.on("pageerror", lambda e: errors.append(str(e)))

        for name, path in PAGES:
            page.goto(BASE + path, wait_until="load", timeout=30000)
            settle(page)
            page.screenshot(path=str(OUT / f"{name}.png"), full_page=True)
            print("shot", name)

        # The trust page acts on the ledger. Three states, in order.
        page.goto(BASE + "/trust", wait_until="load", timeout=30000)
        settle(page)
        page.click("#btn-tamper")
        wait_result(page)
        page.screenshot(path=str(OUT / "trust-2-tamper.png"), full_page=True)
        print("shot trust-2-tamper")
        page.click("#btn-verify")
        wait_result(page)
        page.screenshot(path=str(OUT / "trust-3-verify.png"), full_page=True)
        print("shot trust-3-verify")

        if errors:
            print("PAGE ERRORS:")
            for e in errors:
                print(" ", e)
        browser.close()


if __name__ == "__main__":
    main()
