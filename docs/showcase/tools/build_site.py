"""Build the static edition of the showcase - same content, no server.

Everything on the page is derived at build time from the files the live surface
reads as well: the census JSON, the newest verification record, the red-check
tables, the run reports the hub serves through /rundoc, the screenshots, the
diagrams and the rendered video. Nothing here is typed in by hand, and anything
missing is reported on the console rather than skipped silently.

Run:  py -3 docs/showcase/tools/build_site.py
Writes: dist/showcase/site/
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

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
OUT = os.path.join(ROOT, "dist", "showcase", "site")
MEDIA = os.path.join(ROOT, "dist", "showcase", "media")
WARNINGS = []


def warn(message):
    WARNINGS.append(message)
    print("WARNING:", message, file=sys.stderr)


def read_text(rel):
    with open(os.path.join(ROOT, rel), encoding="utf-8") as handle:
        return handle.read()


def count_controls(rel):
    """The same rule the hub uses: a control record starts its line with name = '."""
    text = read_text(rel)
    return sum(1 for line in text.splitlines() if line.strip().startswith("name = '"))


def latest_verification():
    records = sorted(glob.glob(os.path.join(ROOT, "dist", "verification", "verify-*.json")))
    if not records:
        warn("no dist/verification/verify-*.json record exists; the gate counters read NOT RUN")
        return None
    with open(records[-1], encoding="utf-8") as handle:
        record = json.load(handle)
    record["_file"] = os.path.basename(records[-1])
    return record


def git_revision():
    try:
        rev = subprocess.run(["git", "rev-parse", "--short", "HEAD"], cwd=ROOT,
                             capture_output=True, text=True, check=True).stdout.strip()
        branch = subprocess.run(["git", "rev-parse", "--abbrev-ref", "HEAD"], cwd=ROOT,
                                capture_output=True, text=True, check=True).stdout.strip()
        return f"{branch}@{rev}"
    except Exception as error:  # git may be absent; the page says so rather than guessing
        warn(f"git revision unavailable: {error}")
        return "unknown"


def copy_tree(patterns, dest, flat=True):
    os.makedirs(dest, exist_ok=True)
    copied = []
    for pattern in patterns:
        for path in sorted(glob.glob(os.path.join(ROOT, pattern))):
            name = os.path.basename(path) if flat else os.path.relpath(path, ROOT)
            target = os.path.join(dest, name)
            os.makedirs(os.path.dirname(target), exist_ok=True)
            shutil.copy2(path, target)
            copied.append(name)
    return copied


def grade_class(grade):
    return "g-" + re.sub(r"[^a-z]", "", grade.lower())


def build():
    if os.path.isdir(OUT):
        shutil.rmtree(OUT)
    os.makedirs(OUT)

    census = json.loads(read_text("web/static/census.json"))
    verification = latest_verification()
    root_controls = count_controls("scripts/verify.ps1")
    poc_controls = count_controls("poc/active-monitoring/scripts/verify.ps1")

    # Media ------------------------------------------------------------------
    final_video = os.path.join(MEDIA, "video", "vision-showcase.mp4")
    draft_video = os.path.join(MEDIA, "video", "draft2.mp4")
    video_source = final_video if os.path.exists(final_video) else draft_video
    if video_source == draft_video:
        warn("the final render (media/video/vision-showcase.mp4) does not exist yet; "
             "the page embeds the draft and the page says DRAFT")
    video_name = None
    if os.path.exists(video_source):
        os.makedirs(os.path.join(OUT, "media"), exist_ok=True)
        video_name = "vision-showcase.mp4"
        shutil.copy2(video_source, os.path.join(OUT, "media", video_name))
    else:
        warn("no rendered video exists under dist/showcase/media/video; the video section is NOT RUN")

    screens = copy_tree(["dist/showcase/media/screens/*.png"], os.path.join(OUT, "media", "screens"))
    if not screens:
        warn("no screenshots exist under dist/showcase/media/screens")

    diagram_pngs = copy_tree(["dist/showcase/media/diagrams/*.png"], os.path.join(OUT, "media", "diagrams"))
    if not diagram_pngs:
        warn("no diagram PNGs exist under dist/showcase/media/diagrams")

    diagram_html = copy_tree(["docs/showcase/diagrams/*.html"], os.path.join(OUT, "diagrams"))
    if not diagram_html:
        warn("no standalone diagram HTML exists under docs/showcase/diagrams")

    report_sources = {
        "live-report.html": "dist/live-demo/report.html",
        "live-transcript.txt": "dist/live-demo/transcript.txt",
        "poc-report.html": "dist/demo/report.html",
        "poc-dashboard.html": "dist/demo/dashboard.html",
    }
    reports = []
    for name, rel in report_sources.items():
        source = os.path.join(ROOT, rel)
        if os.path.exists(source):
            os.makedirs(os.path.join(OUT, "reports"), exist_ok=True)
            shutil.copy2(source, os.path.join(OUT, "reports", name))
            reports.append((name, rel))
        else:
            warn(f"the run report {rel} does not exist; the page lists it as NOT RUN")

    docs = copy_tree(
        ["docs/showcase/narration-judges.md", "docs/showcase/narration-customers.md",
         "docs/showcase/findings.md"],
        os.path.join(OUT, "docs"))

    # Page -------------------------------------------------------------------
    built_at = dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    revision = git_revision()

    if verification:
        gate_count = len(verification.get("gates", []))
        gate_outcome = verification.get("outcome", "unknown")
        gate_when = verification.get("finishedAtUtc", "unknown")
        gate_record = verification.get("_file", "unknown")
        gate_skipped = len(verification.get("skipped", []) or [])
        gates_line = (f"{gate_count} gates, outcome <b>{html.escape(str(gate_outcome))}</b>, "
                      f"{gate_skipped} skipped &mdash; {html.escape(str(gate_record))}, {html.escape(str(gate_when))}")
    else:
        gates_line = "NOT RUN &mdash; no verification record on this machine"

    rows = []
    for row in census.get("rows", []):
        rows.append(f"""
        <tr>
          <td class="area">{html.escape(row.get('area', ''))}</td>
          <td><b>{html.escape(row.get('capability', ''))}</b>
              <div class="note">{html.escape(row.get('note', ''))}</div></td>
          <td><code>{html.escape(row.get('proof', ''))}</code>
              <div class="note">{html.escape(row.get('artefact', ''))}</div></td>
          <td>{html.escape(row.get('surface', ''))}</td>
          <td><span class="grade {grade_class(row.get('grade', ''))}">{html.escape(row.get('grade', ''))}</span></td>
        </tr>""")

    screen_cards = "\n".join(
        f'<a class="shot" href="media/screens/{html.escape(name)}" target="_blank" rel="noopener">'
        f'<img src="media/screens/{html.escape(name)}" alt="{html.escape(name)}" loading="lazy" />'
        f'<span>{html.escape(name)}</span></a>'
        for name in screens)

    diagram_cards = []
    for png in diagram_pngs:
        stem = os.path.splitext(png)[0]
        html_link = f"diagrams/{stem}.html"
        link = f' &middot; <a href="{html_link}">interactive edition</a>' if f"{stem}.html" in diagram_html else ""
        diagram_cards.append(
            f'<a class="shot" href="media/diagrams/{html.escape(png)}" target="_blank" rel="noopener">'
            f'<img src="media/diagrams/{html.escape(png)}" alt="{html.escape(png)}" loading="lazy" />'
            f'<span>{html.escape(stem)}{link}</span></a>')
    if not diagram_cards:
        diagram_cards.append('<p class="muted">NOT RUN &mdash; no diagram was rendered on this machine at build time.</p>')

    report_items = "\n".join(
        f'<li><a href="reports/{html.escape(name)}">{html.escape(name)}</a> <span class="muted">'
        f'&mdash; copied from {html.escape(rel)}</span></li>' for name, rel in reports)
    if not reports:
        report_items = '<li class="muted">NOT RUN &mdash; no run report exists on this machine.</li>'

    doc_items = "\n".join(
        f'<li><a href="docs/{html.escape(name)}">{html.escape(name)}</a></li>' for name in docs)

    video_block = (
        f'<video controls preload="metadata" poster="media/screens/hub-home.png" '
        f'src="media/{video_name}"></video>'
        if video_name else
        '<p class="muted">NOT RUN &mdash; no render exists at build time. The composition is '
        'dist/showcase/vision-showcase/index.html; <code>npx hyperframes render</code> writes it.</p>')

    draft_note = "" if video_source == final_video else (
        '<p class="warnline">DRAFT render &mdash; this page was built before the final render; '
        'the counters and screenshots may have moved since.</p>')

    page = f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>VISION &mdash; showcase (static edition)</title>
<style>
  :root {{ color-scheme: dark; }}
  * {{ margin: 0; padding: 0; box-sizing: border-box; }}
  body {{ background: #07080b; color: #f4f4f5; font: 16px/1.6 Inter, "Segoe UI", system-ui, sans-serif; }}
  .wrap {{ max-width: 1280px; margin: 0 auto; padding: 48px 28px 96px; }}
  h1 {{ font-size: 56px; letter-spacing: -0.03em; }}
  h1 span {{ color: #3fb950; }}
  h2 {{ font-size: 30px; margin: 72px 0 8px; letter-spacing: -0.02em; }}
  .sub {{ color: #cfd2da; font-size: 20px; margin-top: 10px; max-width: 900px; }}
  .muted {{ color: #8b93a3; }}
  .warnline {{ color: #d29922; margin-top: 8px; }}
  .counters {{ display: flex; flex-wrap: wrap; gap: 12px; margin-top: 28px; }}
  .counter {{ border: 1px solid #2b2f3a; border-radius: 12px; padding: 14px 18px; background: #0b0d12; }}
  .counter b {{ color: #3fb950; font-size: 22px; }}
  .counter small {{ display: block; color: #8b93a3; }}
  video {{ width: 100%; border: 1px solid #262a33; border-radius: 14px; margin-top: 16px; background: #0b0d12; }}
  table {{ width: 100%; border-collapse: collapse; margin-top: 16px; font-size: 14px; }}
  th, td {{ text-align: left; vertical-align: top; padding: 10px 12px; border-bottom: 1px solid #1d212b; }}
  th {{ color: #8b93a3; font-weight: 600; position: sticky; top: 0; background: #0b0d12; }}
  td .note {{ color: #8b93a3; margin-top: 4px; max-width: 560px; }}
  td code {{ background: #12151d; border: 1px solid #232837; border-radius: 6px; padding: 1px 6px; font-size: 13px; }}
  .area {{ color: #8b93a3; white-space: nowrap; }}
  .grade {{ border-radius: 999px; padding: 3px 12px; font-size: 13px; font-weight: 600; white-space: nowrap; }}
  .g-live {{ background: #17351f; color: #4ade80; }}
  .g-recorded {{ background: #10233c; color: #58a6ff; }}
  .g-notrun {{ background: #2a2d34; color: #c9c9d1; }}
  .g-lab {{ background: #2a1d3d; color: #c792ea; }}
  .gallery {{ display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 16px; margin-top: 16px; }}
  .shot {{ display: block; border: 1px solid #262a33; border-radius: 12px; overflow: hidden; background: #0b0d12; text-decoration: none; color: #cfd2da; }}
  .shot img {{ width: 100%; display: block; aspect-ratio: 16 / 10; object-fit: cover; object-position: top; }}
  .shot span {{ display: block; padding: 8px 12px; font-size: 13px; }}
  ul {{ margin: 12px 0 0 20px; }}
  li {{ margin: 6px 0; }}
  a {{ color: #58a6ff; }}
  .verify {{ background: #0b0d12; border: 1px solid #262a33; border-radius: 12px; padding: 18px 22px; margin-top: 16px; }}
  .verify code {{ display: block; color: #7ee787; margin: 6px 0; }}
  footer {{ margin-top: 80px; border-top: 1px solid #1d212b; padding-top: 20px; color: #8b93a3; font-size: 14px; }}
</style>
</head>
<body>
<div class="wrap">
  <h1>VISION<span>.</span></h1>
  <p class="sub">Security intelligence, with its evidence. This is the static edition of the showcase:
  the same content as the live hub, without a server. Everything on this page was derived at build
  time from this repository's own files &mdash; the counters are parsed, not typed.</p>

  <div class="counters">
    <div class="counter"><b>{root_controls}</b><small>root red-check controls</small></div>
    <div class="counter"><b>{poc_controls}</b><small>POC red-check controls</small></div>
    <div class="counter"><b>{len(census.get('rows', []))}</b><small>graded capabilities</small></div>
  </div>
  <p class="muted" style="margin-top:10px">Root gate record: {gates_line}</p>
  {draft_note}

  <h2>The showcase video</h2>
  <p class="sub">Eighty seconds, English captions, cut from this machine's own screens and the module's
  own synthetic fixture. No relay footage appears in it: every relay row carries
  <code>cleared_for_external_use=false</code> and nothing of it is stored or republished.</p>
  {video_block}

  <h2>Every capability, graded</h2>
  <p class="sub">{html.escape(census.get('note', ''))} <span class="muted">(census schema {html.escape(str(census.get('schema', '?')))},
  generated {html.escape(str(census.get('generated', '?')))})</span></p>
  <table>
    <thead><tr><th>Area</th><th>Capability</th><th>Proof</th><th>Surface</th><th>Grade</th></tr></thead>
    <tbody>{''.join(rows)}</tbody>
  </table>

  <h2>Screens</h2>
  <p class="sub">Captured from the live hub on this machine. Click any screen for the full-size image.</p>
  <div class="gallery">{screen_cards}</div>

  <h2>Diagrams</h2>
  <p class="sub">Drawn from this repository's own files; NOT RUN, DEGRADED and RECORDED are part of the
  drawing, not decoration.</p>
  <div class="gallery">{''.join(diagram_cards)}</div>

  <h2>The runs' own reports</h2>
  <p class="sub">These are the artefacts the pages read their numbers from &mdash; the run reports as
  written, not retold.</p>
  <ul>{report_items}</ul>

  <h2>How this was shown</h2>
  <ul>{doc_items}
    <li class="muted">the two narration tracks (five minutes for judges, ten for customers) and the
    rehearsal's findings, including the defects this pass found and fixed</li>
  </ul>

  <h2>Verify it yourself</h2>
  <div class="verify">
    <p>No CI runs on this account: a green claim here is a human claim about a command that ran on
    this machine. These are the commands.</p>
    <code>.\\scripts\\run-verify.ps1&nbsp;&nbsp;# the root gate: build, lint, race, database, red-checks</code>
    <code>cd poc\\active-monitoring; .\\scripts\\verify.ps1&nbsp;&nbsp;# the POC module's own gate</code>
    <code>.\\scripts\\live-demo.ps1&nbsp;&nbsp;# the live path: resolve, preflight, watch, ledger</code>
    <code>.\\scripts\\showcase.ps1&nbsp;&nbsp;# the one entry point: hub, media, labelled fallbacks</code>
  </div>

  <footer>
    Built {html.escape(built_at)} from {html.escape(revision)} &mdash; every number above was read from a file this
    repository wrote. Where something had not run, this page says NOT RUN.
  </footer>
</div>
</body>
</html>
"""
    with open(os.path.join(OUT, "index.html"), "w", encoding="utf-8", newline="\n") as handle:
        handle.write(page)

    print(f"site written: {OUT}")
    print(f"  video: {video_name or 'NOT RUN'}")
    print(f"  screens: {len(screens)}, diagram PNGs: {len(diagram_pngs)}, diagram HTML: {len(diagram_html)}")
    print(f"  reports: {len(reports)}, docs: {len(docs)}")
    print(f"  counters: root controls {root_controls}, POC controls {poc_controls}, "
          f"census rows {len(census.get('rows', []))}")
    if WARNINGS:
        print(f"  warnings: {len(WARNINGS)} (see above)", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(build())
