(function () {
    "use strict";

    function el(tag, cls, text) {
        var node = document.createElement(tag);
        if (cls) { node.className = cls; }
        if (text !== undefined && text !== null) { node.textContent = String(text); }
        return node;
    }

    function $(id) { return document.getElementById(id); }

    function modal(id) { return document.getElementById("modal-" + id); }

    var state = {
        grid: 4,
        nerdsOpen: false,
        status: null,
        liveOn: false,
        cells: [
            { id: "cam-1", name: "CAM-01 North Gate" },
            { id: "cam-2", name: "CAM-02 Perimeter" },
            { id: "cam-3", name: "CAM-03 Warehouse" },
            { id: "cam-5", name: "CAM-05 Parking" },
            { id: "cam-4", name: "CAM-04 Loading Dock" },
            { id: "cam-6", name: "CAM-06 Office" },
        ],
        frames: {},   // cellId -> latest frame
    };

    // ---------- live feed (single WebSocket, fanned to every cell) ----------

    var liveFeed = (function () {
        var ws = null;
        var reconnectTimer = null;
        var url = "https://www.youtube.com/watch?v=JQ_jwk_7OVE";

        function cellEls(cellId) {
            return {
                img: $("feed-" + cellId),
                pending: $("pending-" + cellId),
                meta: $("meta-" + cellId),
                badge: $("badge-" + cellId),
                overlay: $("overlay-" + cellId),
                ts: $("ts-" + cellId),
            };
        }

        function showCell(cellId, frame) {
            var e = cellEls(cellId);
            if (!e) return;
            if (e.img) { e.img.src = frame.imageData || ""; e.img.style.display = frame.imageData ? "block" : "none"; }
            if (e.pending) { e.pending.style.display = "none"; }
            if (e.meta) {
                var parts = [];
                if (frame.sourceWidth && frame.sourceHeight) parts.push(frame.sourceWidth + "×" + frame.sourceHeight);
                if (frame.analysisFps != null) parts.push(frame.analysisFps.toFixed(1) + " FPS");
                if (frame.inferenceMS != null) parts.push(frame.inferenceMS.toFixed(0) + "ms");
                e.meta.textContent = parts.length ? parts.join(" · ") : "—";
            }
            if (e.badge) {
                e.badge.textContent = "Live";
                e.badge.className = "badge live";
            }
            if (e.overlay) {
                e.overlay.innerHTML = "";
                (frame.detections || []).forEach(function (d) {
                    var box = el("div", "detection-box" + (d.violation ? " warning" : ""));
                    var [x, y, w, h] = d.bbox;
                    box.style.top = (y * 100) + "%";
                    box.style.left = (x * 100) + "%";
                    box.style.width = (w * 100) + "%";
                    box.style.height = (h * 100) + "%";
                    box.appendChild(el("span", "label", d.label + " " + (d.confidence * 100).toFixed(0) + "%"));
                    e.overlay.appendChild(box);
                });
            }
            if (e.ts) {
                e.ts.textContent = frame.at ? new Date(frame.at).toLocaleTimeString() : "—";
            }
        }

        function clearCell(cellId) {
            var e = cellEls(cellId);
            if (!e) return;
            if (e.img) { e.img.src = ""; e.img.style.display = "none"; }
            if (e.pending) { e.pending.style.display = "block"; e.pending.textContent = "Live feed disconnected."; }
            if (e.meta) e.meta.textContent = "—";
            if (e.badge) { e.badge.textContent = "Idle"; e.badge.className = "badge"; }
            if (e.overlay) e.overlay.innerHTML = "";
            if (e.ts) e.ts.textContent = "—";
        }

        function onFrame(frame) {
            state.frames[frame.videoId || "cam-1"] = frame;
            state.cells.forEach(function (c) { showCell(c.id, frame); });
            renderNerdsFeed();
        }

        function connect() {
            if (ws) { try { ws.close(); } catch (e) { } ws = null; }
            if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
            state.cells.forEach(function (c) { clearCell(c.id); });
            var proto = location.protocol === "https:" ? "wss:" : "ws:";
            var qs = new URLSearchParams({ url: url, profile: "realtime", protocol: "2" });
            ws = new WebSocket(proto + "//" + location.host + "/ws/live?" + qs.toString());
            ws.onopen = function () {
                state.liveOn = true;
                var btn = $("liveToggle");
                if (btn) { btn.textContent = "Live on"; btn.classList.add("active"); }
                var dot = $("live-count") && $("live-count").querySelector(".dot");
                if (dot) dot.classList.add("live");
            };
            ws.onmessage = function (ev) {
                try { var frame = JSON.parse(ev.data); } catch (e) { return; }
                if (frame.error) {
                    state.cells.forEach(function (c) {
                        var e = cellEls(c.id);
                        if (e && e.pending) { e.pending.style.display = "block"; e.pending.textContent = frame.error; }
                    });
                    return;
                }
                onFrame(frame);
            };
            ws.onclose = function () {
                ws = null;
                state.liveOn = false;
                var btn = $("liveToggle");
                if (btn) { btn.textContent = "Live off"; btn.classList.remove("active"); }
                var dot = $("live-count") && $("live-count").querySelector(".dot");
                if (dot) dot.classList.remove("live");
                state.cells.forEach(clearCell);
                reconnectTimer = setTimeout(connect, 3000);
            };
            ws.onerror = function () {
                if (ws) { try { ws.close(); } catch (e) { } ws = null; }
            };
        }

        function toggle() {
            if (state.liveOn && ws) {
                try { ws.close(); } catch (e) { }
                ws = null;
                if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
                state.liveOn = false;
            } else {
                connect();
            }
            var btn = $("liveToggle");
            if (btn) btn.textContent = state.liveOn ? "Live on" : "Live off";
            var dot = $("live-count") && $("live-count").querySelector(".dot");
            if (dot) dot.classList.toggle("live", state.liveOn);
            renderNerdsFeed();
        }

        return { toggle: toggle, connect: connect, onFrame: onFrame };
    })();

    // ---------- shell ----------

    function openModal(id) {
        var m = modal(id);
        if (!m) { return; }
        m.classList.add("open");
        m.setAttribute("aria-hidden", "false");
        document.body.style.overflow = "hidden";
    }

    function closeModal(id) {
        var m = modal(id);
        if (!m) { return; }
        m.classList.remove("open");
        m.setAttribute("aria-hidden", "true");
        document.body.style.overflow = "";
    }

    function closeAll() {
        document.querySelectorAll(".vms-modal-overlay.open").forEach(function (m) {
            m.classList.remove("open");
            m.setAttribute("aria-hidden", "true");
        });
        document.body.style.overflow = "";
    }

    function setGrid(n) {
        state.grid = n;
        var w = $("videoWall");
        if (!w) { return; }
        w.className = "video-wall grid-" + n;
        document.querySelectorAll(".vms-toolbar .toolbar-btn").forEach(function (b) {
            b.classList.remove("active");
            var on = b.getAttribute("onclick") || "";
            if (on.indexOf("setGrid(" + n + ")") >= 0) { b.classList.add("active"); }
        });
    }

    function toggleRecord(btn) {
        btn.classList.toggle("active");
        var dot = $("live-count") && $("live-count").querySelector(".dot");
        if (dot) dot.classList.toggle("rec", btn.classList.contains("active"));
    }

    function toggleNerds() {
        state.nerdsOpen = !state.nerdsOpen;
        var p = $("nerds-panel");
        var b = $("nerdsBtn");
        if (!p) { return; }
        if (state.nerdsOpen) { p.style.display = "flex"; if (b) { b.classList.add("active"); } }
        else { p.style.display = "none"; if (b) { b.classList.remove("active"); } }
        renderNerds();
    }

    document.addEventListener("keydown", function (e) {
        if (e.key === "Escape") { closeAll(); }
    });

    document.addEventListener("click", function (e) {
        if (e.target.classList.contains("vms-modal-overlay")) {
            closeModal(e.target.id.replace("modal-", ""));
        }
    });

    // ---------- data ----------

    function kpi(value, caption) {
        var b = el("div", "data-card");
        b.appendChild(el("div", "label", caption));
        b.appendChild(el("div", "value", value));
        return b;
    }

    function chip(stateName, label) {
        var c = el("span", "chip " + stateName);
        c.appendChild(document.createTextNode(label));
        return c;
    }

    function fetchStatus() {
        fetch("/api/showcase/status")
            .then(function (r) { return r.ok ? r.json() : null; })
            .then(function (st) {
                state.status = st;
                renderModals(st);
                renderNerds(st);
            })
            .catch(function () { });
    }

    function renderModals(st) {
        var g = st && st.gate ? st.gate : null;

        var kpis = $("modal-kpis");
        if (kpis) {
            kpis.textContent = "";
            kpis.appendChild(kpi(state.liveOn ? "on" : "off", "Live analysis"));
            kpis.appendChild(kpi(g ? String(g.gates) : "—", "Verification gates"));
            kpis.appendChild(kpi(g && g.outcome ? g.outcome : "—", "Last gate outcome"));
        }

        var tiles = $("modal-tiles");
        if (tiles) {
            tiles.textContent = "";
            state.cells.forEach(function (c) {
                var t = el("div", "data-card");
                t.appendChild(el("div", "label", c.name));
                t.appendChild(el("div", "value", state.liveOn ? "live" : "idle"));
                t.appendChild(chip(state.liveOn ? "ok" : "neutral", state.liveOn ? "streaming" : "idle"));
                tiles.appendChild(t);
            });
        }

        var ev = $("events-grid");
        if (ev) {
            ev.textContent = "";
            ev.appendChild(kpi(g ? String(g.failed) : "0", "Failed gates"));
            ev.appendChild(kpi(g ? String(g.skipped) : "0", "Skipped gates"));
            ev.appendChild(kpi(g && g.exitReason ? "1" : "0", "Exit reasons"));
        }

        var tb = $("events-table");
        if (tb) {
            var body = $("events-tbody");
            if (body) {
                body.textContent = "";
                var rows = g && g.gateList ? g.gateList : [];
                if (rows.length === 0) {
                    var tr = el("tr");
                    var td = el("td", null, "no gate events"); td.setAttribute("colspan", "5"); tr.appendChild(td); body.appendChild(tr);
                } else {
                    rows.forEach(function (r) {
                        tr = el("tr");
                        tr.appendChild(el("td", "num", String(r.seconds) + "s"));
                        tr.appendChild(el("td", null, r.name));
                        tr.appendChild(el("td", null, "—"));
                        tr.appendChild(el("td", "num", "—"));
                        tr.appendChild(el("td", null, chip(r.outcome === "passed" ? "ok" : "bad", r.outcome)));
                        body.appendChild(tr);
                    });
                }
            }
        }

        var ag = $("archive-gate");
        if (ag) {
            ag.textContent = "";
            ag.appendChild(el("div", null, g ? (g.outcome + " · " + g.gates + " gates · " + g.failed + " failed") : "no verification record"));
            ag.appendChild(el("div", "sub", g ? g.record : "run scripts/verify.ps1"));
        }

        var aa = $("archive-artifacts");
        if (aa) {
            aa.textContent = "";
            var arts = st && st.artifacts ? st.artifacts : [];
            if (arts.length === 0) { aa.appendChild(el("p", "sub", "no artefacts")); }
            else { arts.forEach(function (a) { aa.appendChild(el("div", null, a.path + " · " + a.size + " bytes")); }); }
        }

        var ci = $("config-sysinfo");
        if (ci) {
            ci.textContent = "";
            ci.appendChild(kpi(g && g.race ? "on" : "off", "Race detector"));
            ci.appendChild(kpi(g && g.cgo ? "on" : "off", "CGO"));
            ci.appendChild(kpi(g && g.dbUrlSet ? "set" : "not set", "Database URL"));
            ci.appendChild(kpi(g && g.commitShort ? g.commitShort : "—", "Commit"));
        }

        var lc = $("logs-content");
        if (lc) {
            lc.textContent = "";
            lc.appendChild(el("div", null, g ? ("gate: " + g.outcome + " · " + g.record) : "no gate record"));
            lc.appendChild(el("div", "sub", g ? ("finished " + g.finishedAt + " · " + (g.exitReason || "—")) : ""));
            if (st && st.notes) { st.notes.forEach(function (n) { lc.appendChild(el("div", "sub", "note: " + n)); }); }
        }
    }

    function renderNerdsFeed() {
        var host = $("nerds-feed");
        if (!host) return;
        host.textContent = "";
        var active = state.liveOn ? Object.keys(state.frames).length : 0;
        host.appendChild(kpi(state.liveOn ? "on" : "off", "WebSocket"));
        host.appendChild(kpi(String(active), "Frames received"));
        host.appendChild(kpi(state.liveOn ? "realtime" : "—", "Profile"));
    }

    function renderNerds(st) {
        var sys = $("nerds-system");
        if (sys) {
            sys.textContent = "";
            sys.appendChild(kpi("34%", "CPU"));
            sys.appendChild(kpi("67%", "Memory"));
            sys.appendChild(kpi(state.cells.length, "Stream cells"));
            sys.appendChild(kpi(state.liveOn ? "streaming" : "idle", "Feed state"));
        }
        var ver = $("nerds-verification");
        if (ver) {
            ver.textContent = "";
            var g = st && st.gate ? st.gate : null;
            ver.appendChild(kpi(g ? g.outcome : "—", "Last gate"));
            ver.appendChild(kpi(g && g.gateList ? String(g.gateList.length) : "0", "Gate rows"));
            ver.appendChild(kpi(g && g.record ? g.record.split("/").pop() : "—", "Record file"));
        }
        var j = $("nerds-json");
        if (j) {
            try { j.textContent = JSON.stringify(st, null, 2); } catch (e) { j.textContent = "status unavailable"; }
        }
    }

    document.addEventListener("DOMContentLoaded", function () {
        setGrid(4);
        fetchStatus();
        liveFeed.connect();
    });

    window.openModal = openModal;
    window.closeModal = closeModal;
    window.setGrid = setGrid;
    window.toggleRecord = toggleRecord;
    window.toggleNerds = toggleNerds;
})();
