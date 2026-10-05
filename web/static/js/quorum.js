/* Quorum progressive enhancement. Everything here is optional:
 * viewing and voting work without JavaScript. Budget: < 15 KB. */
(() => {
	"use strict";

	var lang = document.documentElement.lang || "en";
	var on = document.addEventListener.bind(document);

	function $(sel, root) { return (root || document).querySelector(sel); }
	function $$(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }
	function el(tag, attrs, text) {
		var e = document.createElement(tag);
		for (var k in attrs) e.setAttribute(k, attrs[k]);
		if (text) e.textContent = text;
		return e;
	}
	function attr(n, a) { return n.getAttribute(a); }
	function pad(n) { return (n < 10 ? "0" : "") + n; }
	function dstr(d) { return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()); }
	function load(k, d) { try { return JSON.parse(localStorage.getItem(k)) || d; } catch (e) { return d; } }
	function save(k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch (e) { /* full */ } }

	// copy; plain HTTP has no Clipboard API: selection copy fallback

	on("click", (ev) => {
		var btn = ev.target.closest("[data-copy]");
		var input = btn && document.getElementById(attr(btn, "data-copy"));
		if (!input) return;
		function done() {
			var old = attr(btn, "data-label") || btn.textContent;
			btn.setAttribute("data-label", old);
			btn.textContent = attr(btn, "data-copied") || old;
			clearTimeout(btn._t);
			btn._t = setTimeout(() => { btn.textContent = old; }, 1500);
		}
		function fallback() {
			input.focus(); input.select();
			try { if (document.execCommand("copy")) done(); } catch (e) { /* manual copy */ }
		}
		if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(input.value).then(done, fallback);
		else fallback();
	});

	// native share sheet, touch devices only

	if (navigator.share && window.matchMedia && matchMedia("(pointer: coarse)").matches) {
		$$("[data-share]").forEach((b) => { b.hidden = false; });
	}
	on("click", (ev) => {
		var btn = ev.target.closest("[data-share]");
		var input = btn && document.getElementById(attr(btn, "data-share"));
		if (input) navigator.share({ title: attr(btn, "data-share-title") || document.title, url: input.value }).catch(() => { /* dismissed */ });
	});

	// confirm destructive actions (first: later listeners honour it)

	on("submit", (ev) => {
		var b = ev.submitter;
		if (b && b.hasAttribute("data-confirm") && !confirm(attr(b, "data-confirm"))) ev.preventDefault();
	});

	// warn before leaving a changed guarded form

	function isDirty(form) {
		return Array.prototype.some.call(form.elements, (f) => {
			if (f.type === "radio" || f.type === "checkbox") return f.checked !== f.defaultChecked;
			return (f.type === "text" || f.type === "email" || f.tagName === "TEXTAREA") && !f.readOnly && f.value !== f.defaultValue;
		});
	}
	on("submit", (ev) => {
		if (!ev.defaultPrevented && ev.target.hasAttribute("data-unsaved-guard")) ev.target.setAttribute("data-submitting", "");
	});
	addEventListener("pageshow", () => {
		$$("form[data-submitting]").forEach((f) => { f.removeAttribute("data-submitting"); });
	});
	addEventListener("beforeunload", (ev) => {
		if ($$("form[data-unsaved-guard]:not([data-submitting])").some(isDirty)) { ev.preventDefault(); ev.returnValue = ""; }
	});

	// menus close on outside click or Escape

	on("click", (ev) => {
		$$("details.usermenu[open]").forEach((m) => { if (!m.contains(ev.target)) m.removeAttribute("open"); });
	});
	on("keydown", (ev) => {
		if (ev.key !== "Escape") return;
		$$("details.usermenu[open]").forEach((m) => {
			m.removeAttribute("open");
			var s = $("summary", m);
			if (s) s.focus();
		});
	});

	// three-state control: space cycles

	on("keydown", (ev) => {
		var seg = ev.key === " " && ev.target.type === "radio" && ev.target.closest("[data-seg]");
		if (!seg) return;
		ev.preventDefault();
		var radios = $$("input[type=radio]", seg);
		var next = radios[(radios.indexOf(ev.target) + 1) % radios.length];
		next.checked = true;
		next.focus();
	});

	// switchers apply on change (button kept for no-JS)

	on("change", (ev) => {
		var f = ev.target.closest("form[data-autosubmit]");
		if (f) f.requestSubmit ? f.requestSubmit() : f.submit();
	});

	// timezone

	var browserTZ = "";
	try { browserTZ = Intl.DateTimeFormat().resolvedOptions().timeZone || ""; } catch (e) { /* none */ }

	function ensureOption(select, value) {
		if (value && !$$("option", select).some((o) => o.value === value)) {
			select.insertBefore(el("option", { value: value }, value), select.firstChild);
		}
	}

	// First visit to a poll: switch the grid to the browser zone.
	(() => {
		var select = $("#grid select[data-tz-select]");
		if (!select || !browserTZ || /(^|; )quorum_tz=/.test(document.cookie) || new URLSearchParams(location.search).has("tz")) return;
		document.cookie = "quorum_tz=" + browserTZ + "; path=/; max-age=31536000; samesite=lax";
		if (select.value !== browserTZ) {
			ensureOption(select, browserTZ);
			select.value = browserTZ;
			select.dispatchEvent(new Event("change", { bubbles: true }));
		}
	})();

	// A zone switch re-renders the vote form: carry the ballot over.
	var draft = null;
	on("htmx:beforeSwap", (ev) => {
		var f = ev.detail.target.id === "grid" && $("#vote");
		draft = f ? $$("input", f).filter((i) => i.type !== "hidden").map((i) => {
			return [i.name, i.type === "radio" ? (i.checked ? i.value : null) : i.value];
		}) : null;
	});
	on("htmx:afterSwap", () => {
		var f = draft && $("#vote");
		if (!f) return;
		draft.forEach((d) => {
			$$("input", f).forEach((i) => {
				if (i.name !== d[0]) return;
				if (i.type === "radio") { if (d[1] !== null) i.checked = i.value === d[1]; } else i.value = d[1];
			});
		});
		draft = null;
	});

	// polls run and votes cast from this device

	var POLLS = "quorum.myPolls", VOTES = "quorum.myVotes";

	$$("[data-admin-link]").forEach((n) => {
		var input = $("input", n), url = attr(n, "data-url") || (input && input.value);
		if (!url) return;
		var polls = load(POLLS, []).filter((p) => p.url !== url);
		polls.unshift({ url: url, title: attr(n, "data-title") || document.title, ts: Date.now() });
		save(POLLS, polls.slice(0, 20));
	});

	(() => {
		var section = $("[data-recent-polls]"), polls = load(POLLS, []);
		if (!section || !polls.length) return;
		section.appendChild(el("h2", { "class": "font-display text-lg font-semibold" }, attr(section, "data-heading")));
		var ul = el("ul", { "class": "mt-2 space-y-1 text-sm" });
		polls.forEach((p) => {
			var li = el("li", {});
			li.appendChild(el("a", { href: p.url, "class": "underline" }, p.title));
			ul.appendChild(li);
		});
		section.appendChild(ul);
		section.hidden = false;
	})();

	// Guest edit links are shown once: keep them, offer them back.
	$$("[data-my-vote]").forEach((n) => {
		var v = load(VOTES, {});
		v[attr(n, "data-my-vote")] = { url: attr(n, "data-url"), name: attr(n, "data-name") };
		save(VOTES, v);
	});
	$$("[data-remembered-vote]").forEach((n) => {
		var mine = load(VOTES, {})[attr(n, "data-remembered-vote")];
		if (!mine || mine.url.indexOf("/polls/") !== 0) return;
		n.textContent = attr(n, "data-l10n") + " ";
		n.appendChild(el("a", { href: mine.url, "class": "underline" }, attr(n, "data-l10n-link") + " (" + mine.name + ")"));
		n.hidden = false;
	});
	on("submit", (ev) => {
		var id = !ev.defaultPrevented && attr(ev.target, "data-forget-vote");
		if (!id) return;
		var v = load(VOTES, {});
		delete v[id];
		save(VOTES, v);
	});

	// creation form: calendar + slots

	var form = $("form[data-create-form]");
	if (!form) return;
	var calRoot = $("[data-calendar]", form), slotsRoot = $("[data-slots]", form);
	var inputsRoot = $("[data-option-inputs]", form), tzRow = $("[data-tz-row]", form);
	if (!calRoot || !slotsRoot || !inputsRoot) return;

	var L = (key) => attr(slotsRoot, "data-l10n-" + key) || key;
	var DURATIONS = [15, 30, 45, 60, 90, 120, 180, 240, 480];
	function durLabel(m) {
		var h = Math.floor(m / 60), r = m % 60;
		return h === 0 ? m + " min" : (r === 0 ? h + " h" : h + " h " + pad(r));
	}
	var monthFmt = new Intl.DateTimeFormat(lang, { month: "long", year: "numeric" });
	var wdFmt = new Intl.DateTimeFormat(lang, { weekday: "narrow" });
	var dayFmt = new Intl.DateTimeFormat(lang, { weekday: "long", day: "numeric", month: "long", year: "numeric" });

	var today = new Date(); today.setHours(0, 0, 0, 0);
	var cursor = new Date(today.getFullYear(), today.getMonth(), 1);
	var selected = {}; // date → [{start, dur}]
	var lastSlots = [{ start: "18:00", dur: 60 }];
	function copySlots(list) { return list.map((s) => { return { start: s.start, dur: s.dur }; }); }

	// A refused form brings its options back; else use the browser zone.
	var initial = [];
	try { initial = JSON.parse(attr(form, "data-initial-options") || "[]"); } catch (e) { /* none */ }
	initial.forEach((o) => {
		(selected[o.d] = selected[o.d] || []).push({ start: o.s || "18:00", dur: parseInt(o.m, 10) || 60 });
	});
	if (initial.length) {
		var first = new Date(initial[0].d + "T00:00:00");
		if (!isNaN(first)) cursor = new Date(first.getFullYear(), first.getMonth(), 1);
	} else if (browserTZ) {
		$$("select[data-tz-select]", form).forEach((s) => { ensureOption(s, browserTZ); s.value = browserTZ; });
	}

	function mode() {
		var c = $("input[name=kind]:checked", form);
		return c ? c.value : "timed";
	}
	function selectedDates() { return Object.keys(selected).sort(); }
	function focusDay(ds) {
		var b = $('.cal-day[data-date="' + ds + '"]', calRoot);
		if (b && !b.disabled) b.focus();
	}
	function month(y, m) { cursor = new Date(y, m, 1); renderCalendar(); }

	function renderCalendar() {
		calRoot.textContent = "";
		var head = el("div", { "class": "cal-head" });
		var prev = el("button", { type: "button", "class": "cal-nav", "aria-label": L("prev") }, "‹");
		var next = el("button", { type: "button", "class": "cal-nav", "aria-label": L("next") }, "›");
		prev.onclick = () => { month(cursor.getFullYear(), cursor.getMonth() - 1); };
		next.onclick = () => { month(cursor.getFullYear(), cursor.getMonth() + 1); };
		head.appendChild(prev);
		head.appendChild(el("span", { "class": "cal-title", "aria-live": "polite" }, monthFmt.format(cursor)));
		head.appendChild(next);
		calRoot.appendChild(head);

		var grid = el("div", { "class": "cal-grid" });
		for (var w = 0; w < 7; w++) { // Monday first; 2024-01-01 is one
			grid.appendChild(el("span", { "class": "cal-wd", "aria-hidden": "true" }, wdFmt.format(new Date(2024, 0, 1 + w))));
		}
		var lead = (new Date(cursor.getFullYear(), cursor.getMonth(), 1).getDay() + 6) % 7;
		for (var i = 0; i < lead; i++) grid.appendChild(el("span", {}));
		var days = new Date(cursor.getFullYear(), cursor.getMonth() + 1, 0).getDate();
		for (var d = 1; d <= days; d++) {
			var date = new Date(cursor.getFullYear(), cursor.getMonth(), d), ds = dstr(date);
			var btn = el("button", {
				type: "button", "class": "cal-day" + (selected[ds] ? " sel" : ""), "data-date": ds,
				"aria-pressed": selected[ds] ? "true" : "false", "aria-label": dayFmt.format(date)
			}, String(d));
			btn.disabled = date < today;
			grid.appendChild(btn);
		}
		calRoot.appendChild(grid);
	}

	calRoot.addEventListener("click", (ev) => {
		var b = ev.target.closest(".cal-day");
		if (!b) return;
		var ds = attr(b, "data-date");
		if (selected[ds]) delete selected[ds]; else selected[ds] = copySlots(lastSlots);
		refresh();
		focusDay(ds); // keep keyboard focus across the re-render
	});
	// Arrow keys walk the days, across months.
	calRoot.addEventListener("keydown", (ev) => {
		var step = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }[ev.key];
		var b = step && ev.target.closest(".cal-day");
		if (!b) return;
		ev.preventDefault();
		var t = new Date(attr(b, "data-date") + "T00:00:00");
		t.setDate(t.getDate() + step);
		if (t < today) return;
		if (t.getMonth() !== cursor.getMonth()) month(t.getFullYear(), t.getMonth());
		focusDay(dstr(t));
	});

	function slotRow(ds, slot, idx) {
		var row = el("div", { "class": "slot-row" });
		var start = el("input", { type: "time", "class": "field", value: slot.start, "aria-label": L("start") });
		start.onchange = () => { slot.start = start.value || slot.start; lastSlots = selected[ds]; syncInputs(); };
		var dur = el("select", { "class": "field", "aria-label": L("duration") });
		DURATIONS.forEach((m) => {
			var o = el("option", { value: String(m) }, durLabel(m));
			o.selected = m === slot.dur;
			dur.appendChild(o);
		});
		dur.onchange = () => { slot.dur = parseInt(dur.value, 10); lastSlots = selected[ds]; syncInputs(); };
		var rm = el("button", { type: "button", "class": "btn-link text-xs" }, L("remove"));
		rm.onclick = () => {
			selected[ds].splice(idx, 1);
			if (!selected[ds].length) delete selected[ds];
			refresh();
		};
		row.appendChild(start); row.appendChild(dur); row.appendChild(rm);
		return row;
	}

	function renderSlots() {
		slotsRoot.textContent = "";
		if (mode() !== "timed") return;
		var dates = selectedDates(), short = new Intl.DateTimeFormat(lang, { weekday: "short", day: "numeric", month: "short" });
		dates.forEach((ds, di) => {
			var box = el("div", { "class": "slot-day" });
			box.appendChild(el("p", { "class": "slot-date" }, short.format(new Date(ds + "T00:00:00"))));
			selected[ds].forEach((slot, i) => { box.appendChild(slotRow(ds, slot, i)); });
			var add = el("button", { type: "button", "class": "btn-link text-xs" }, "+ " + L("add"));
			add.onclick = () => {
				var prev = selected[ds][selected[ds].length - 1];
				selected[ds].push({ start: prev ? prev.start : "18:00", dur: prev ? prev.dur : 60 });
				refresh();
			};
			box.appendChild(add);
			if (di === 0 && dates.length > 1) {
				var copy = el("button", { type: "button", "class": "btn-link text-xs" }, L("copy"));
				copy.onclick = () => {
					dates.slice(1).forEach((o) => { selected[o] = copySlots(selected[ds]); });
					refresh();
				};
				box.appendChild(copy);
			}
			slotsRoot.appendChild(box);
		});
	}

	function hidden(name, value) { inputsRoot.appendChild(el("input", { type: "hidden", name: name, value: value })); }
	function syncInputs() {
		inputsRoot.textContent = "";
		selectedDates().forEach((ds) => {
			if (mode() === "allday") return hidden("option_date", ds);
			selected[ds].forEach((s) => {
				hidden("option_date", ds); hidden("option_start", s.start); hidden("option_duration", String(s.dur));
			});
		});
	}

	var submitBtn = $('button[type="submit"]', form);
	function refresh() {
		var timed = mode() === "timed";
		if (tzRow) tzRow.hidden = !timed;
		slotsRoot.hidden = !timed;
		renderCalendar();
		renderSlots();
		syncInputs();
		if (submitBtn) submitBtn.disabled = selectedDates().length === 0;
	}

	$$("input[name=kind]", form).forEach((r) => { r.addEventListener("change", refresh); });
	calRoot.hidden = false;
	$$("[data-js-only]", form).forEach((n) => { n.hidden = false; });
	refresh();
})();
