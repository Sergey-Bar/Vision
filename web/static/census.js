// The census page. It renders web/static/census.json verbatim, grades included,
// and the grade filter is the only interaction: pressing "NOT RUN" is the
// fastest way to see where this repository knows more than it has measured.
(function () {
    "use strict";

    var rows = [];
    var active = "All";

    var GRADES = ["All", "Live", "Recorded", "NOT RUN", "Lab"];

    function el(tag, cls, text) {
        var node = document.createElement(tag);
        if (cls) { node.className = cls; }
        if (text !== undefined && text !== null) { node.textContent = String(text); }
        return node;
    }

    function gradeChip(grade) {
        var cls = "chip plain";
        if (grade === "Live") { cls = "chip live"; }
        else if (grade === "Recorded") { cls = "chip recorded"; }
        else if (grade === "NOT RUN") { cls = "chip notrun"; }
        else if (grade === "Lab") { cls = "chip lab"; }
        return el("span", cls, grade);
    }

    function surfaceCell(surface) {
        var td = el("td", null);
        if (!surface) { return td; }
        if (surface.charAt(0) === "/") {
            var a = el("a", null, surface);
            a.href = surface;
            td.appendChild(a);
        } else {
            td.appendChild(el("span", "mono", surface));
        }
        return td;
    }

    // codeCell builds a <td> that carries a <code>. The element goes in through
    // appendChild, never through el()'s text argument: el() sets textContent,
    // and String(element) is "[object HTMLElement]" - which is what the proof
    // and artefact columns printed before this helper existed.
    function codeCell(text) {
        var td = el("td", null);
        td.appendChild(el("code", null, text));
        return td;
    }

    function rowToTr(row) {
        var tr = el("tr", null);

        var tdCap = el("td", null);
        tdCap.appendChild(el("b", null, row.capability));
        tdCap.appendChild(el("span", "why", row.area + " · " + row.note));
        tr.appendChild(tdCap);

        tr.appendChild(codeCell(row.proof));
        tr.appendChild(codeCell(row.artefact));
        tr.appendChild(surfaceCell(row.surface));

        var tdGrade = el("td", null);
        tdGrade.appendChild(gradeChip(row.grade));
        tr.appendChild(tdGrade);

        return tr;
    }

    function counts() {
        var out = { All: rows.length };
        GRADES.slice(1).forEach(function (g) { out[g] = 0; });
        rows.forEach(function (r) {
            if (out[r.grade] === undefined) { out[r.grade] = 0; }
            out[r.grade] += 1;
        });
        return out;
    }

    function renderFilters() {
        var host = document.getElementById("filters");
        host.textContent = "";
        var c = counts();
        GRADES.forEach(function (g) {
            var b = el("button", null, g + " · " + (c[g] || 0));
            b.type = "button";
            b.setAttribute("aria-pressed", g === active ? "true" : "false");
            b.addEventListener("click", function () {
                active = g;
                renderFilters();
                renderTable();
            });
            host.appendChild(b);
        });
        var line = document.getElementById("count-line");
        line.textContent = rows.length + " rows in " +
            "web/static/census.json, generated 2026-10-15. A grade is never rounded up: " +
            "a row moves from NOT RUN to Recorded only when a dated artefact exists.";
    }

    function renderTable() {
        var body = document.getElementById("census-body");
        body.textContent = "";
        var visible = rows.filter(function (r) { return active === "All" || r.grade === active; });
        if (visible.length === 0) {
            var tr = el("tr", null);
            var td = el("td", null);
            td.colSpan = 5;
            td.appendChild(el("span", "sub", "No rows with this grade."));
            tr.appendChild(td);
            body.appendChild(tr);
            return;
        }
        visible.forEach(function (r) { body.appendChild(rowToTr(r)); });
    }

    function failed(err) {
        var body = document.getElementById("census-body");
        body.textContent = "";
        var tr = el("tr", null);
        var td = el("td", null);
        td.colSpan = 5;
        td.appendChild(el("div", "note", "census.json could not be read: " + err));
        tr.appendChild(td);
        body.appendChild(tr);
    }

    document.addEventListener("DOMContentLoaded", function () {
        fetch("/static/census.json")
            .then(function (r) { if (!r.ok) { throw new Error("HTTP " + r.status); } return r.json(); })
            .then(function (doc) {
                rows = doc.rows || [];
                renderFilters();
                renderTable();
            })
            .catch(failed);
    });
})();
