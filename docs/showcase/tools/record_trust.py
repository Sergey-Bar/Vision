"""Record the /trust tamper run as a real screen recording.

The showcase video's rule is that its moving pictures are the product's own
surfaces: this script drives the hub's own tamper command in a real Chrome and
records the viewport while it runs - BEFORE PASS, DURING FAIL (row 85), AFTER
PASS - then the caller trims the interesting window.

Run:  py -3 docs/showcase/tools/record_trust.py
Writes: dist/showcase/media/video/raw/*.webm
"""

import glob
import os
import sys

from playwright.sync_api import sync_playwright

BASE = "http://127.0.0.1:8080"
OUT = os.path.join("dist", "showcase", "media", "video", "raw")


def main() -> int:
    os.makedirs(OUT, exist_ok=True)
    for stale in glob.glob(os.path.join(OUT, "*.webm")):
        os.remove(stale)

    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome", headless=True)
        context = browser.new_context(
            viewport={"width": 1728, "height": 720},
            record_video_dir=OUT,
            record_video_size={"width": 1728, "height": 720},
        )
        page = context.new_page()
        page.goto(BASE + "/trust", wait_until="load", timeout=30000)
        page.wait_for_timeout(2200)

        page.eval_on_selector("#btn-tamper", "el => el.scrollIntoView({block: 'center'})")
        page.wait_for_timeout(900)
        page.click("#btn-tamper")

        done = False
        for _ in range(160):
            try:
                text = page.inner_text("#run-result").upper()
            except Exception:
                text = ""
            if "AFTER" in text and "PASS" in text:
                done = True
                break
            page.wait_for_timeout(500)
        page.eval_on_selector("#run-result", "el => el.scrollIntoView({block: 'center'})")
        page.wait_for_timeout(3200)

        print("tamper reached AFTER PASS:", done)
        context.close()
        browser.close()

    videos = sorted(glob.glob(os.path.join(OUT, "*.webm")))
    for video in videos:
        print("recorded:", video, os.path.getsize(video), "bytes")
    if not videos:
        print("no recording produced", file=sys.stderr)
        return 1
    if not done:
        print("recording kept, but the run never reached AFTER PASS", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
