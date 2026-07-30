/* Quorum progressive enhancement. Everything here is optional:
 * viewing and voting work without JavaScript. Budget: < 15 KB. */
(function () {
	"use strict";

	var lang = document.documentElement.lang || "en";

	/* ---------- tiny helpers ---------- */

	function $(sel, root) { return (root || document).querySelector(sel); }
	function $$(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }
	function el(tag, attrs, text) {
		var e = document.createElement(tag);
		for (var k in attrs) { if (attrs[k] !== null) e.setAttribute(k, attrs[k]); }
		if (text) e.textContent = text;
		return e;
	}
	function pad(n) { return (n < 10 ? "0" : "") + n; }
	function dstr(d) { return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()); }

	/* ---------- copy buttons ---------- */

	document.addEventListener("click", function (ev) {
		var btn = ev.target.closest("[data-copy]");
		if (!btn) return;
		var input = document.getElementById(btn.getAttribute("data-copy"));
		if (!input) return;
		navigator.clipboard.writeText(input.value).then(function () {
			var old = btn.textContent;
			btn.textContent = btn.getAttribute("data-copied") || old;
			setTimeout(function () { btn.textContent = old; }, 1500);
		});
	});

	/* ---------- confirm on destructive forms ---------- */

	document.addEventListener("submit", function (ev) {
		var btn = ev.submitter;
		if (btn && btn.hasAttribute("data-confirm") && !window.confirm(btn.getAttribute("data-confirm"))) {
			ev.preventDefault();
		}
	});

	/* ---------- dropdown menus (theme, user): close on outside
	 * click or Escape — native <details> only closes on re-click. ---------- */

	document.addEventListener("click", function (ev) {
		$$("details.usermenu[open]").forEach(function (menu) {
			if (!menu.contains(ev.target)) menu.removeAttribute("open");
		});
	});
	document.addEventListener("keydown", function (ev) {
		if (ev.key !== "Escape") return;
		$$("details.usermenu[open]").forEach(function (menu) {
			menu.removeAttribute("open");
			var s = menu.querySelector("summary");
			if (s) s.focus();
		});
	});

	/* ---------- three-state control: space cycles ---------- */

	document.addEventListener("keydown", function (ev) {
		if (ev.key !== " ") return;
		var seg = ev.target.closest("[data-seg]");
		if (!seg || ev.target.type !== "radio") return;
		ev.preventDefault();
		var radios = $$("input[type=radio]", seg);
		var idx = radios.indexOf(ev.target);
		var next = radios[(idx + 1) % radios.length];
		next.checked = true;
		next.focus();
	});

	/* ---------- timezone ---------- */

	var browserTZ = "";
	try { browserTZ = Intl.DateTimeFormat().resolvedOptions().timeZone || ""; } catch (e) { /* keep empty */ }

	function ensureOption(select, value) {
		if (!value) return;
		for (var i = 0; i < select.options.length; i++) {
			if (select.options[i].value === value) return;
		}
		select.insertBefore(el("option", { value: value }, value), select.firstChild);
	}

	// Creation form: preselect the browser zone.
	$$("form[data-create-form] select[data-tz-select]").forEach(function (select) {
		if (browserTZ) { ensureOption(select, browserTZ); select.value = browserTZ; }
	});

	// Poll page: first visit (no cookie, no explicit ?tz=) → switch the
	// grid to the browser zone through the regular htmx path.
	(function () {
		var select = $("#grid select[data-tz-select]");
		if (!select || !browserTZ) return;
		if (/(^|; )quorum_tz=/.test(document.cookie)) return;
		if (new URLSearchParams(location.search).has("tz")) return;
		document.cookie = "quorum_tz=" + browserTZ + "; path=/; max-age=31536000; samesite=lax";
		if (select.value !== browserTZ) {
			ensureOption(select, browserTZ);
			select.value = browserTZ;
			select.dispatchEvent(new Event("change", { bubbles: true }));
		}
	})();

	/* ---------- recent polls on this device ---------- */

	var STORE_KEY = "quorum.myPolls";

	function readPolls() {
		try { return JSON.parse(localStorage.getItem(STORE_KEY)) || []; } catch (e) { return []; }
	}

	$$("[data-admin-link]").forEach(function (node) {
		var url = node.getAttribute("data-url") || ($("input", node) && $("input", node).value);
		if (!url) return;
		var polls = readPolls().filter(function (p) { return p.url !== url; });
		polls.unshift({ url: url, title: node.getAttribute("data-title") || document.title, ts: Date.now() });
		try { localStorage.setItem(STORE_KEY, JSON.stringify(polls.slice(0, 20))); } catch (e) { /* full */ }
	});

	(function () {
		var section = $("[data-recent-polls]");
		if (!section) return;
		var polls = readPolls();
		if (!polls.length) return;
		section.appendChild(el("h2", { "class": "font-display text-lg font-semibold" }, section.getAttribute("data-heading")));
		var ul = el("ul", { "class": "mt-2 space-y-1 text-sm" });
		polls.forEach(function (p) {
			var li = el("li", {});
			li.appendChild(el("a", { href: p.url, "class": "underline" }, p.title));
			ul.appendChild(li);
		});
		section.appendChild(ul);
		section.hidden = false;
		section.classList.remove("hidden");
		section.classList.add("border-t", "border-ink/15", "pt-6", "pb-8");
	})();

	/* ---------- creation form: calendar + slots ---------- */

	var form = $("form[data-create-form]");
	if (!form) return;

	var calRoot = $("[data-calendar]", form);
	var slotsRoot = $("[data-slots]", form);
	var inputsRoot = $("[data-option-inputs]", form);
	var tzRow = $("[data-tz-row]", form);
	if (!calRoot || !slotsRoot || !inputsRoot) return;

	var L = function (key) { return slotsRoot.getAttribute("data-l10n-" + key) || key; };
	var DURATIONS = [15, 30, 45, 60, 90, 120, 180, 240, 480];
	function durLabel(m) {
		var h = Math.floor(m / 60), r = m % 60;
		return h === 0 ? m + " min" : (r === 0 ? h + " h" : h + " h " + pad(r));
	}

	var monthFmt = new Intl.DateTimeFormat(lang, { month: "long", year: "numeric" });
	var wdFmt = new Intl.DateTimeFormat(lang, { weekday: "narrow" });

	var today = new Date(); today.setHours(0, 0, 0, 0);
	var cursor = new Date(today.getFullYear(), today.getMonth(), 1);
	var selected = {};              // dateStr → array of {start, dur} (timed mode)
	var lastSlots = [{ start: "18:00", dur: 60 }];

	function mode() {
		var checked = form.querySelector("input[name=kind]:checked");
		return checked ? checked.value : "timed";
	}

	function selectedDates() { return Object.keys(selected).sort(); }

	function renderCalendar() {
		calRoot.textContent = "";
		var head = el("div", { "class": "cal-head" });
		var prev = el("button", { type: "button", "class": "cal-nav", "aria-label": "previous month" }, "‹");
		var next = el("button", { type: "button", "class": "cal-nav", "aria-label": "next month" }, "›");
		prev.onclick = function () { cursor = new Date(cursor.getFullYear(), cursor.getMonth() - 1, 1); renderCalendar(); };
		next.onclick = function () { cursor = new Date(cursor.getFullYear(), cursor.getMonth() + 1, 1); renderCalendar(); };
		head.appendChild(prev);
		head.appendChild(el("span", { "class": "cal-title" }, monthFmt.format(cursor)));
		head.appendChild(next);
		calRoot.appendChild(head);

		var grid = el("div", { "class": "cal-grid" });
		var monday = 1; // week starts Monday
		for (var w = 0; w < 7; w++) {
			var day = new Date(2024, 0, monday + w); // 2024-01-01 is a Monday
			grid.appendChild(el("span", { "class": "cal-wd" }, wdFmt.format(day)));
		}
		var first = new Date(cursor.getFullYear(), cursor.getMonth(), 1);
		var lead = (first.getDay() + 6) % 7; // Monday-indexed offset
		for (var i = 0; i < lead; i++) grid.appendChild(el("span", {}));
		var daysInMonth = new Date(cursor.getFullYear(), cursor.getMonth() + 1, 0).getDate();
		for (var d = 1; d <= daysInMonth; d++) {
			(function (d) {
				var date = new Date(cursor.getFullYear(), cursor.getMonth(), d);
				var ds = dstr(date);
				var btn = el("button", { type: "button", "class": "cal-day" }, String(d));
				if (date < today) btn.disabled = true;
				if (selected[ds]) btn.classList.add("sel");
				btn.setAttribute("aria-pressed", selected[ds] ? "true" : "false");
				btn.onclick = function () {
					if (selected[ds]) {
						delete selected[ds];
					} else {
						selected[ds] = lastSlots.map(function (s) { return { start: s.start, dur: s.dur }; });
					}
					refresh();
				};
				grid.appendChild(btn);
			})(d);
		}
		calRoot.appendChild(grid);
	}

	function slotRow(ds, slot, idx) {
		var row = el("div", { "class": "slot-row" });
		var start = el("input", { type: "time", "class": "field", value: slot.start, "aria-label": L("start") });
		start.onchange = function () { slot.start = start.value || slot.start; lastSlots = selected[ds]; syncInputs(); };
		var dur = el("select", { "class": "field", "aria-label": L("duration") });
		DURATIONS.forEach(function (m) {
			var o = el("option", { value: String(m) }, durLabel(m));
			if (m === slot.dur) o.selected = true;
			dur.appendChild(o);
		});
		dur.onchange = function () { slot.dur = parseInt(dur.value, 10); lastSlots = selected[ds]; syncInputs(); };
		var rm = el("button", { type: "button", "class": "btn-link text-xs" }, L("remove"));
		rm.onclick = function () {
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
		var dates = selectedDates();
		dates.forEach(function (ds, di) {
			var box = el("div", { "class": "slot-day" });
			var d = new Date(ds + "T00:00:00");
			box.appendChild(el("p", { "class": "slot-date" },
				new Intl.DateTimeFormat(lang, { weekday: "short", day: "numeric", month: "short" }).format(d)));
			selected[ds].forEach(function (slot, i) { box.appendChild(slotRow(ds, slot, i)); });
			var add = el("button", { type: "button", "class": "btn-link text-xs" }, "+ " + L("add"));
			add.onclick = function () {
				var prev = selected[ds][selected[ds].length - 1];
				selected[ds].push({ start: prev ? prev.start : "18:00", dur: prev ? prev.dur : 60 });
				refresh();
			};
			box.appendChild(add);
			if (di === 0 && dates.length > 1) {
				var copy = el("button", { type: "button", "class": "btn-link text-xs" }, L("copy"));
				copy.onclick = function () {
					dates.slice(1).forEach(function (other) {
						selected[other] = selected[ds].map(function (s) { return { start: s.start, dur: s.dur }; });
					});
					refresh();
				};
				box.appendChild(copy);
			}
			slotsRoot.appendChild(box);
		});
	}

	function syncInputs() {
		inputsRoot.textContent = "";
		selectedDates().forEach(function (ds) {
			if (mode() === "allday") {
				inputsRoot.appendChild(el("input", { type: "hidden", name: "option_date", value: ds }));
				return;
			}
			selected[ds].forEach(function (slot) {
				inputsRoot.appendChild(el("input", { type: "hidden", name: "option_date", value: ds }));
				inputsRoot.appendChild(el("input", { type: "hidden", name: "option_start", value: slot.start }));
				inputsRoot.appendChild(el("input", { type: "hidden", name: "option_duration", value: String(slot.dur) }));
			});
		});
	}

	var submitBtn = form.querySelector('button[type="submit"]');

	function refresh() {
		var timed = mode() === "timed";
		if (tzRow) tzRow.hidden = !timed;
		slotsRoot.hidden = !timed;
		renderCalendar();
		renderSlots();
		syncInputs();
		// No date picked yet → nothing to create; the calendar hint
		// above explains what to do.
		if (submitBtn) submitBtn.disabled = selectedDates().length === 0;
	}

	$$("input[name=kind]", form).forEach(function (radio) {
		radio.addEventListener("change", refresh);
	});

	calRoot.hidden = false;
	$$("[data-js-only]", form).forEach(function (n) { n.hidden = false; });
	refresh();
})();
