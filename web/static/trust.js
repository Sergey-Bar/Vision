// The trust page. It renders the gate's own record of itself and wires the
// three fixed commands, so the claims on the page can be falsified from the
// page. Everything it shows is read at load time; nothing is cached between
// runs, because a cached proof is a story about a proof.
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

    // codeCell builds a <td> that carries a <code>. The element goes in through
    // appendChild, never through el()'s text argument: el() sets textContent,
    // and String(element) is "[object HTMLElement]" - which is what the gate
    // column printed before this helper existed.
    function codeCell(text) {
        var td = el("td");
        td.appendChild(el("code", null, text));
        return td;
    }

    function renderGate(st) {
        var host = document.getElementById("gatebox");
        host.textContent = "";
        if (!st.gate) {
            host.appendChild(el("p", "sub", "No verification record exists on this machine. Run scripts/verify.ps1 and it will write one."));
            return;
        }
        var g = st.gate;
        var p = el("p");
        p.appendChild(el("b", null, g.outcome === "passed" ? "All " + g.gates + " gates passed. " : "The last run did not pass. "));
        p.appendChild(document.createTextNode(g.exitReason || ""));
        host.appendChild(p);
        host.appendChild(el("p", "sub", g.record + " · finished " + fmtTime(g.finishedAt)));

        var kv = el("dl", "kv");
        [
            ["commit", g.commitShort + (g.dirty ? " — worktree dirty at run time" : "")],
            ["mode", "race " + (g.race ? "on" : "off") + " · cgo " + (g.cgo ? "on" : "off")],
            ["database", g.dbUrlSet ? "reached" : "not set — the database suites skipped"],
            ["skipped / failed", g.skipped + " / " + g.failed],
            ["ci", g.ci || "none"]
        ].forEach(function (pair) {
            kv.appendChild(el("dt", null, pair[0]));
            kv.appendChild(el("dd", null, pair[1]));
        });
        host.appendChild(kv);

        var tbl = el("table", "tbl");
        var head = el("tr", null);
        head.appendChild(el("th", null, "gate"));
        head.appendChild(el("th", null, "outcome"));
        head.appendChild(el("th", null, "seconds"));
        tbl.appendChild(head);
        (g.gateList || []).forEach(function (row) {
            var tr = el("tr", null);
            tr.appendChild(codeCell(row.name));
            var td = el("td", null, row.outcome);
            tr.appendChild(td);
            tr.appendChild(el("td", "num", row.seconds));
            tbl.appendChild(tr);
        });
        host.appendChild(tbl);
    }

    function renderExcluded(st) {
        var host = document.getElementById("excluded");
        host.textContent = "";
        if (!st.gate || !st.gate.excluded || st.gate.excluded.length === 0) {
            host.appendChild(el("p", "sub", "The record lists nothing as excluded — which for this repository would itself be suspicious."));
            return;
        }
        st.gate.excluded.forEach(function (x) {
            var box = el("div", "note");
            box.appendChild(document.createTextNode(x));
            host.appendChild(box);
        });
    }

    function renderRedCheck(st) {
        var root = document.getElementById("root-red");
        root.textContent = "";
        if (st.redCheck.rootCount > 0) {
            root.appendChild(el("p", null, st.redCheck.rootCount + " controls are declared in the root table. The last gate run executed the table and every control turned the suite red when it was removed — the record above carries that gate as " + "\u201Cred-check\u201D."));
            root.appendChild(el("p", "sub", "The transcripts of the earliest entries are quoted in docs/status.md, beside the claim they prove."));
            root.appendChild(el("p", "sub", "\u201CA control with no such run is not a control; it is a comment.\u201D"));
        } else {
            root.appendChild(el("p", "sub", "The table could not be read from scripts/verify.ps1 on this machine."));
        }

        var poc = document.getElementById("poc-red");
        poc.textContent = "";
        if (st.redCheck.pocTotal > 0) {
            var line = el("p");
            line.appendChild(el("b", null, st.redCheck.pocProved + " of " + st.redCheck.pocTotal + " controls proved"));
            line.appendChild(document.createTextNode(", " + st.redCheck.pocSkipped + " skipped. Each one applied to the file on disk, the test run, the failure recorded, the file restored, the restore confirmed."));
            poc.appendChild(line);
            poc.appendChild(el("p", "sub", st.redCheck.pocFile + " · " + (st.redCheck.pocAt || "")));
            poc.appendChild(el("p", "sub", "\u201CThe baseline was run FIRST and is required to be green: an exit code after a mutation is only evidence if the same command was green before it.\u201D"));
        } else {
            poc.appendChild(el("p", "sub", "The POC transcript is not on this machine. It is written by poc/active-monitoring/scripts/redcheck-transcript.ps1."));
        }
    }

    function resultHost() {
        var host = document.getElementById("run-result");
        host.textContent = "";
        return host;
    }

    function showNotRun(host, reason) {
        var box = el("div", "note");
        box.appendChild(el("b", null, "NOT RUN. "));
        box.appendChild(document.createTextNode(reason));
        host.appendChild(box);
    }

    function showRun(host, res) {
        var head = el("div", "out-head");
        head.appendChild(el("code", null, res.command));
        var exit = el("span", "chip " + (res.exit === 0 ? "live" : "recorded"),
            "exit " + res.exit);
        head.appendChild(exit);
        if (res.dataRoot) {
            head.appendChild(el("span", null, "data root: " + res.dataRoot));
        }
        host.appendChild(head);
        var pre = el("pre", "out " + (res.exit === 0 ? "ok" : "bad"), res.output || "(no output)");
        host.appendChild(pre);
        if (res.exit === 0) {
            host.appendChild(el("p", "sub", "Exit 0 is the command's own verdict, printed here as it printed it."));
        } else {
            host.appendChild(el("p", "sub", "Non-zero exit. Read the output above before reading the number: a demonstrated break and a failed run both exit non-zero, and the command's own words are the difference."));
        }
    }

    function run(button, url, body) {
        var host = resultHost();
        var buttons = document.querySelectorAll(".btn-row .btn");
        buttons.forEach(function (b) { b.disabled = true; });
        host.appendChild(el("p", "sub", "running — the command is executed on this machine…"));
        fetch(url, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: body ? JSON.stringify(body) : ""
        })
            .then(function (r) { if (!r.ok) { throw new Error("HTTP " + r.status + " " + r.statusText); } return r.json(); })
            .then(function (res) {
                host.textContent = "";
                if (!res.ran) { showNotRun(host, res.reason); } else { showRun(host, res); }
            })
            .catch(function (err) {
                host.textContent = "";
                showNotRun(host, "the request itself failed: " + err);
            })
            .finally(function () {
                buttons.forEach(function (b) { b.disabled = false; });
            });
    }

    document.addEventListener("DOMContentLoaded", function () {
        fetch("/api/showcase/status")
            .then(function (r) { return r.json(); })
            .then(function (st) {
                renderGate(st);
                renderExcluded(st);
                renderRedCheck(st);
            })
            .catch(function (err) {
                document.getElementById("gatebox").textContent = "the status endpoint is not answering: " + err;
            });

        document.getElementById("btn-verify").addEventListener("click", function () {
            run(this, "/api/showcase/ledger", { mode: "verify" });
        });
        document.getElementById("btn-tamper").addEventListener("click", function () {
            run(this, "/api/showcase/ledger", { mode: "tamper" });
        });
        document.getElementById("btn-prove").addEventListener("click", function () {
            run(this, "/api/showcase/prove", null);
        });
    });
})();
