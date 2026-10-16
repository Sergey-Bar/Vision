// The pipeline page. The journey is rendered from the demo's own incident and
// observation documents: every field is shown under the name it has on disk,
// and a document that is missing is said to be missing rather than skipped.
(function () {
    "use strict";

    var pairs = [];
    var selected = 0;

    function el(tag, cls, text) {
        var node = document.createElement(tag);
        if (cls) { node.className = cls; }
        if (text !== undefined && text !== null) { node.textContent = String(text); }
        return node;
    }

    function show(v) {
        if (v === undefined || v === null || v === "") { return null; }
        if (typeof v === "boolean") { return v ? "true" : "false"; }
        return String(v);
    }

    function kvTable(pairsOut) {
        var dl = el("dl", "kv");
        pairsOut.forEach(function (pair) {
            var v = show(pair[1]);
            if (v === null) { return; }
            dl.appendChild(el("dt", null, pair[0]));
            dl.appendChild(el("dd", null, v));
        });
        return dl;
    }

    function stage(title, subtitle, nodes) {
        var li = el("li", "stage");
        var rail = el("div", "rail");
        rail.appendChild(el("span", "dot"));
        rail.appendChild(el("span", "line"));
        li.appendChild(rail);
        var body = el("div", "body");
        body.appendChild(el("h3", null, title));
        if (subtitle) { body.appendChild(el("p", null, subtitle)); }
        nodes.forEach(function (n) { if (n) { body.appendChild(n); } });
        li.appendChild(body);
        return li;
    }

    function flags(incident, observation) {
        var row = el("div", "btn-row");
        function chip(cls, text) {
            row.appendChild(el("span", "chip " + cls, text));
        }
        if (incident.decidedWithoutAiObservation) { chip("recorded", "decided without an AI observation"); }
        if (incident.observationDegraded) { chip("recorded", "observation degraded"); }
        if (incident.awaitingOperatorDisposition) { chip("lab", "awaiting an operator"); }
        if (incident.evidence && incident.evidence.hashVerified) { chip("live", "evidence hash verified"); }
        if (observation && observation.insufficientEvidence) { chip("recorded", "insufficient evidence"); }
        if (observation && observation.idMintedLocally) { chip("plain", "id minted locally"); }
        return row.childElementCount > 0 ? row : null;
    }

    function renderJourney() {
        var host = document.getElementById("journey");
        var source = document.getElementById("journey-source");
        host.textContent = "";
        if (pairs.length === 0) {
            source.textContent = "Nothing to render.";
            return;
        }
        var pair = pairs[selected];
        var inc = pair.incident || {};
        var obs = pair.observation || null;
        source.textContent = "Rendered from " + (inc.incidentId || "an incident document") + " and its observation, as written by the run.";

        host.appendChild(stage(
            "1 · The event arrives",
            "Ingested and normalized before anything looked at a frame.",
            [kvTable([
                ["eventId", inc.eventId],
                ["raisedAt", inc.raisedAt],
                ["tenantId", inc.tenantId],
                ["siteId", inc.siteId]
            ])]));

        host.appendChild(stage(
            "2 · The decision",
            "Made from stored facts. The reason is the sentence an operator reads, so it is quoted in full.",
            [el("p", null, inc.reason || ""),
             kvTable([
                 ["state", inc.state],
                 ["severity", inc.severity],
                 ["title", inc.title],
                 ["reasonCode", inc.reasonCode]
             ]),
             flags(inc, obs)]));

        var evNodes = [];
        if (inc.evidence) {
            evNodes.push(kvTable([
                ["storageRef", inc.evidence.storageRef],
                ["sha256", inc.evidence.sha256],
                ["sizeBytes", inc.evidence.sizeBytes],
                ["mediaType", inc.evidence.mediaType],
                ["hashVerified", inc.evidence.hashVerified]
            ]));
            evNodes.push(el("p", "sub",
                inc.evidence.hashVerified
                    ? "The bytes under this decision were re-hashed and matched. An edited clip would fail this field before it failed anything else."
                    : "This evidence row does not carry a verified hash; that is shown, not smoothed over."));
        } else {
            evNodes.push(el("p", "sub", "This incident document carries no evidence block."));
        }
        host.appendChild(stage("3 · The evidence under it", "What the decision can be checked against without trusting the decider.", evNodes));

        var aiNodes = [];
        if (obs) {
            aiNodes.push(kvTable([
                ["model", obs.model],
                ["runtimeVersion", obs.runtimeVersion],
                ["profileId", obs.profileId],
                ["degraded", obs.degraded],
                ["degradedReason", obs.degradedReason],
                ["insufficientEvidence", obs.insufficientEvidence],
                ["observationId", obs.observationId]
            ]));
            if (obs.degradedDetail) {
                aiNodes.push(el("p", "sub", obs.degradedDetail));
            }
            if (obs.degraded) {
                aiNodes.push(el("div", "note",
                    "Degraded is not negative. This row says the model was never asked and why; a reader must not be able to read it as “the model looked and found nothing”."));
            }
        } else {
            aiNodes.push(el("p", "sub", pair.observationMissing || "No observation document was written for this incident."));
        }
        host.appendChild(stage("4 · The AI plane", "Where the product is most careful: an absent answer stays absent.", aiNodes));
    }

    function renderPick() {
        var host = document.getElementById("pick");
        host.textContent = "";
        pairs.forEach(function (pair, i) {
            var inc = pair.incident || {};
            var b = el("button", null);
            b.type = "button";
            b.setAttribute("aria-current", i === selected ? "true" : "false");
            b.appendChild(el("span", "t", inc.title || inc.incidentId || "incident"));
            b.appendChild(el("span", "d",
                (inc.raisedAt || "") + " · " + (inc.reasonCode || "") + " · " + (inc.severity || "")));
            b.addEventListener("click", function () {
                selected = i;
                renderPick();
                renderJourney();
            });
            host.appendChild(b);
        });
    }

    function fromData(body) {
        pairs = body.pairs || [];
        var host = document.getElementById("pick");
        if (!body.found || pairs.length === 0) {
            host.textContent = "";
            var box = el("div", "note");
            box.appendChild(el("b", null, "NOT RUN. "));
            box.appendChild(document.createTextNode(body.reason || "no incident documents are on this machine"));
            host.appendChild(box);
            document.getElementById("journey").textContent = "";
            document.getElementById("journey-source").textContent =
                "Nothing has run here yet, so there is no journey to render — which is the honest state of a machine that has not run the demo.";
            return;
        }
        renderPick();
        renderJourney();
    }

    document.addEventListener("DOMContentLoaded", function () {
        fetch("/api/showcase/incidents")
            .then(function (r) { if (!r.ok) { throw new Error("HTTP " + r.status); } return r.json(); })
            .then(fromData)
            .catch(function (err) {
                var host = document.getElementById("pick");
                host.textContent = "";
                host.appendChild(el("div", "note", "the incidents endpoint is not answering: " + err));
            });
    });
})();
