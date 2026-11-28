// Scope for the desktop app: workers grouped under their chief tag, rows in a
// stable order that no click or status change can move, and short worker names.
//
// This is a port of this repo's Go domain layer, not a re-invention of it:
//   ChiefTag / WorkerName      internal/domain/tag.go
//   rankStable / compareActive internal/domain/order.go
//   tag-family blocks          internal/domain/sidebar.go (block, newGroup)
// When a rule changes there, mirror it here; the Go tests pin both.

import { View, div, svg } from "gpui-kit";
import { v_flex, h_flex } from "gpui-base";
import { agents, spaces, theme, settings, run, shared, setShared, setAgentOrder, openOverlay, closeOverlay, props, checking, home, exec, readTextFile, nextCommand } from "herdr";

function isDarkAppearance(t) {
  const s = settings();
  if (typeof s?.dark === "boolean") return s.dark;
  const hex = String(t?.chrome ?? "").replace("#", "");
  if (hex.length >= 6) {
    const r = parseInt(hex.slice(0, 2), 16);
    const g = parseInt(hex.slice(2, 4), 16);
    const b = parseInt(hex.slice(4, 6), 16);
    return (0.299 * r + 0.587 * g + 0.114 * b) < 128;
  }
  return true;
}

function bunnyPalette(t) {
  return isDarkAppearance(t)
    ? { identity: "#38bdf8", title: "#93c5fd", ok: "#4ade80", caution: "#facc15", danger: "#f87171", accent2: "#a78bfa" }
    : { identity: "#0369a1", title: "#1d4ed8", ok: "#15803d", caution: "#b45309", danger: "#b91c1c", accent2: "#6d28d9" };
}

function scopeStateColor(state, p) {
  const s = String(state ?? "").toLowerCase();
  if (s === "running" || s === "working" || s === "active" || s === "pending") return p.caution;
  if (s === "done" || s === "merged" || s === "completed" || s === "passed") return p.ok;
  if (s === "failed" || s === "error" || s === "blocked" || s === "killed") return p.danger;
  return p.accent2;
}

// --- internal/domain/tag.go:ChiefTag — the first dot-delimited segment names
// the group; no dot or a leading dot names none; capped at 16 code points so a
// header stays a short id, whatever git says about the workspace.
const CHIEF_TAG_MAX = 16;
function chiefTag(label) {
  const s = label ?? "";
  const i = s.indexOf(".");
  if (i < 1) return "";
  const runes = Array.from(s.slice(0, i));
  return runes.length > CHIEF_TAG_MAX ? runes.slice(0, CHIEF_TAG_MAX).join("") : runes.join("");
}

// --- internal/domain/tag.go:WorkerName — the last dot-delimited segment, so
// `<chief>.<worker>` and the legacy `<chief>.<repo>.<worker>` both read as the
// bare worker. Empty when the label names no worker.
function workerName(label) {
  const s = label ?? "";
  const i = s.lastIndexOf(".");
  return i < 0 ? "" : s.slice(i + 1);
}

// --- internal/domain/order.go:layout keys. Rows sort descending (the host
// rule), so the earliest position gets the largest string. Constants verbatim.
const NO_STAMP = "000000000000";
const LAYOUT_CEIL = 999999999999;
const layoutKey = (i) => String(LAYOUT_CEIL - i).padStart(NO_STAMP.length, "0");
const numberKey = (n) => (n <= 0 ? NO_STAMP : layoutKey(n - 1));

// --- internal/domain/order.go:rankStable. A family is the chief tag of the
// label ("tag:<chief>") or the space itself; it stands where its eldest member
// was created (the largest numberKey = the smallest number). No activity, no
// status and no focus enters a key, so nothing that happens while you watch a
// row can move it.
function rankSpaceKeys(entries, numbers, labelOf) {
  const own = new Map();
  const seen = [];
  for (const e of entries) {
    const ws = e.space;
    if (!ws || own.has(ws)) continue;
    own.set(ws, numberKey(numbers.get(ws) ?? 0));
    seen.push(ws);
  }
  const fam = new Map();
  for (const ws of seen) {
    const tag = chiefTag(labelOf(ws));
    fam.set(ws, tag ? "tag:" + tag : ws);
  }
  const familyKey = new Map();
  for (const ws of seen) {
    const f = fam.get(ws);
    if ((own.get(ws) ?? "") > (familyKey.get(f) ?? "")) familyKey.set(f, own.get(ws));
  }
  const workspace = new Map();
  for (const ws of seen) {
    const f = fam.get(ws);
    const depth = f === ws ? "1" : "0";
    workspace.set(ws, `${familyKey.get(f)}-${f}-${depth}-${own.get(ws)}-${ws}`);
  }
  return { workspace, fam };
}

// --- internal/domain/order.go:rankStable tab/pane half: Herdr lists agents in
// layout order, so a tab's first appearance is its place in the tab bar and a
// pane's its place in the split; the pane the others were split from comes
// first inside a tab.
function rankLayoutKeys(entries) {
  const tabs = new Map();
  const sizes = new Map();
  const tab = new Map();
  const pane = new Map();
  entries.forEach((e, i) => {
    if (!e.tab) {
      pane.set(e.address, layoutKey(i));
      return;
    }
    if (!tabs.has(e.tab)) tabs.set(e.tab, layoutKey(tabs.size));
    const key = layoutKey(sizes.get(e.tab) ?? 0);
    sizes.set(e.tab, (sizes.get(e.tab) ?? 0) + 1);
    const head = sizes.get(e.tab) === 1 ? "1" : "0";
    tab.set(e.address, `${tabs.get(e.tab)}-${e.tab}-${head}-${key}`);
    pane.set(e.address, key);
  });
  return { tab, pane };
}

// --- internal/domain/order.go:compareActive, missing last, descending.
// Array.prototype.sort is stable (ES2019), mirroring sort.SliceStable.
function cmpDesc(a, b) {
  if (a === b) return 0;
  if (!a) return 1;
  if (!b) return -1;
  return a > b ? -1 : 1;
}

// --- internal/domain/sidebar.go:rowBuilder.block — the run a row joins: its
// chief tag's family, or its own space. One tag, one header, however many
// spaces carry it.
function groupsOf(ordered, fam) {
  const groups = [];
  for (const e of ordered) {
    const id = fam.get(e.space) ?? e.space;
    const last = groups[groups.length - 1];
    if (last && last.id === id) last.members.push(e);
    else groups.push({ id, tag: id.startsWith("tag:") ? id.slice(4) : "", members: [e] });
  }
  return groups;
}

// Drops what an agent puts before its title (status glyph, spinner, Codex's
// mark) — same rule as mods/agents/main.js, so rows read alike.
const clean = (s) => (s ?? "").replace(/^(?:\[ [!.] \] |[✋⏳]\uFE0F?(?=\s)|(?!(?:\p{Emoji}\uFE0F|[#*]\u20E3))[^\p{L}\p{N}\p{Ps}\p{Pi}\p{Emoji_Presentation}])+/u, "").trim();

const COLLAPSED_KEY = "scope.collapsed";
const STATE_SORT_RANK = { working: 0, blocked: 1, idle: 2, done: 3 };

// The two orders the panel offers, in the Agents idiom: the header icon shows
// the current one and a click pops up the choice. `created` is the ported Go
// rule (rows never move); `state` orders each group working → blocked → idle
// → done. Kept in shared state like `agents.sort`: persisted, and every mod
// re-renders when it changes.
const SORTS = [
  { id: "created", label: "Created (stable)", icon: "layers" },
  { id: "state", label: "State (rows move)", icon: "clock" },
];
const sortOf = () => SORTS.find((s) => s.id === shared("scope.sort")) ?? SORTS[0];



function loadStoredSet(key) {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return new Set();
    const parsed = JSON.parse(raw);
    return new Set(Array.isArray(parsed) ? parsed.filter((x) => typeof x === "string") : []);
  } catch {
    return new Set();
  }
}

function saveStoredSet(key, set) {
  try {
    localStorage.setItem(key, JSON.stringify([...set]));
  } catch {}
}

// The Bunny run ledger: one JSON per finished-or-live run at
// <FSP>/bunny-chief-of-staff/reports/orchestration-runs/<runId>.json, with
// chief, workspaceLabel (`<chief>.<worker>`), machine, and workers[] each
// carrying workerName, state, runtime, terminalId. It is an enrichment: absent
// or unreadable, grouping still comes from the live space labels, rows just
// lose the ledger chip.
function ledgerDir() {
  const p = props()?.ledger;
  if (p === false || p === "") return null;
  if (typeof p === "string") return p.replace(/[\\/]+$/, "");
  const s = shared("scope.ledger");
  if (typeof s === "string") return s.replace(/[\\/]+$/, "");
  return [home(), "hungquangnguyen", "FSP", "bunny-chief-of-staff", "reports", "orchestration-runs"].join("\\");
}

async function loadLedger(dir) {
  if (!dir) return { dir: null, byLabel: new Map(), byTerminal: new Map(), missing: false };
  let names = [];
  try {
    // `dir /o-d` lists newest first: the runs still on screen are the young ones.
    const res = await exec("cmd", ["/c", "dir", "/b", "/o-d", dir], { timeout: 10000 });
    if (res.code === 0) names = res.stdout.split(/\r?\n/).filter((n) => n.toLowerCase().endsWith(".json")).slice(0, 80);
  } catch {
    names = [];
  }
  const byLabel = new Map();
  const byTerminal = new Map();
  for (const n of names) {
    let text = null;
    try {
      text = await readTextFile(`${dir}\\${n}`);
    } catch {
      continue;
    }
    if (!text) continue;
    let j;
    try {
      j = JSON.parse(text);
    } catch {
      continue;
    }
    if (!j || typeof j !== "object") continue;
    const label = typeof j.workspaceLabel === "string" ? j.workspaceLabel : "";
    const chief = j.chief || chiefTag(label);
    const machine = j.machine || null;
    const workers = Array.isArray(j.workers) ? j.workers : [];
    for (const w of workers) {
      const rec = {
        runId: w.runId || j.runId, chief, worker: w.workerName || j.workerName, label,
        state: w.state || null, runtime: w.runtime || null, machine,
      };
      if (rec.terminalId && !byTerminal.has(String(rec.terminalId).toLowerCase())) byTerminal.set(String(rec.terminalId).toLowerCase(), rec);
      if (label && !byLabel.has(label)) byLabel.set(label, rec);
    }
    if (!workers.length && j.workerName && label && !byLabel.has(label)) {
      byLabel.set(label, { runId: j.runId, chief, worker: j.workerName, label, state: null, runtime: null, machine });
    }
  }
  return { dir, byLabel, byTerminal, missing: names.length === 0 };
}

export default class Scope extends View {
  init(_props, cx) {
    if (props()?.sortMenu) return;
    this.ledger = null;
    this.collapsed = loadStoredSet(COLLAPSED_KEY);
    this.groupIds = [];
    this.activeGroupId = null;
    this.closed = false;
    // `herdr-app check` runs init for real and renders once: no process reads,
    // no timers there.
    if (checking()) return;
    const refresh = Math.max(5000, Number(props()?.refresh) || 30000);
    const reload = (cx) => loadLedger(ledgerDir()).then((l) => { this.ledger = l; cx.notify(); }, () => { this.ledger = { dir: ledgerDir(), byLabel: new Map(), byTerminal: new Map(), missing: true }; });
    reload(cx);
    cx.timer.every(refresh, reload);
    cx.spawn(async (cx) => {
      for (;;) {
        const cmd = await nextCommand();
        if (this.closed) return;
        this.handleCommand(cmd, cx);
      }
    });
  }

  close() {
    this.closed = true;
  }

  // The popup the header sort icon opens, in the `agents` idiom: the host
  // renders this mod again with `props().sortMenu` and the overlay's own
  // `props().overlay`; a choice writes `scope.sort` and closes itself.
  sortMenu(t) {
    const on = sortOf().id;
    return v_flex()
      .w_full()
      .p(5)
      .bg(t.popover)
      .text_size(12.8)
      .child(div().px(8).pt(4).pb(6).text_size(11).text_color(t.dim).child("Sort rows"))
      .children(SORTS.map((s) => h_flex()
        .id(`sort-${s.id}`)
        .h(31)
        .px(8)
        .gap(9)
        .rounded(7)
        .items_center()
        .text_color(t.foreground)
        .hover((el) => el.bg(t.hover))
        .on_click(() => {
          setShared("scope.sort", s.id);
          closeOverlay(props().overlay);
        })
        .child(svg(`mods/workspace-chrome/${s.icon}.svg`).size(14).flex_none().text_color(t.muted))
        .child(div().flex_1().child(s.label))
        .when(s.id === on, (el) => el.child(svg("mods/workspace-chrome/check.svg").size(14).flex_none().text_color(t.foreground)))));
  }

  toggleCollapsed(groupId) {
    if (!groupId) return;
    if (this.collapsed.has(groupId)) this.collapsed.delete(groupId);
    else this.collapsed.add(groupId);
    saveStoredSet(COLLAPSED_KEY, this.collapsed);
  }

  handleCommand(cmd, cx) {
    const target = this.activeGroupId || this.groupIds[0];
    if (cmd === "toggle-collapse") this.toggleCollapsed(target);
    else if (cmd === "toggle-sort") setShared("scope.sort", sortOf().id === "state" ? "created" : "state");
    else if (cmd === "collapse-all") {
      for (const id of this.groupIds) this.collapsed.add(id);
      saveStoredSet(COLLAPSED_KEY, this.collapsed);
    } else if (cmd === "expand-all") {
      this.collapsed.clear();
      saveStoredSet(COLLAPSED_KEY, this.collapsed);
    } else if (cmd === "sort-created") {
      setShared("scope.sort", "created");
    } else if (cmd === "sort-state") {
      setShared("scope.sort", "state");
    } else {
      return;
    }
    cx.notify();
  }

  render() {
    const t = theme();
    if (props()?.sortMenu) return this.sortMenu(t);
    const list = (agents() ?? []).filter((a) => a.space);

    // The space label: the app's space name first (Bunny creates worker spaces
    // `--label <chief>.<worker>`), the ledger's label where the name lost it,
    // so grouping follows the same rule the Go plugin reads.
    const led = this.ledger;
    const recByAddress = new Map();
    for (const a of list) {
      if (!led) continue;
      const tail = String(a.address).split("/").pop().toLowerCase();
      const rec = led.byTerminal.get(tail) || led.byLabel.get(a.space_name);
      if (rec) recByAddress.set(a.address, rec);
    }
    const labelBySpace = new Map();
    for (const a of list) {
      if (labelBySpace.has(a.space)) continue;
      const rec = recByAddress.get(a.address);
      labelBySpace.set(a.space, a.space_name || rec?.label || "");
    }
    const labelOf = (ws) => labelBySpace.get(ws) || "";

    // Space creation order: the app's own space index; an agent whose space
    // the list does not name falls back to its first appearance, which is
    // that order (Go does the same: cmp.Or(Number, i+1)).
    const numbers = new Map((spaces() ?? []).map((sp, i) => [sp.id, (sp.index ?? i) + 1]));
    list.forEach((a, i) => {
      if (!a.space || numbers.has(a.space)) return;
      numbers.set(a.space, i + 1);
    });

    const rs = rankSpaceKeys(list, numbers, labelOf);
    const rl = rankLayoutKeys(list);
    const ordered = [...list].sort((x, y) =>
      cmpDesc(rs.workspace.get(x.space), rs.workspace.get(y.space)) ||
      cmpDesc(rl.tab.get(x.address), rl.tab.get(y.address)) ||
      cmpDesc(rl.pane.get(x.address), rl.pane.get(y.address)));

    const groups = groupsOf(ordered, rs.fam);
    this.groupIds = groups.map((g) => g.id);
    this.activeGroupId = groups.find((g) => g.members.some((a) => a.focused))?.id ?? null;
    const rowName = (a, g) => {
      const label = labelOf(a.space) || a.space_name;
      // The short name: a tagged row shows its bare worker, the way the Go
      // plugin publishes run_worker; an untagged row shows its telling title.
      return g.tag ? (workerName(label) || clean(a.title) || a.program?.name || a.address)
        : (clean(a.tab_name) || clean(a.title) || a.program?.name || a.address);
    };
    const displayMembers = (g) => {
      if (sortOf().id !== "state") return g.members;
      return [...g.members].sort((x, y) => {
        const rx = STATE_SORT_RANK[x.status] ?? 4;
        const ry = STATE_SORT_RANK[y.status] ?? 4;
        if (rx !== ry) return rx - ry;
        const nx = rowName(x, g).toLowerCase();
        const ny = rowName(y, g).toLowerCase();
        return nx === ny ? 0 : nx < ny ? -1 : 1;
      });
    };

    // Keep all rows (including collapsed groups) in panel order so agent.next/prev/select
    // never fall back to appending unlisted collapsed agents in raw model order.
    const panelOrder = groups.flatMap((g) => displayMembers(g));
    setAgentOrder(panelOrder.map((a) => a.address));

    const p = bunnyPalette(t);
    const sort = sortOf();
    const status = (a) => {
      if (a.status === "working") return svg("mods/workspace-chrome/spinner.svg").size(12).mx(1).flex_none().text_color(p.caution).spin(900);
      if (a.status === "blocked") return div().flex_none().px(5).h(16).rounded(5).bg(p.danger + "26").text_size(10).font_semibold().text_color(p.danger).flex().items_center().child("Needs you");
      if (a.status === "idle") return div().size(7).mx(3).flex_none().rounded(4).border(1.5).border_color(p.ok);
      return div().size(7).mx(3).flex_none().rounded(4).bg(a.status === "done" ? p.ok : (t[a.status] ?? p.title));
    };

    const rows = [];
    for (const g of groups) {
      // One header per block: the tag names the chief, a plain space names
      // itself. A group's first row hangs under the header, the rest one
      // level in (Go: rowBuilder.depth).
      const head = g.tag || labelOf(g.members[0]?.space) || g.id;
      const top = g.members.some((a) => a.status === "blocked") ? "blocked"
        : g.members.some((a) => a.status === "working") ? "working" : null;
      const headColor = top === "blocked" ? p.danger : top === "working" ? p.caution : p.identity;
      const isCollapsed = this.collapsed.has(g.id);
      rows.push(h_flex()
        .id(`sc-h-${g.id}`)
        .px(10).pt(6).pb(2).h(26).flex_none().gap(5).items_center().rounded(6)
        .text_size(11).font_semibold().text_color(headColor)
        .cursor_pointer().hover((el) => el.bg(t.hover))
        .on_click((_e, cx) => {
          this.toggleCollapsed(g.id);
          cx.notify();
        })
        .child(svg(`mods/workspace-chrome/chevron-${isCollapsed ? "right" : "down"}.svg`).size(10).flex_none().text_color(headColor))
        .child(div().flex_1().min_w(0).truncate().child(head))
        .when(isCollapsed, (el) => el.child(div().flex_none().text_size(10).text_color(t.dim).child(String(g.members.length))))
        .when(Boolean(top), (el) => el.child(div().size(6).flex_none().rounded(3).bg(headColor))));

      if (isCollapsed) continue;

      displayMembers(g).forEach((a, i) => {
        const rec = recByAddress.get(a.address);
        const name = rowName(a, g);
        const machine = String(a.address).includes("/") ? a.address.split("/")[0] : (rec?.machine ?? null);
        rows.push(h_flex()
          .id(`sc-${a.address}`)
          .px(12).pl(i === 0 && !g.tag ? 12 : 24).h(28).flex_none().rounded(8).gap(8).items_center()
          .text_size(12.5)
          .when(a.focused, (el) => el.bg(t.selected))
          .hover((el) => el.bg(t.hover))
          .cursor_pointer()
          .on_click(() => run("pane.focus", { terminal: a.address }))
          .child(div().flex_1().min_w(0).truncate().text_color(a.focused ? p.identity : p.title).when(a.focused, (el) => el.font_semibold()).child(name))
          .when(Boolean(rec?.state), (el) => el.child(div().flex_none().text_size(10).font_semibold().text_color(scopeStateColor(rec.state, p)).child(rec.state)))
          .when(Boolean(rec?.runtime), (el) => el.child(div().flex_none().text_size(10).text_color(p.accent2).child(rec.runtime)))
          .when(Boolean(machine), (el) => el.child(div().flex_none().text_size(10).text_color(p.accent2).child(machine)))
          .child(status(a)));
      });
    }

    return v_flex()
      .size_full().min_h(0).bg(t.chrome).border_t(1).border_color(t.border).text_size(13)
      .child(h_flex().px(16).pt(12).pb(4).h(36).flex_none().items_center().gap(6)
        .child(svg("icons-bunny/bunny-chief.svg").size(14).flex_none().text_color(p.accent2))
        .child(div().flex_none().text_size(11.5).font_semibold().text_color(p.accent2).child("Scope"))
        .child(div().flex_1().min_w(4))
        .child(div().id("sc-sort").flex_none().w(24).h(20).flex().items_center().justify_center()
          .rounded(4).cursor_pointer().hover((s) => s.bg(t.hover))
          .tooltip(`Sort rows: ${sort.label}`)
          .on_click(() => openOverlay({ mod: "scope", kind: "popup", width: 200, height: 140, props: { sortMenu: true } }))
          .child(svg(`mods/workspace-chrome/${sort.icon}.svg`).size(14).flex_none().text_color(t.dim))))
      .when(ordered.length === 0, (el) => el.child(
        div().px(12).text_size(12).text_color(t.dim).child("No agents yet. Workers appear here grouped under their chief tag, in the order their spaces were created.")))
      .child(v_flex().id("sc-list").flex_1().min_h(0).px(8).overflow_y_scrollbar().mode("always").children(rows));
  }
}
