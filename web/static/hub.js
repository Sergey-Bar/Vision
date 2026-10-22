(function () {
    "use strict";

    // The front door's three facts.
    //
    // Every value on this page comes from the server, and every value names
    // the file it was read from. There is no fallback number, no plausible
    // default and no "0" standing in for a measurement that was never made:
    // this machine either produced the artefact or it did not, and the page
    // says which.
    //
    // The three are the ones the judge track names: the gate, the red-check
    // and the demo's own incidents. The first two come from one endpoint and
    // the third from another, because the incident documents live in a
    // different place from the gate record and a page that conflated them
    // would be claiming one file says two things.

    function el(tag, cls, text) {
        var node = document.createElement(tag);
        if (cls) { node.className = cls; }
        if (text !== undefined && text !== null) { node.textContent = String(text); }
        return node;
    }

    function chip(word, kind) {
        return el("span", "chip " + kind, word);
    }

    function fact(label, word, kind, value, source, absent) {
        var f = el("div", "fact");
        var head = el("div", "fact-label");
        head.appendChild(el("span", null, label));
        head.appendChild(chip(word, kind));
        f.appendChild(head);
        var v = el("div", "fact-value" + (absent ? " is-absent" : ""), value);
        f.appendChild(v);
        f.appendChild(el("div", "fact-source", source));
        return f;
    }

    // ---------- the three facts ------------------------------------------------

    function gateFact(st) {
        var g = st && st.gate;
        if (!g) {
            return fact("Gate", "NOT RUN", "neutral", "No gate record on this machine",
                "dist/verification holds no verify-*.json — run scripts/verify.ps1 and it writes one.", true);
        }
        var kind = g.outcome === "passed" ? "ok" : (g.outcome === "failed" ? "bad" : "warn");
        var word = String(g.outcome || "unknown").toUpperCase();
        var value = g.gates + (g.gates === 1 ? " gate" : " gates") +
            " · " + g.failed + " failed" +
            (g.skipped ? " · " + g.skipped + " skipped" : "");
        var source = g.record + " · finished " + (g.finishedAt || "at an unrecorded time");
        if (g.dirty) {
            source += " · taken over a dirty worktree";
        }
        return fact("Gate", word, kind, value, source, false);
    }

    function redCheckFact(st) {
        var r = (st && st.redCheck) || {};
        if (!r.pocTotal) {
            return fact("Red-check", "NOT RUN", "neutral", "No transcript on this machine",
                "poc/active-monitoring/redcheck-transcripts.txt is absent — a control is a control " +
                "only after it has been watched failing.", true);
        }
        var word = r.pocProved === r.pocTotal ? "PROVED" : "PARTIAL";
        var kind = r.pocProved === r.pocTotal ? "ok" : "warn";
        var value = r.pocProved + " of " + r.pocTotal + " controls proved";
        var source = r.pocFile + (r.pocAt ? " · " + r.pocAt : "");
        return fact("Red-check", word, kind, value, source, false);
    }

    function incidentFact(inc) {
        if (!inc) {
            return fact("Incidents", "NOT RUN", "neutral", "The server did not answer",
                "/api/showcase/incidents did not answer, so the count is unknown. It is not zero.", true);
        }
        if (!inc.found) {
            return fact("Incidents", "NOT RUN", "neutral", "No incident documents on this machine",
                (inc.reason || "dist/demo holds no incident documents.") + " — scripts/demo.ps1 writes them.", true);
        }
        var n = (inc.pairs || []).length;
        return fact("Incidents", "RECORDED", "info", n + (n === 1 ? " incident document" : " incident documents"),
            "dist/demo — the documents the pipeline page replays", false);
    }

    // ---------- the page ---------------------------------------------------------

    function render(st, inc) {
        var host = document.getElementById("facts");
        if (!host) { return; }
        host.textContent = "";
        host.setAttribute("aria-busy", "false");
        host.appendChild(gateFact(st));
        host.appendChild(redCheckFact(st));
        host.appendChild(incidentFact(inc));

        var readAt = document.getElementById("readAt");
        if (readAt) {
            readAt.textContent = "Everything on this page was read from this machine at " +
                ((st && st.generatedAt) || "an unrecorded time") + ".";
        }
    }

    function getJSON(url) {
        return fetch(url, { cache: "no-store" }).then(function (r) {
            if (!r.ok) { throw new Error(url + " answered " + r.status); }
            return r.json();
        });
    }

    document.addEventListener("DOMContentLoaded", function () {
        // The two endpoints are fetched together and rendered together, so a
        // slow one does not leave the page half-written: the facts appear
        // when they are all known, not as they arrive.
        Promise.all([
            getJSON("/api/showcase/status").catch(function (e) { return { error: String(e) }; }),
            getJSON("/api/showcase/incidents").catch(function (e) { return { error: String(e) }; }),
        ]).then(function (both) {
            render(both[0], both[1]);
        });
    });
})();
