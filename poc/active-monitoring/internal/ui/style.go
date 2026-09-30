package ui

// style is the whole design system, in one string, because there is no
// framework and no build step for the CSS and a separate file would be one more
// thing to keep in sync with the markup.
//
// The rules that shaped it:
//
//   - A MONOSPACE face for every number. Digits must not jitter as they change,
//     and a viewer must be able to tell a changed value from a re-render at a
//     glance. This is a monitoring surface, not a brochure.
//   - Verdict is carried by colour AND by the word itself. Colour alone excludes
//     the colour-blind reader, and on a surface whose whole job is to be read at
//     a glance under stress, that is not a cosmetic concern.
//   - Nothing blinks or animates. An operator watches this for hours. Motion in
//     a monitoring surface trains the eye to look away from it.
//   - `NOT RUN` gets its own colour, distinct from both pass and fail, because
//     "we did not measure it" is a different thing from "it failed" and reading
//     it as a failure is how a person ends up defending a number nobody measured.
const style = `
:root{
  --bg:#0a0d12; --bg2:#11161d; --panel:#141a23; --panel2:#1a212c;
  --line:#232c39; --line2:#2f3b4d;
  --fg:#e6edf6; --fg2:#9fb0c6; --fg3:#6b7c93;
  --pass:#3ddc84; --fail:#ff5f56; --notrun:#ffb02e; --live:#3aa0ff;
  --accent:#3aa0ff;
  --mono:ui-monospace,"SF Mono",SFMono-Regular,Menlo,Consolas,"Liberation Mono",monospace;
  --r:10px; --r2:14px;
}
*{box-sizing:border-box}
html,body{margin:0;padding:0}
body{
  background:var(--bg); color:var(--fg); font-family:var(--mono);
  font-size:14px; line-height:1.55; -webkit-font-smoothing:antialiased;
}
a{color:var(--accent)}
.wrap{max-width:1560px;margin:0 auto;padding:0 22px 64px}

/* ---- header: the truth line is the first thing on the page, not the last ---- */
header{
  position:sticky; top:0; z-index:20; margin:0 -22px 22px; padding:14px 22px 13px;
  background:linear-gradient(180deg,#0e131a 0%,#0a0d12 100%);
  border-bottom:1px solid var(--line);
}
.hrow{display:flex;align-items:center;gap:14px;flex-wrap:wrap}
.brand{font-weight:700;letter-spacing:.16em;font-size:11px;color:var(--fg3);text-transform:uppercase}
.sep{width:1px;height:16px;background:var(--line2)}
.truth{color:var(--fg2);font-size:12.5px}
.truth b{color:var(--fg)}
.truth .warn{
  color:var(--notrun);border:1px solid #4a3a14;background:#221a08;
  padding:1px 7px;border-radius:999px;font-weight:600;letter-spacing:.02em;
}
.livedot{width:8px;height:8px;border-radius:50%;background:var(--pass);box-shadow:0 0 0 3px #0d2e1b}
.livedot.stale{background:var(--notrun);box-shadow:0 0 0 3px #2c2410}
.livedot.dead{background:var(--fail);box-shadow:0 0 0 3px #2e1211}
nav{margin-left:auto;display:flex;gap:4px}
nav a{
  text-decoration:none;color:var(--fg3);font-size:11.5px;letter-spacing:.1em;
  text-transform:uppercase;padding:6px 12px;border:1px solid transparent;border-radius:8px;
}
nav a:hover{color:var(--fg);background:var(--panel)}
nav a.on{color:var(--fg);background:var(--panel2);border-color:var(--line2)}

/* ---- KPI row ---- */
.kpis{display:grid;grid-template-columns:repeat(auto-fit,minmax(168px,1fr));gap:12px;margin-bottom:20px}
.kpi{background:var(--panel);border:1px solid var(--line);border-radius:var(--r2);padding:14px 16px;position:relative;overflow:hidden}
.kpi::before{content:"";position:absolute;left:0;top:0;bottom:0;width:3px;background:var(--line2)}
.kpi.pass::before{background:var(--pass)} .kpi.fail::before{background:var(--fail)}
.kpi.notrun::before{background:var(--notrun)} .kpi.live::before{background:var(--live)}
.kpi .k{font-size:10.5px;letter-spacing:.13em;text-transform:uppercase;color:var(--fg3)}
.kpi .v{font-size:27px;font-weight:700;letter-spacing:-.02em;margin-top:5px;font-variant-numeric:tabular-nums}
.kpi .s{font-size:11.5px;color:var(--fg3);margin-top:3px}
.kpi.pass .v{color:var(--pass)} .kpi.fail .v{color:var(--fail)}
.kpi.notrun .v{color:var(--notrun)} .kpi.live .v{color:var(--live)}

/* ---- panels ---- */
.cols{display:grid;grid-template-columns:minmax(420px,1.15fr) minmax(380px,1fr);gap:20px;align-items:start}
@media(max-width:1180px){.cols{grid-template-columns:1fr}}
.panel{background:var(--panel);border:1px solid var(--line);border-radius:var(--r2);overflow:hidden;margin-bottom:20px}
.panel>h2{
  margin:0;padding:12px 16px;font-size:11px;letter-spacing:.14em;text-transform:uppercase;
  color:var(--fg3);border-bottom:1px solid var(--line);background:var(--bg2);
  display:flex;align-items:center;gap:10px;
}
.panel>h2 .tag{margin-left:auto;font-size:10.5px;letter-spacing:.08em;color:var(--fg3)}
.panel .body{padding:16px}

/* ---- the live view ---- */
.stage{background:#05070a;border-bottom:1px solid var(--line);position:relative;line-height:0}
.stage img{width:100%;display:block}
.stage .empty{padding:40px 20px;text-align:center;line-height:1.5}
.stagefoot{display:flex;gap:16px;flex-wrap:wrap;padding:10px 16px;font-size:11.5px;color:var(--fg3);background:var(--bg2)}
.stagefoot b{color:var(--fg2);font-weight:600}

/* ---- criteria tiles ---- */
.tiles{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:10px}
.tile{
  background:var(--panel2);border:1px solid var(--line);border-radius:var(--r);
  padding:12px 13px;border-left:3px solid var(--line2);
}
.tile.pass{border-left-color:var(--pass)} .tile.fail{border-left-color:var(--fail)}
.tile.notrun{border-left-color:var(--notrun)}
.tile .id{font-size:10.5px;letter-spacing:.12em;color:var(--fg3)}
.tile .nm{font-size:12px;color:var(--fg2);margin:3px 0 7px;min-height:2.2em}
.tile .vd{font-size:12px;font-weight:700;letter-spacing:.1em}
.tile.pass .vd{color:var(--pass)} .tile.fail .vd{color:var(--fail)} .tile.notrun .vd{color:var(--notrun)}
.tile .ob{font-size:11px;color:var(--fg3);margin-top:5px;word-break:break-word}

/* ---- tables ---- */
table{width:100%;border-collapse:collapse;font-size:12.5px}
th,td{text-align:left;padding:7px 9px;border-bottom:1px solid var(--line);vertical-align:top}
th{color:var(--fg3);font-size:10.5px;letter-spacing:.11em;text-transform:uppercase;font-weight:600}
tr:last-child td{border-bottom:none}
td.num{text-align:right;font-variant-numeric:tabular-nums}
tbody tr:hover{background:var(--panel2)}

/* ---- the "no data" line, which is the point ---- */
.nodata{
  margin:0;padding:16px;color:var(--notrun);font-size:12.5px;
  background:#1a1509;border:1px dashed #4a3a14;border-radius:var(--r);
}
.nodata b{color:var(--notrun)}
body .nodata p{margin:0 0 6px}
body .nodata p:last-child{margin:0;color:var(--fg3);font-size:11.5px}

/* ---- faults ---- */
.fault{border:1px solid var(--line);border-left:3px solid var(--fail);border-radius:var(--r);padding:10px 12px;margin-bottom:8px;background:#14171c}
.fault .h{display:flex;gap:9px;align-items:baseline;flex-wrap:wrap}
.fault .r{font-size:11.5px;color:var(--fail);font-weight:700;letter-spacing:.05em}
.fault .t{font-size:10.5px;color:var(--fg3)}
.fault .d{font-size:11.5px;color:var(--fg2);margin-top:4px;word-break:break-word}
.fault.ok{border-left-color:var(--pass);background:#0f1611}
.fault.ok .r{color:var(--pass)}

/* ---- evidence clips ---- */
.clips{display:grid;grid-template-columns:repeat(auto-fill,minmax(148px,1fr));gap:10px}
.clip{border:1px solid var(--line);border-radius:var(--r);overflow:hidden;background:#05070a}
.clip img{width:100%;display:block;aspect-ratio:16/9;object-fit:cover}
.clip .m{padding:6px 8px;font-size:10px;color:var(--fg3);background:var(--bg2);word-break:break-all}
.clip .m b{color:var(--pass);font-weight:600}

/* ---- footer ---- */
footer{
  margin-top:28px;padding-top:16px;border-top:1px solid var(--line);
  font-size:11.5px;color:var(--fg3);
}
footer b{color:var(--fg2)}
code{background:var(--panel2);border:1px solid var(--line);border-radius:5px;padding:1px 5px;font-size:11.5px}
`
