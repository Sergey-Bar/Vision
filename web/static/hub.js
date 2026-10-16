// The hub's home. Every value on the page comes from /api/showcase/status,
// which reads files this machine already wrote - so "not run yet" renders as
// "not run yet" rather than as a zero, and a stale record shows its own date.
(function () {
    "use strict";

    function el(tag, cls, text) {
        var node = document.createElement(tag);
        if (cls) { node.className = cls; }
        if (text !== undefined && text !== null) { node.textContent = String(text); }
        return node;
    }

    function fmtTime(iso) {
        if (!iso) { return "unknown time"; }
        var d = new Date(iso);
        return isNaN(d.getTime()) ? iso : d.toLocaleString();
    }

    function kb(n) {
        if (!n) { return "0 KB"; }
        if (n >= 1048576) { return (n / 1048576).toFixed(1) + " MB"; }
        return Math.max(1, Math.round(n / 1024)) + " KB";
    }

    function pill(state, label, value) {
        var p = el("span", "pill" + (state ? " " + state : ""));
        p.appendChild(el("span", "dot"));
        p.appendChild(el("b", null, label));
        p.appendChild(document.createTextNode(value ? " " + value : ""));
        return p;
    }

    function kpi(value, caption, source) {
        var box = el("div", "kpi");
        box.appendChild(el("div", "v", value));
        box.appendChild(el("div", "k", caption));
        if (source) { box.appendChild(el("div", "src", source)); }
        return box;
    }

    function renderPills(st) {
        var host = document.getElementById("pills");
        host.textContent = "";
        if (st.gate) {
            var state = st.gate.outcome === "passed" ? "ok" : "bad";
            host.appendChild(pill(state, "gate:",
                st.gate.outcome + " — " + st.gate.gates + " gates, " + st.gate.skipped + " skipped, " + st.gate.failed + " failed"));
            host.appendChild(pill(st.gate.dirty ? "warn" : "ok", "commit:", st.gate.commitShort));
            host.appendChild(pill(st.gate.dbUrlSet ? "ok" : "bad", "database:", st.gate.dbUrlSet ? "live" : "not set"));
            host.appendChild(pill(st.gate.race ? "ok" : "warn", "race:", st.gate.race ? "on" : "off"));
        } else {
            host.appendChild(pill("", "gate:", "no verification record on this machine yet"));
        }
        if (st.redCheck.pocTotal > 0) {
            host.appendChild(pill(
                st.redCheck.pocSkipped === 0 && st.redCheck.pocProved === st.redCheck.pocTotal ? "ok" : "warn",
                "red-check:", st.redCheck.pocProved + "/" + st.redCheck.pocTotal + " POC controls proved"));
        }
        if (st.dataRoot) {
            host.appendChild(pill("ok", "demo data root:", st.dataRoot.split("/").pop()));
        } else {
            host.appendChild(pill("", "demo data root:", "none yet — scripts/live-demo.ps1 writes one"));
        }
    }

    function renderKpis(st) {
        var host = document.getElementById("kpis");
        host.textContent = "";
        if (st.redCheck.pocTotal > 0) {
            host.appendChild(kpi(
                st.redCheck.pocProved + " / " + st.redCheck.pocTotal,
                "POC controls proved by removal",
                st.redCheck.pocFile + " · " + (st.redCheck.pocAt || "date not in the file")));
        } else {
            host.appendChild(kpi("not run", "POC red-check", "the transcript file is not on this machine"));
        }
        if (st.gate) {
            host.appendChild(kpi(
                String(st.gate.gates),
                "root gates in the last record",
                st.gate.record + " · " + fmtTime(st.gate.finishedAt)));
        } else {
            host.appendChild(kpi("not run", "root gate", "run scripts/verify.ps1 to write a record"));
        }
        host.appendChild(kpi(
            st.redCheck.rootCount > 0 ? String(st.redCheck.rootCount) : "not read",
            "controls in the root red-check table",
            "counted from " + (st.redCheck.rootFile || "scripts/verify.ps1")));
        host.appendChild(kpi(
            st.artifacts.length > 0 ? String(st.artifacts.length) : "none",
            "demo artefacts on disk",
            "dist/live-demo · dist/demo"));
    }

    // The files whose bytes a reader can open right here. The keys are the same
    // closed set the server serves at /rundoc/ - a path that is not one of them
    // stays plain text, so this table never links to a document that is not
    // there.
    var RUNDOC = {
        "dist/demo/report.html": "poc-report",
        "dist/demo/dashboard.html": "poc-dashboard",
        "dist/live-demo/report.html": "live-report",
        "dist/live-demo/transcript.txt": "live-transcript"
    };

    function renderArtifacts(st) {
        var host = document.getElementById("artifacts");
        host.textContent = "";
        if (!st.artifacts || st.artifacts.length === 0) {
            host.appendChild(el("p", "sub", "No demo artefacts exist on this machine yet. scripts/demo.ps1 and scripts/live-demo.ps1 write them."));
            return;
        }
        var tbl = el("table", "tbl");
        var head = el("tr", null);
        ["file", "written", "size"].forEach(function (h) { head.appendChild(el("th", null, h)); });
        tbl.appendChild(head);
        st.artifacts.forEach(function (a) {
            var tr = el("tr", null);
            var tdName = el("td", null);
            var code = el("code", null, a.path);
            var doc = RUNDOC[a.path];
            if (doc) {
                var link = el("a", null);
                link.href = "/rundoc/" + doc;
                link.appendChild(code);
                link.appendChild(el("span", "go", "  open \u2192"));
                tdName.appendChild(link);
            } else {
                tdName.appendChild(code);
            }
            tdName.appendChild(el("div", "why", a.name));
            tr.appendChild(tdName);
            tr.appendChild(el("td", "num", fmtTime(a.at)));
            tr.appendChild(el("td", "num", kb(a.size)));
            tbl.appendChild(tr);
        });
        host.appendChild(tbl);
    }

    function renderGate(st) {
        var host = document.getElementById("gatebox");
        host.textContent = "";
        if (!st.gate) {
            host.appendChild(el("p", "sub", "No verification record exists on this machine. The gate writes one at the end of every run."));
            return;
        }
        var g = st.gate;
        var line = el("p");
        line.appendChild(el("b", null, g.outcome === "passed" ? "All gates passed. " : "The last run did not pass. "));
        line.appendChild(document.createTextNode(g.exitReason || ""));
        host.appendChild(line);
        host.appendChild(el("p", "sub", g.record + " · finished " + fmtTime(g.finishedAt)));

        var kv = el("dl", "kv");
        [
            ["commit", g.commitShort + (g.dirty ? " (worktree dirty at run time)" : "")],
            ["mode", "race " + (g.race ? "on" : "off") + " · cgo " + (g.cgo ? "on" : "off")],
            ["database", g.dbUrlSet ? "SP_TEST_DATABASE_URL set — the database suites ran" : "not set — those suites skipped"],
            ["red-check", "the gate ran its control table and every control was watched failing"],
            ["ci", g.ci || "none"]
        ].forEach(function (pair) {
            kv.appendChild(el("dt", null, pair[0]));
            kv.appendChild(el("dd", null, pair[1]));
        });
        host.appendChild(kv);

        if (st.redCheck.pocTotal > 0) {
            host.appendChild(el("p", "sub",
                "POC module (a second Go module; the root record excludes it): " +
                st.redCheck.pocProved + " of " + st.redCheck.pocTotal + " controls proved, " +
                st.redCheck.pocSkipped + " skipped. Read " + st.redCheck.pocFile + "."));
        }
    }

    function renderNotes(st) {
        var host = document.getElementById("notes");
        host.textContent = "";
        (st.notes || []).forEach(function (n) {
            var box = el("div", "note");
            box.appendChild(el("b", null, "Honest status: "));
            box.appendChild(document.createTextNode(n));
            host.appendChild(box);
        });
    }

    function render(st) {
        renderPills(st);
        renderKpis(st);
        renderArtifacts(st);
        renderGate(st);
        renderNotes(st);
    }

    function failed(err) {
        var host = document.getElementById("pills");
        host.textContent = "";
        var box = el("div", "note");
        box.appendChild(el("b", null, "The status endpoint is not answering: "));
        box.appendChild(document.createTextNode(String(err)));
        host.appendChild(box);
    }

    document.addEventListener("DOMContentLoaded", function () {
        fetch("/api/showcase/status")
            .then(function (r) { if (!r.ok) { throw new Error("HTTP " + r.status); } return r.json(); })
            .then(render)
            .catch(failed);
    });
})();
