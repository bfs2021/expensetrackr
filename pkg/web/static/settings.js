// Settings page (/settings): the signed-in user's own preferences, read and
// written through GET/PUT /api/users/:uid/preferences. The uid comes from
// /api/auth/me and is only used in the request URL, never shown.
(function () {
  "use strict";
  const { $, clearErrors, showFieldErrors } = App;
  const form = $("#settings-form");
  const sel = $("#s-base-currency");
  let path = null; // /api/users/<uid>/preferences of the signed-in user
  let gen = 0;

  function setStatus(text, kind) { App.setStatus($("#s-form-status"), text, kind); }

  async function enter() {
    const my = ++gen;
    clearErrors(form);
    setStatus("");
    form.hidden = true;
    try {
      const me = await fetch("/api/auth/me").then((r) => (r.ok ? r.json() : Promise.reject(new Error(`status ${r.status}`))));
      path = `/api/users/${encodeURIComponent(me.uid)}/preferences`;
      const [res] = await Promise.all([fetch(path), App.fillCurrencySelect(sel)]);
      const prefs = await res.json().catch(() => ({}));
      if (my !== gen) return;
      if (!res.ok) {
        setStatus(prefs.error || `Could not load settings (${res.status})`, "bad");
        return;
      }
      App.setCurrency(sel, prefs.base_currency);
      form.hidden = false;
    } catch (err) {
      if (my === gen) setStatus("Could not load settings: " + err.message, "bad");
    }
  }

  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    clearErrors(form);
    if (!path) return;
    const btn = $("#s-submit-btn");
    btn.disabled = true;
    setStatus("Saving…");
    try {
      const res = await fetch(path, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ base_currency: sel.value }),
      });
      const body = await res.json().catch(() => ({}));
      if (res.ok) {
        App.setCurrency(sel, body.base_currency);
        setStatus("Settings saved.", "ok");
      } else {
        showFieldErrors(form, body.fields);
        setStatus(body.error || `Could not save (${res.status})`, "bad");
      }
    } catch (err) {
      setStatus("Network error: " + err.message, "bad");
    } finally {
      btn.disabled = false;
    }
  });

  Combobox.enhance(sel);
  App.route("/settings", { view: "#view-settings", section: "settings", title: "Settings", enter });
})();
