(function() {
    "use strict";

    const $ = (sel) => document.querySelector(sel);
    const $$ = (sel) => document.querySelectorAll(sel);

    // --- Auth ---
    const loginView = $("#login-view");
    const dashView = $("#dashboard-view");

    $("#login-form").addEventListener("submit", async (e) => {
        e.preventDefault();
        const err = $("#login-error");
        err.hidden = true;
        try {
            const res = await fetch("/api/login", {
                method: "POST",
                headers: {"Content-Type": "application/json"},
                body: JSON.stringify({
                    username: $("#username").value,
                    password: $("#password").value
                })
            });
            if (!res.ok) {
                const data = await res.json();
                throw new Error(data.error || "Login failed");
            }
            loginView.hidden = true;
            dashView.hidden = false;
            loadStatus();
        } catch (ex) {
            err.textContent = ex.message;
            err.hidden = false;
        }
    });

    $("#logout-btn").addEventListener("click", async () => {
        await fetch("/api/logout", {method: "POST"});
        dashView.hidden = true;
        loginView.hidden = false;
        $("#password").value = "";
    });

    // --- Tabs ---
    $$(".tab:not(.tab-right)").forEach((btn) => {
        btn.addEventListener("click", () => {
            $$(".tab").forEach((t) => t.classList.remove("active"));
            btn.classList.add("active");
            $$(".tab-content").forEach((c) => c.hidden = true);
            const target = $("#tab-" + btn.dataset.tab);
            if (target) target.hidden = false;
            if (btn.dataset.tab === "cdm") loadCDM();
            if (btn.dataset.tab === "audit") loadAudit();
        });
    });

    // --- Status ---
    async function loadStatus() {
        try {
            const res = await fetch("/api/status");
            if (res.status === 401) return showLogin();
            const data = await res.json();
            const tbody = $("#status-table tbody");
            tbody.innerHTML = "";
            const rows = [
                ["Agent ID", data.agent_id],
                ["Version", data.version],
                ["Server URL", data.server_url],
                ["CDM Enabled", data.cdm_enabled ? "Yes" : "No"],
                ["Session Active", data.session_active ? "Yes" : "No"]
            ];
            rows.forEach(([k, v]) => {
                const tr = document.createElement("tr");
                tr.innerHTML = `<td><strong>${esc(k)}</strong></td><td>${esc(String(v))}</td>`;
                tbody.appendChild(tr);
            });
        } catch (ex) {
            console.error("loadStatus:", ex);
        }
    }

    // --- CDM ---
    async function loadCDM() {
        try {
            const res = await fetch("/api/cdm");
            if (res.status === 401) return showLogin();
            const data = await res.json();
            const status = $("#cdm-status");
            let html = `<strong>CDM:</strong> <span class="status-badge ${data.enabled ? "badge-on" : "badge-off"}">${data.enabled ? "Enabled" : "Disabled"}</span>`;
            if (data.session_active) {
                html += ` <span class="status-badge badge-session">Session Active</span>`;
                if (data.session_expires_at) {
                    html += `<br><small>Expires: ${new Date(data.session_expires_at).toLocaleString()}</small>`;
                }
            }
            status.innerHTML = html;

            $("#cdm-enable-btn").hidden = data.enabled;
            $("#cdm-disable-btn").hidden = !data.enabled;
            $("#cdm-session").hidden = !data.enabled;
            $("#cdm-grant-btn").hidden = data.session_active;
            $("#grant-duration").hidden = data.session_active;
            $("#cdm-revoke-btn").hidden = !data.session_active;
        } catch (ex) {
            console.error("loadCDM:", ex);
        }
    }

    $("#cdm-enable-btn").addEventListener("click", () => cdmAction("/api/cdm/enable"));
    $("#cdm-disable-btn").addEventListener("click", () => cdmAction("/api/cdm/disable"));
    $("#cdm-revoke-btn").addEventListener("click", () => cdmAction("/api/cdm/revoke"));
    $("#cdm-grant-btn").addEventListener("click", () => {
        const dur = $("#grant-duration").value;
        cdmAction("/api/cdm/grant", {duration: dur});
    });

    async function cdmAction(url, body) {
        try {
            const opts = {method: "POST", headers: {"Content-Type": "application/json"}};
            if (body) opts.body = JSON.stringify(body);
            const res = await fetch(url, opts);
            if (res.status === 401) return showLogin();
            if (!res.ok) {
                const data = await res.json();
                alert(data.error || "Action failed");
            }
            loadCDM();
            loadStatus();
        } catch (ex) {
            alert("Error: " + ex.message);
        }
    }

    // --- Audit ---
    async function loadAudit() {
        try {
            const res = await fetch("/api/audit");
            if (res.status === 401) return showLogin();
            const entries = await res.json();
            const tbody = $("#audit-table tbody");
            tbody.innerHTML = "";
            if (!entries || entries.length === 0) {
                tbody.innerHTML = '<tr><td colspan="4">No audit entries</td></tr>';
                return;
            }
            entries.forEach((e) => {
                const tr = document.createElement("tr");
                const details = [];
                if (e.old_state) details.push(e.old_state + " \u2192 " + (e.new_state || ""));
                if (e.duration) details.push("duration: " + e.duration);
                if (e.job_id) details.push("job: " + e.job_id);
                tr.innerHTML = `<td>${esc(new Date(e.timestamp).toLocaleString())}</td><td>${esc(e.action)}</td><td>${esc(e.actor || "")}</td><td>${esc(details.join(", "))}</td>`;
                tbody.appendChild(tr);
            });
        } catch (ex) {
            console.error("loadAudit:", ex);
        }
    }

    function showLogin() {
        dashView.hidden = true;
        loginView.hidden = false;
    }

    function esc(s) {
        const d = document.createElement("div");
        d.textContent = s;
        return d.innerHTML;
    }

    // On load: try to fetch status to check if we have a valid session.
    (async () => {
        try {
            const res = await fetch("/api/status");
            if (res.ok) {
                loginView.hidden = true;
                dashView.hidden = false;
                loadStatus();
            }
        } catch (_) {
            // Not authenticated, show login.
        }
    })();
})();
