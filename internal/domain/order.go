package domain

import (
	"maps"
	"sort"
	"strconv"
	"time"
)

// SortKeys are the ordering keys of one row. Each is a string that sorts
// lexicographically in the intended order (descending = most active first),
// because the host sorts the panel by them as opaque values.
type SortKeys struct {
	// Pane ranks the row by its own last activity, minute-grained; empty
	// when the agent has no activity stamp (such rows sort last).
	Pane string
	// Workspace ranks the row's whole workspace: its worktree family's most
	// recent member first, the family's checkout before its worktrees, then
	// the workspace's own activity, with the id as tie-break.
	Workspace string
	// Tab ranks the panes of one split screen as a unit, the pane the others
	// were split from first.
	Tab string
}

const (
	noStamp         = "000000000000"
	maxMinute int64 = 999999999999
	// layoutCeil is the top of the descending layout keys: a list this long
	// is past any terminal Herdr draws.
	layoutCeil int64 = maxMinute
)

// minuteKey is an activity stamp at minute grain, zero-padded so that string
// order is numeric order: a working pane changes its key once a minute.
func minuteKey(activity Activity) string {
	if !activity.Known() {
		return ""
	}
	key := strconv.FormatInt(activity.At.UnixMilli()/int64(time.Minute/time.Millisecond), 10)
	if len(key) < len(noStamp) {
		key = noStamp[len(key):] + key
	}
	return key
}

// triageKey encodes who needs the engineer first (rank descending) and, within
// a rank, longest wait first by inverting the minute stamp against maxMinute
// (earliest Since -> largest string); unknown waits sort last in the rank.
func triageKey(display DisplayState, since time.Time, run *RunMeta, runRootSet, owner bool) string {
	if since.IsZero() && runRootSet && run != nil {
		if !run.SignalReceivedAt.IsZero() {
			since = run.SignalReceivedAt
		} else if !run.StepStartedAt.IsZero() {
			since = run.StepStartedAt
		}
	}
	wait := noStamp
	if !since.IsZero() {
		m := since.UnixMilli() / int64(time.Minute/time.Millisecond)
		m = min(maxMinute, max(0, m))
		key := strconv.FormatInt(maxMinute-m, 10)
		if len(key) < len(noStamp) {
			key = noStamp[len(key):] + key
		}
		wait = key
	}
	return triageRank(display, run, runRootSet, owner) + "-" + wait
}

// triageRank is the tier a row sits in, descending. A run owner — the pane
// that launched the workers — takes the top tier whatever it is displaying: an
// engineer's own session must never be shuffled below a worker it launched and
// has already signalled. Inside that tier the wait/activity tie-break still
// orders two owners sensibly.
func triageRank(display DisplayState, run *RunMeta, runRootSet, owner bool) string {
	if owner {
		return "9"
	}
	if runRootSet && run != nil {
		if run.SignalStatus == "failed" || run.SignalStatus == "blocked" {
			return "8"
		}
		if run.State == "awaiting-review" {
			return "6"
		}
	}
	switch display {
	case DisplayBlocked:
		return "7"
	case DisplayDone:
		return "6"
	case DisplayWorking:
		return "5"
	case DisplayIdleFresh:
		return "4"
	case DisplayIdle:
		return "3"
	case DisplayIdleStale:
		return "2"
	case DisplayUnknown:
		return "1"
	}
	return "1"
}

// orNone is a minute key, or noStamp for a pane without activity.
func orNone(key string) string {
	if key == "" {
		return noStamp
	}
	return key
}

// orderEntry is one agent row as ordering sees it.
type orderEntry struct {
	agent    Agent
	activity Activity
	display  DisplayState
	since    time.Time
	run      *RunMeta
}

// ranking is everything ordering knows about one snapshot.
type ranking struct {
	pane      map[PaneID]string
	workspace map[WorkspaceID]string
	tab       map[PaneID]string
	// parentOf hangs a member under a parent the panel lists; orphanOf names
	// the family of a member whose parent it does not, so that the family
	// still ranks and sits as one block, under a synthesised parent row.
	parentOf   map[WorkspaceID]WorkspaceID
	orphanOf   map[WorkspaceID]WorkspaceID
	orphanRepo map[WorkspaceID]string
	// famOf overrides family(): under stable a worktree of a tagged checkout
	// rides with the tag, or its corner would point into another block.
	famOf      map[WorkspaceID]WorkspaceID
	detached   map[WorkspaceID]bool
	splitChild map[PaneID]bool
}

func (r ranking) keys(agent Agent) SortKeys {
	return SortKeys{Pane: r.pane[agent.Pane], Workspace: r.workspace[agent.Workspace], Tab: r.tab[agent.Pane]}
}

// nested reports whether a workspace sits one level in: a worktree under a
// real parent group or under a synthesised parent row.
func (r ranking) nested(ws WorkspaceID) bool {
	_, child := r.parentOf[ws]
	_, orphan := r.orphanRepo[ws]
	return child || orphan
}

// family is the block a workspace ranks with: its parent, listed or
// synthesised, or itself.
func (r ranking) family(ws WorkspaceID) WorkspaceID {
	if family, ok := r.famOf[ws]; ok {
		return family
	}
	if parent, ok := r.parentOf[ws]; ok {
		return parent
	}
	if parent, ok := r.orphanOf[ws]; ok {
		return parent
	}
	return ws
}

// rank computes the ordering keys of a snapshot.
//
// Git worktrees ride with the checkout they were cut from, and nested folders
// with the folder: a family ranks by its most recent member and stays
// contiguous, parent first. When the parent runs no agent the members still
// stay together, under a synthesised parent row (orphanRepo: the folder's
// name, or the repository's).
//
// Under stable the keys read no activity at all: the workspace number, then
// Herdr's tab and pane order, so nothing a status change does can move a row.
// A chief tag from a workspace label is the family there, whatever git says.
func rank(
	entries []orderEntry, tree WorktreeTree, mode SortMode, numbers map[WorkspaceID]int,
	runRootSet bool, owners map[PaneID]struct{}, pinned map[PaneID]SortKeys,
) ranking {
	r := ranking{
		pane: make(map[PaneID]string, len(entries)), workspace: map[WorkspaceID]string{},
		tab: make(map[PaneID]string, len(entries)), parentOf: map[WorkspaceID]WorkspaceID{},
		orphanOf: map[WorkspaceID]WorkspaceID{}, orphanRepo: map[WorkspaceID]string{}, detached: map[WorkspaceID]bool{},
		splitChild: make(map[PaneID]bool, len(entries)),
	}
	stable := mode == SortStable
	keys := make([]string, len(entries))
	own := map[WorkspaceID]string{}
	present := map[WorkspaceID]bool{}
	var wsOrder []WorkspaceID
	for i, e := range entries {
		var key string
		switch {
		case stable:
			// Its layout position, laid out once the families are known.
		case mode == SortTriage:
			_, owner := owners[e.agent.Pane]
			key = triageKey(e.display, e.since, e.run, runRootSet, owner && runRootSet)
		default:
			key = minuteKey(e.activity)
		}
		// A focused row keeps the key it had before the focus cleared its ✓:
		// substituting it here, before the workspace and tab roll-ups read
		// keys[i], also keeps the family it leads where the family was.
		// Stable keys never change, so there is nothing to hold.
		if pin, ok := pinned[e.agent.Pane]; ok && pin.Pane != "" && !stable {
			key = pin.Pane
		}
		keys[i] = key
		r.pane[e.agent.Pane] = key
		ws := e.agent.Workspace
		if ws == "" {
			continue
		}
		if !present[ws] {
			present[ws] = true
			wsOrder = append(wsOrder, ws)
		}
		if stable {
			// rankStable lays the keys out from the numbers alone.
			continue
		}
		if k := orNone(key); k > own[ws] {
			own[ws] = k
		}
	}
	for child, parent := range tree.Parents {
		switch {
		case !present[child]:
		case present[parent]:
			r.parentOf[child] = parent
		default:
			r.orphanOf[child] = parent
		}
	}
	for _, ws := range wsOrder {
		if repo := tree.ParentLabels[ws]; repo != "" {
			if _, child := r.parentOf[ws]; !child {
				r.orphanRepo[ws] = repo
			}
		}
	}
	if stable {
		// A label's chief tag outranks the git tree: the workers of one chief
		// sit under one synthesised row however they were cut. Only a label
		// moves a workspace: an untagged one keeps the family git gives it,
		// and detachStrays draws it plain when that family went elsewhere.
		for ws, tag := range tree.TagOf {
			if !present[ws] {
				continue
			}
			delete(r.parentOf, ws)
			r.orphanOf[ws] = TagRoot(tag)
			r.orphanRepo[ws] = tag
		}
		rankStable(entries, &r, numbers)
		return r
	}
	familyKey := map[WorkspaceID]string{}
	for _, ws := range wsOrder {
		if key := own[ws]; key > familyKey[r.family(ws)] {
			familyKey[r.family(ws)] = key
		}
	}
	for _, ws := range wsOrder {
		f := r.family(ws)
		depth := "0"
		if f == ws {
			depth = "1"
		}
		r.workspace[ws] = familyKey[f] + "-" + string(f) + "-" + depth + "-" + own[ws] + "-" + string(ws)
	}
	rankTabs(entries, keys, &r)
	return r
}

// rankStable is the ordering with nothing live in it: a workspace's key is
// its family (a chief tag, or its git family) and Herdr's own creation
// number, a tab's and a pane's their place in Herdr's layout. No activity, no
// status and no pin goes in, so no repaint can reorder a row.
func rankStable(entries []orderEntry, r *ranking, numbers map[WorkspaceID]int) {
	own := map[WorkspaceID]string{}
	var wsOrder []WorkspaceID
	for _, e := range entries {
		ws := e.agent.Workspace
		if ws == "" {
			continue
		}
		if _, seen := own[ws]; !seen {
			own[ws] = numberKey(numbers[ws])
			wsOrder = append(wsOrder, ws)
		}
	}
	// A worktree of a tagged checkout rides with the tag, so that every block
	// reads as one family to the furniture above and below it.
	r.famOf = make(map[WorkspaceID]WorkspaceID, len(wsOrder))
	for _, ws := range wsOrder {
		r.famOf[ws] = r.family(ws)
	}
	for _, ws := range wsOrder {
		if parent, child := r.parentOf[ws]; child {
			r.famOf[ws] = r.famOf[parent]
		}
	}
	// A family stands where its eldest member was created: the largest key
	// under the host's descending sort is the smallest number.
	familyKey := map[WorkspaceID]string{}
	for _, ws := range wsOrder {
		if key := own[ws]; key > familyKey[r.family(ws)] {
			familyKey[r.family(ws)] = key
		}
	}
	for _, ws := range wsOrder {
		f := r.family(ws)
		depth := "0"
		if f == ws {
			depth = "1"
		}
		r.workspace[ws] = familyKey[f] + "-" + string(f) + "-" + depth + "-" + own[ws] + "-" + string(ws)
	}
	// Herdr lists agents in layout order: a tab's first appearance is its
	// place in the tab bar, a pane's its place in the split.
	tabs, sizes := map[TabID]string{}, map[TabID]int{}
	paneKey := make(map[PaneID]string, len(entries))
	for i, e := range entries {
		tab := e.agent.Tab
		if tab == "" {
			r.pane[e.agent.Pane] = layoutKey(i)
			continue
		}
		if _, seen := tabs[tab]; !seen {
			tabs[tab] = layoutKey(len(tabs))
		}
		key := layoutKey(sizes[tab])
		sizes[tab]++
		paneKey[e.agent.Pane] = key
		head := "0"
		if sizes[tab] == 1 {
			head = "1"
		}
		r.tab[e.agent.Pane] = tabs[tab] + "-" + string(tab) + "-" + head + "-" + key
		r.pane[e.agent.Pane] = key
	}
	for _, e := range entries {
		tab := e.agent.Tab
		if tab == "" {
			continue
		}
		r.splitChild[e.agent.Pane] = sizes[tab] > 1 && paneKey[e.agent.Pane] != layoutKey(0)
	}
}

// numberKey is a workspace number as a key the host's descending sort puts
// in creation order: number 1 sorts first. A workspace Herdr did not number
// (only an agent named it) sorts after every numbered one.
func numberKey(n int) string {
	if n <= 0 {
		return noStamp
	}
	return layoutKey(n - 1)
}

// layoutKey is a layout position (or a workspace number) as a descending key:
// the host sorts tokens descending, so the earliest position gets the largest
// string.
func layoutKey(i int) string {
	key := strconv.FormatInt(layoutCeil-int64(i), 10)
	if len(key) < len(noStamp) {
		key = noStamp[len(key):] + key
	}
	return key
}

// rankTabs ranks the panes sharing a tab — one split screen — as a unit, by
// their most recent member; inside it the pane the others were split from
// (Herdr lists panes in layout order, so the first seen) comes first.
// keys are the entries' pane sort keys.
func rankTabs(entries []orderEntry, keys []string, r *ranking) {
	panes := map[TabID][]PaneID{}
	best := map[TabID]string{}
	for i, e := range entries {
		tab := e.agent.Tab
		if tab == "" {
			continue
		}
		panes[tab] = append(panes[tab], e.agent.Pane)
		if key := orNone(keys[i]); key > best[tab] {
			best[tab] = key
		}
	}
	for i, e := range entries {
		tab := e.agent.Tab
		if tab == "" {
			continue
		}
		head := "0"
		if panes[tab][0] == e.agent.Pane {
			head = "1"
		}
		r.tab[e.agent.Pane] = best[tab] + "-" + string(tab) + "-" + head + "-" + orNone(keys[i])
		r.splitChild[e.agent.Pane] = len(panes[tab]) > 1 && panes[tab][0] != e.agent.Pane
	}
}

// displayOrder predicts the order the panel shows, because group furniture
// (headers, gaps, corners) is pinned to the first and last rows as displayed.
// It mirrors the host's stable sort on the same keys, missing values last.
func displayOrder(entries []orderEntry, mode SortMode, r ranking) []orderEntry {
	out := append([]orderEntry(nil), entries...)
	switch mode {
	case SortActive, SortHere, SortTriage, SortStable:
		sort.SliceStable(out, func(i, j int) bool { return compareActive(out[i], out[j], r) < 0 })
	case SortRecent:
		sort.SliceStable(out, func(i, j int) bool {
			return compareDescMissingLast(r.pane[out[i].agent.Pane], r.pane[out[j].agent.Pane]) < 0
		})
	case SortOff:
	}
	return out
}

func compareActive(a, b orderEntry, r ranking) int {
	if c := compareDesc(r.workspace[a.agent.Workspace], r.workspace[b.agent.Workspace]); c != 0 {
		return c
	}
	if c := compareDesc(r.tab[a.agent.Pane], r.tab[b.agent.Pane]); c != 0 {
		return c
	}
	return compareDescMissingLast(r.pane[a.agent.Pane], r.pane[b.agent.Pane])
}

func compareDesc(a, b string) int {
	switch {
	case a == b:
		return 0
	case a > b:
		return -1
	default:
		return 1
	}
}

func compareDescMissingLast(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	default:
		return compareDesc(a, b)
	}
}

// detachStrays draws a worktree's corner only where the panel puts it right
// under its parent, or under a sibling that is. In the plugin's own order a
// family is one block; in Herdr's order a worktree opened later is appended
// to the end and a corner would hang it off whichever project sits above. Such
// a worktree is drawn detached: top level, its mark the only sign of what it
// is, and no repository row (the repository is already on the panel).
func detachStrays(ordered []orderEntry, r ranking) ranking {
	parentOf := maps.Clone(r.parentOf)
	detached := map[WorkspaceID]bool{}
	seen := map[WorkspaceID]bool{}
	var previous WorkspaceID
	for _, e := range ordered {
		ws := e.agent.Workspace
		if ws == "" || seen[ws] {
			continue
		}
		seen[ws] = true
		parent, isChild := parentOf[ws]
		// A detached sibling left parentOf, so it cannot vouch for the next:
		// a run of strays stays a run of strays.
		if isChild && previous != parent && parentOf[previous] != parent {
			delete(parentOf, ws)
			detached[ws] = true
		}
		previous = ws
	}
	r.parentOf = parentOf
	r.detached = detached
	return r
}
