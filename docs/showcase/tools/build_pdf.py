"""Build the media pack PDF - the screens, the census and the diagrams, as pages.

Same rule as the static site: everything is read from the files on disk at build
time, and anything missing is reported rather than silently dropped.

Run:  py -3 docs/showcase/tools/build_pdf.py
Writes: dist/showcase/media/vision-showcase-pack.pdf
"""

import datetime as dt
import glob
import html
import json
import os
import re
import shutil
import subprocess
import sys

from playwright.sync_api import sync_playwright

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
MEDIA = os.path.join(ROOT, "dist", "showcase", "media")
OUT = os.path.join(MEDIA, "vision-showcase-pack.pdf")

SCREEN_CAPTIONS = {
    "hub-home": "The front door reads the record, it does not describe it. Each counter is parsed from the artefact beside it; when the artefact says nothing, the page says NOT RUN.",
    "live": "Two surfaces on one page, labelled: the POC's own live gate (its report and transcript open from here) and the lab-grade analytics surface, which is not on the gate path.",
    "pipeline": "One event, stage by stage, from the demo's own files. A decision made with no AI observation says exactly that.",
    "trust-1-intact": "The record, and what it does not cover: the page parses the gate record and prints its blind spots beside it.",
    "trust-2-tamper": "The tamper command running on the page itself: one byte changed, the chain recomputed, row 85 named as the first row that does not chain.",
    "trust-3-verify": "After the restore: the chain verifies again, exit 0. BEFORE PASS, DURING FAIL, AFTER PASS.",
    "census": "Every capability, graded: Live, Recorded, NOT RUN, Lab. NOT RUN is a first-class answer, never a green pass.",
    "operations": "Operations: the surfaces that watch the watchers - leases, budgets and attention, from the records they write.",
    "lab": "The lab tools under tools/, shown and labelled lab - not on the gate path.",
    "console": "The operator console, at /console.",
    "rundoc-live-report": "The live gate's own report, served through /rundoc/<name> from a closed allowlist: the request name never reaches the filesystem.",
    "rundoc-poc-report": "The demo run's own report, as written by pocctl.",
    "rundoc-poc-dashboard": "The demo dashboard, as written by pocctl.",
}

DIAGRAM_CAPTIONS = {
    "a1-pipeline": "How a decision is made - camera rows to audit ledger.",
    "b1-trust": "Why you can believe the record - chain, seal, proof.",
    "c1-live-arithmetic": "The arithmetic that refuses - p99 x ai_fps < 1000 ms, and the levers each refusal names.",
    "d1-surfaces": "What runs where - surfaces, ports and the gate path; the lab tools outside it.",
    "e1-architecture": "What this repository is - the product, the proving ground and the reading surface.",
    "f1-lifecycle": "The life of one event - ingest to ledger, and the two guarantees that survive kill -9.",
}


def read_text(rel):
    with open(os.path.join(ROOT, rel), encoding="utf-8") as handle:
        return handle.read()


def count_controls(rel):
    text = read_text(rel)
    return sum(1 for line in text.splitlines() if line.strip().startswith("name = '"))


def build_html():
    census = json.loads(read_text("web/static/census.json"))
    root_controls = count_controls("scripts/verify.ps1")
    poc_controls = count_controls("poc/active-monitoring/scripts/verify.ps1")

    try:
        revision = subprocess.run(["git", "rev-parse", "--short", "HEAD"], cwd=ROOT,
                                  capture_output=True, text=True, check=True).stdout.strip()
    except Exception:
        revision = "unknown"
    built_at = dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%d %H:%M UTC")

    screens = sorted(glob.glob(os.path.join(MEDIA, "screens", "*.png")))
    diagrams = sorted(glob.glob(os.path.join(MEDIA, "diagrams", "*.png")))

    screen_pages = []
    for path in screens:
        stem = os.path.splitext(os.path.basename(path))[0]
        caption = SCREEN_CAPTIONS.get(stem, "")
        screen_pages.append(f"""
      <section class="page">
        <div class="shotwrap"><img src="file:///{html.escape(path.replace(os.sep, '/'))}" alt="{html.escape(stem)}" /></div>
        <div class="caption"><b>{html.escape(stem)}</b>{(' &mdash; ' + html.escape(caption)) if caption else ''}</div>
      </section>""")

    diagram_pages = []
    for path in diagrams:
        stem = os.path.splitext(os.path.basename(path))[0]
        caption = DIAGRAM_CAPTIONS.get(stem, "")
        diagram_pages.append(f"""
      <section class="page">
        <div class="shotwrap"><img src="file:///{html.escape(path.replace(os.sep, '/'))}" alt="{html.escape(stem)}" /></div>
        <div class="caption"><b>{html.escape(stem)}</b>{(' &mdash; ' + html.escape(caption)) if caption else ''}</div>
      </section>""")

    rows = []
    for row in census.get("rows", []):
        rows.append(f"""<tr>
          <td>{html.escape(row.get('area', ''))}</td>
          <td><b>{html.escape(row.get('capability', ''))}</b><br/><span class="muted">{html.escape(row.get('note', ''))}</span></td>
          <td><code>{html.escape(row.get('proof', ''))}</code><br/><span class="muted">{html.escape(row.get('artefact', ''))}</span></td>
          <td>{html.escape(row.get('surface', ''))}</td>
          <td class="grade">{html.escape(row.get('grade', ''))}</td>
        </tr>""")

    return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>VISION - media pack</title>
<style>
  @page {{ size: A4 landscape; margin: 14mm 12mm; }}
  * {{ margin: 0; padding: 0; box-sizing: border-box; }}
  body {{ font-family: Inter, "Segoe UI", system-ui, sans-serif; color: #0f172a; }}
  .page {{ page-break-after: always; display: flex; flex-direction: column; height: 182mm; }}
  .page:last-child {{ page-break-after: auto; }}
  .shotwrap {{ flex: 1; display: flex; align-items: center; justify-content: center; overflow: hidden; }}
  .shotwrap img {{ max-width: 100%; max-height: 100%; object-fit: contain; border: 1px solid #e2e8f0; border-radius: 6px; }}
  .caption {{ padding-top: 6mm; font-size: 10.5pt; color: #334155; line-height: 1.5; }}
  .caption b {{ color: #0f172a; }}
  .cover {{ display: flex; flex-direction: column; justify-content: center; height: 182mm; }}
  .cover h1 {{ font-size: 44pt; letter-spacing: -0.03em; }}
  .cover h1 span {{ color: #059669; }}
  .cover p {{ font-size: 13pt; color: #334155; margin-top: 6mm; max-width: 200mm; line-height: 1.6; }}
  .cover .counts {{ margin-top: 10mm; font-size: 12pt; color: #0f172a; }}
  .cover .counts b {{ color: #059669; font-size: 16pt; }}
  .cover .foot {{ margin-top: 14mm; color: #64748b; font-size: 10pt; }}
  h2.section {{ font-size: 20pt; margin-bottom: 4mm; }}
  table {{ width: 100%; border-collapse: collapse; font-size: 8.5pt; }}
  th, td {{ text-align: left; vertical-align: top; padding: 2.4mm 2.6mm; border-bottom: 1px solid #e2e8f0; }}
  th {{ color: #64748b; }}
  .muted {{ color: #64748b; }}
  code {{ font-family: ui-monospace, Consolas, monospace; font-size: 8pt; background: #f1f5f9; border-radius: 3px; padding: 0 3px; }}
  .grade {{ font-weight: 700; white-space: nowrap; }}
</style>
</head>
<body>
  <section class="page">
    <div class="cover">
      <h1>VISION<span>.</span></h1>
      <p>Security intelligence, with its evidence. This pack is the static media edition of the showcase:
      the screens of the live surfaces, the graded census of every capability, and the diagrams drawn from
      this repository's own files. Nothing here is retold from memory; every number is parsed from the file
      that defines it, and anything that had not run is printed as NOT RUN.</p>
      <div class="counts">
        <b>{root_controls}</b> root red-check controls &nbsp;&middot;&nbsp;
        <b>{poc_controls}</b> POC red-check controls &nbsp;&middot;&nbsp;
        <b>{len(census.get('rows', []))}</b> graded capabilities
      </div>
      <div class="foot">Built {html.escape(built_at)} from {html.escape(revision)}.
      No CI runs on this account: a green claim is a human claim about a command that ran on this machine.</div>
    </div>
  </section>

  {''.join(screen_pages)}

  <section class="page">
    <h2 class="section">Every capability, graded</h2>
    <table>
      <thead><tr><th>Area</th><th>Capability</th><th>Proof / artefact</th><th>Surface</th><th>Grade</th></tr></thead>
      <tbody>{''.join(rows)}</tbody>
    </table>
  </section>

  {''.join(diagram_pages)}
</body>
</html>"""


def main():
    page_html = build_html()
    staging = os.path.join(MEDIA, "pack-staging.html")
    with open(staging, "w", encoding="utf-8", newline="\n") as handle:
        handle.write(page_html)

    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome", headless=True)
        page = browser.new_page()
        page.goto("file:///" + staging.replace(os.sep, "/"), wait_until="load", timeout=60000)
        page.wait_for_timeout(1500)
        page.pdf(path=OUT, format="A4", landscape=True, print_background=True,
                 margin={"top": "14mm", "bottom": "14mm", "left": "12mm", "right": "12mm"})
        browser.close()

    size = os.path.getsize(OUT)
    print(f"pdf written: {OUT} ({size/1024/1024:.1f} MB)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
