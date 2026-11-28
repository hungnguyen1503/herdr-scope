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
import { agents, spaces, theme, run, shared, setAgentOrder, props, checking, home, exec, readTextFile } from "herdr";

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
    this.ledger = null;
    // `herdr-app check` runs init for real and renders once: no process reads,
    // no timers there.
    if (checking()) return;
    const refresh = Math.max(5000, Number(props()?.refresh) || 30000);
    const reload = (cx) => loadLedger(ledgerDir()).then((l) => { this.ledger = l; cx.notify(); }, () => { this.ledger = { dir: ledgerDir(), byLabel: new Map(), byTerminal: new Map(), missing: true }; });
    reload(cx);
    cx.timer.every(refresh, reload);
  }

  render() {
    const t = theme();
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
    // agent.next/prev/select follow the panel, not the model order.
    setAgentOrder(ordered.map((a) => a.address));

    const status = (a) => {
      if (a.status === "working") return svg("mods/workspace-chrome/spinner.svg").size(12).mx(1).flex_none().text_color(t.working).spin(900);
      if (a.status === "blocked") return div().flex_none().px(5).h(16).rounded(5).bg(t.blocked + "26").text_size(10).font_semibold().text_color(t.blocked).flex().items_center().child("Needs you");
      if (a.status === "idle") return div().size(7).mx(3).flex_none().rounded(4).border(1.5).border_color(t.idle ?? t.dim);
      return div().size(7).mx(3).flex_none().rounded(4).bg(t[a.status] ?? t.dim);
    };

    const groups = groupsOf(ordered, rs.fam);
    const rows = [];
    for (const g of groups) {
      // One header per block: the tag names the chief, a plain space names
      // itself. A group's first row hangs under the header, the rest one
      // level in (Go: rowBuilder.depth).
      const head = g.tag || labelOf(g.members[0]?.space) || g.id;
      const top = g.members.some((a) => a.status === "blocked") ? "blocked"
        : g.members.some((a) => a.status === "working") ? "working" : null;
      rows.push(h_flex()
        .id(`sc-h-${g.id}`)
        .px(12).pt(8).pb(2).h(26).flex_none().gap(6).items_center()
        .text_size(11).font_medium().text_color(t[top] ?? t.dim)
        .child(div().truncate().child(head))
        .when(Boolean(top), (el) => el.child(div().size(6).flex_none().rounded(3).bg(t[top]))));
      g.members.forEach((a, i) => {
        const rec = recByAddress.get(a.address);
        const label = labelOf(a.space) || a.space_name;
        // The short name: a tagged row shows its bare worker, the way the Go
        // plugin publishes run_worker; an untagged row shows its telling title.
        const name = g.tag ? (workerName(label) || clean(a.title) || a.program?.name || a.address)
          : (clean(a.tab_name) || clean(a.title) || a.program?.name || a.address);
        const machine = String(a.address).includes("/") ? a.address.split("/")[0] : (rec?.machine ?? null);
        rows.push(h_flex()
          .id(`sc-${a.address}`)
          .px(12).pl(i === 0 && !g.tag ? 12 : 24).h(28).flex_none().rounded(8).gap(8).items_center()
          .text_size(12.5)
          .when(a.focused, (el) => el.bg(t.selected))
          .hover((el) => el.bg(t.hover))
          .cursor_pointer()
          .on_click(() => run("pane.focus", { terminal: a.address }))
          .child(div().flex_1().min_w(0).truncate().text_color(a.focused ? t.foreground : t.muted).child(name))
          .when(Boolean(rec?.state), (el) => el.child(div().flex_none().text_size(10).text_color(t.dim).child(rec.state)))
          .when(Boolean(rec?.runtime), (el) => el.child(div().flex_none().text_size(10).text_color(t.dim).child(rec.runtime)))
          .when(Boolean(machine), (el) => el.child(div().flex_none().text_size(10).text_color(t.dim).child(machine)))
          .child(status(a)));
      });
    }

    return v_flex()
      .size_full().min_h(0).bg(t.chrome).text_size(13)
      .child(h_flex().px(16).pt(12).pb(4).h(36).flex_none().items_center().gap(8)
        .child(div().text_size(11.5).font_medium().text_color(t.dim).child("Scope"))
        .child(div().flex_1())
        .child(div().text_size(10).text_color(t.dim).child(
          led === null ? "ledger: loading" : !led.dir ? "ledger: off" : led.missing ? "ledger: no runs" : `${led.byLabel.size} runs`)))
      .when(ordered.length === 0, (el) => el.child(
        div().px(12).text_size(12).text_color(t.dim).child("No agents yet. Workers appear here grouped under their chief tag, in the order their spaces were created.")))
      .child(v_flex().id("sc-list").flex_1().min_h(0).px(8).overflow_y_scrollbar().mode("always").children(rows));
  }
}
