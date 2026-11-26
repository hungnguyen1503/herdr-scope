package domain

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scenario builds sidebar inputs compactly: one agent per (workspace, tab,
// pane) triple, activity in minutes before t0 (negative = none).
type scenario struct {
	workspaces []Workspace
	tabs       []Tab
	agents     []ObservedAgent
}

func (s *scenario) ws(id WorkspaceID, label string, repo *RepositoryInfo) *scenario {
	s.workspaces = append(s.workspaces, Workspace{ID: id, Label: label, Repository: repo})
	return s
}

func (s *scenario) agent(ws WorkspaceID, tab string, pane string, minutesAgo int, display DisplayState) *scenario {
	tabID := TabID(fmt.Sprintf("%s:%s", ws, tab))
	known := false
	for _, t := range s.tabs {
		known = known || t.ID == tabID
	}
	if !known {
		s.tabs = append(s.tabs, Tab{ID: tabID, Workspace: ws, Label: tab})
	}
	activity := Activity{}
	if minutesAgo >= 0 {
		activity = Activity{At: at(0).Add(-time.Duration(minutesAgo) * time.Minute)}
	}
	paneID := PaneID(fmt.Sprintf("%s:%s", ws, pane))
	s.agents = append(s.agents, ObservedAgent{
		Agent:    Agent{ID: AgentID(paneID), Pane: paneID, Tab: tabID, Workspace: ws, Kind: "claude", Title: "task " + string(paneID)},
		Display:  display,
		Activity: activity,
	})
	return s
}

func (s *scenario) build(mode SortMode, policy DisplayPolicy) SidebarView {
	return BuildSidebar(SidebarInput{At: at(0), Mode: mode, Policy: policy, Workspaces: s.workspaces, Tabs: s.tabs, Agents: s.agents})
}

func repo(name string, linked bool, checkout string) *RepositoryInfo {
	return &RepositoryInfo{
		ID:       RepositoryID("/src/" + name + "/.git"),
		Name:     name,
		Root:     "/src/" + name,
		Checkout: WorktreeID(checkout),
		Linked:   linked,
	}
}

// outline renders a view as text for compact assertions.
func outline(v SidebarView) []string {
	var out []string
	for _, g := range v.Groups {
		if f := g.Rows[0].Fold; f != nil {
			line := fmt.Sprintf("▸ %s +%d", f.Label, f.Hidden)
			if f.Working {
				line += " working " + string(f.WorkingKind)
			}
			if f.Alone {
				line += " alone"
			}
			out = append(out, line)
		}
		if g.Header {
			tree := map[TreePosition]string{TreeRoot: "", TreeChild: "├ ", TreeLastChild: "└ ", TreeDetached: "~ "}[g.Tree]
			if g.OrphanRepo != "" {
				tree = "@" + g.OrphanRepo + " " + tree
			}
			stale := ""
			if g.Stale {
				stale = " (stale)"
			}
			out = append(out, "# "+tree+g.Label+stale)
		}
		for _, r := range g.Rows {
			corner := map[Corner]string{CornerNone: "", CornerTee: "├─", CornerLast: "└─"}[r.Corner]
			out = append(out, fmt.Sprintf("%d%s %s", r.Depth, corner, r.Pane))
		}
		if g.Gap {
			out = append(out, "")
		}
	}
	return out
}

func TestActiveOrderPutsTheBusiestGroupFirst(t *testing.T) {
	s := (&scenario{}).ws("w1", "billing", nil).ws("w2", "dashboard", nil).
		agent("w1", "t1", "p1", 90, DisplayIdle).
		agent("w2", "t1", "p1", 1, DisplayIdleFresh).
		agent("w2", "t2", "p2", 30, DisplayIdle)
	v := s.build(SortActive, spaced())
	require.True(t, v.Grouped)
	assert.Equal(t, []string{
		"# dashboard", "0 w2:p1", "1 w2:p2", "",
		"# billing", "0 w1:p1", "",
	}, outline(v))
}

func TestRecentOrderIsFlatAndUnstampedLast(t *testing.T) {
	s := (&scenario{}).ws("w1", "billing", nil).ws("w2", "dashboard", nil).
		agent("w1", "t1", "p1", -1, DisplayIdle).
		agent("w1", "t2", "p2", 5, DisplayIdle).
		agent("w2", "t1", "p1", 1, DisplayWorking)
	v := s.build(SortRecent, DefaultDisplayPolicy())
	assert.False(t, v.Grouped)
	assert.Equal(t, []string{"0 w2:p1", "0 w1:p2", "0 w1:p1"}, outline(v))
	assert.Empty(t, v.Groups[0].Rows[2].Keys.Pane, "no stamp publishes no pane key")
}

func TestOffOrderKeepsHerdrOrderAndGroupsOnlyWhenHerdrDoes(t *testing.T) {
	s := (&scenario{}).ws("w1", "billing", nil).ws("w2", "dashboard", nil).
		agent("w1", "t1", "p1", 90, DisplayIdle).
		agent("w2", "t1", "p1", 1, DisplayIdle)
	in := SidebarInput{
		At:          at(0),
		Mode:        SortOff,
		HostGrouped: true,
		Policy:      spaced(),
		Workspaces:  s.workspaces,
		Tabs:        s.tabs,
		Agents:      s.agents,
	}
	assert.Equal(t, []string{"# billing", "0 w1:p1", "", "# dashboard", "0 w2:p1", ""}, outline(BuildSidebar(in)))
	in.HostGrouped = false
	assert.Equal(t, []string{"0 w1:p1", "0 w2:p1"}, outline(BuildSidebar(in)))
}

func TestGapsFollowTheSetting(t *testing.T) {
	s := (&scenario{}).ws("w1", "a", nil).ws("w2", "b", nil).
		agent("w1", "t1", "p1", 1, DisplayIdle).agent("w2", "t1", "p1", 2, DisplayIdle)
	p := spaced()
	p.GroupGap = false
	assert.Equal(t, []string{"# a", "0 w1:p1", "# b", "0 w2:p1"}, outline(s.build(SortActive, p)))
}

// spaced is the default policy with a blank row after every block: the tests
// of structure predate pack_single and pin their gaps.
func spaced() DisplayPolicy {
	p := DefaultDisplayPolicy()
	p.PackSingle = false
	return p
}

func TestSingleBlocksPack(t *testing.T) {
	s := (&scenario{}).ws("w1", "billing", nil).ws("w2", "dashboard", nil).ws("w3", "notes", nil).
		agent("w2", "t1", "p1", 1, DisplayWorking).
		agent("w2", "t2", "p2", 30, DisplayIdle).
		agent("w1", "t1", "p1", 5, DisplayIdle).
		agent("w3", "t1", "p1", 10, DisplayIdle)
	assert.Equal(t, []string{
		"# dashboard", "0 w2:p1", "1 w2:p2", "",
		"# billing", "0 w1:p1",
		"# notes", "0 w3:p1",
	}, outline(s.build(SortActive, DefaultDisplayPolicy())), "one-agent workspaces follow each other; nothing trails the last")

	s.agents[2].Display = DisplayWorking
	s.agents[2].Activity.At = at(0)
	assert.Equal(t, []string{
		"# billing", "0 w1:p1", "",
		"# dashboard", "0 w2:p1", "1 w2:p2", "",
		"# notes", "0 w3:p1",
	}, outline(s.build(SortActive, DefaultDisplayPolicy())), "a group of several agents keeps a blank row on both sides")

	p := DefaultDisplayPolicy()
	p.GroupGap = false
	assert.Equal(t, []string{
		"# billing", "0 w1:p1",
		"# dashboard", "0 w2:p1", "1 w2:p2",
		"# notes", "0 w3:p1",
	}, outline(s.build(SortActive, p)))

	assert.Equal(t, []string{
		"# billing", "0 w1:p1", "",
		"# dashboard", "0 w2:p1", "1 w2:p2", "",
		"# notes", "0 w3:p1", "",
	}, outline(s.build(SortActive, spaced())), "pack_single off: a blank row after every block")
}

func TestAFamilyIsNeverASingleBlock(t *testing.T) {
	s := (&scenario{}).
		ws("app", "app", repo("app", false, "/src/app")).
		ws("other", "other", nil).
		ws("feature", "feature/auth", repo("app", true, "/wt/feature")).
		ws("hotfix", "hotfix", repo("app", true, "/wt/hotfix")).
		agent("app", "t1", "p1", 300, DisplayIdleStale).
		agent("other", "t1", "p1", 10, DisplayIdle).
		agent("feature", "t1", "p1", 1, DisplayWorking).
		agent("hotfix", "t1", "p1", 20, DisplayIdle)
	assert.Equal(t, []string{
		"# app (stale)", "0 app:p1",
		"# ├ feature/auth", "1 feature:p1",
		"# └ hotfix", "1 hotfix:p1", "",
		"# other", "0 other:p1",
	}, outline(s.build(SortActive, DefaultDisplayPolicy())), "one agent per member, still a block of several")

	s = (&scenario{}).ws("feature", "feature/auth", repo("app", true, "/wt/feature")).ws("other", "other", nil).
		agent("feature", "t1", "p1", 1, DisplayIdle).
		agent("other", "t1", "p1", 10, DisplayIdle)
	assert.Equal(t, []string{
		"# @app └ feature/auth", "1 feature:p1", "",
		"# other", "0 other:p1",
	}, outline(s.build(SortActive, DefaultDisplayPolicy())), "a synthesised repository row makes it three lines")
}

func TestAFoldWithOneAgentPacks(t *testing.T) {
	s := (&scenario{}).ws("w1", "notes", nil).ws("w2", "other", nil).
		agent("w1", "t1", "p1", 1, DisplayWorking).
		agent("w2", "t1", "p1", 5, DisplayIdle)
	v := BuildSidebar(SidebarInput{
		At: at(0), Mode: SortActive, Policy: DefaultDisplayPolicy(), Workspaces: s.workspaces, Tabs: s.tabs, Agents: s.agents,
		Folded: map[WorkspaceID]bool{"w1": true}, Focused: "w2",
	})
	assert.Equal(t, []string{
		"▸ notes +0", "0 w1:p1",
		"# other", "0 w2:p1",
	}, outline(v), "the line stands for the header: still one agent")
}

func TestWorktreeFamilyStaysTogetherParentFirst(t *testing.T) {
	s := (&scenario{}).
		ws("app", "app", repo("app", false, "/src/app")).
		ws("other", "other", nil).
		ws("feature", "feature/auth", repo("app", true, "/wt/feature")).
		ws("hotfix", "hotfix", repo("app", true, "/wt/hotfix")).
		agent("app", "t1", "p1", 300, DisplayIdleStale).
		agent("other", "t1", "p1", 10, DisplayIdle).
		agent("feature", "t1", "p1", 1, DisplayWorking).
		agent("hotfix", "t1", "p1", 20, DisplayIdle)
	v := s.build(SortActive, spaced())
	// The busy worktree pulls its dormant parent up with it; the parent is
	// drawn first and the family has no gap inside.
	assert.Equal(t, []string{
		"# app (stale)", "0 app:p1",
		"# ├ feature/auth", "1 feature:p1",
		"# └ hotfix", "1 hotfix:p1", "",
		"# other", "0 other:p1", "",
	}, outline(v))
}

func TestOrphanWorktreeGetsARepositoryRow(t *testing.T) {
	s := (&scenario{}).
		ws("app", "app", repo("app", false, "/src/app")).
		ws("feature", "feature/auth", repo("app", true, "/wt/feature")).
		agent("feature", "t1", "p1", 1, DisplayIdle)
	v := s.build(SortActive, spaced())
	assert.Equal(t, []string{"# @app └ feature/auth", "1 feature:p1", ""}, outline(v))

	s.ws("hotfix", "hotfix", repo("app", true, "/wt/hotfix")).agent("hotfix", "t1", "p1", 2, DisplayIdle)
	assert.Equal(t, []string{
		"# @app ├ feature/auth", "1 feature:p1",
		"# └ hotfix", "1 hotfix:p1", "",
	}, outline(s.build(SortActive, spaced())), "siblings share the row")
}

func TestStrayWorktreeIsDetachedInHerdrOrder(t *testing.T) {
	// Herdr's order: app, its first worktree, an unrelated project, a worktree
	// of app opened later, another project.
	s := (&scenario{}).
		ws("app", "app", repo("app", false, "/src/app")).
		ws("feature", "feature", repo("app", true, "/wt/feature")).
		ws("other", "other", nil).
		ws("hotfix", "hotfix", repo("app", true, "/wt/hotfix")).
		ws("scratch", "scratch", nil)
	for _, ws := range []WorkspaceID{"app", "feature", "other", "hotfix", "scratch"} {
		s.agent(ws, "t1", "p1", 1, DisplayIdle)
	}
	in := SidebarInput{
		At:          at(0),
		Mode:        SortOff,
		HostGrouped: true,
		Policy:      spaced(),
		Workspaces:  s.workspaces,
		Tabs:        s.tabs,
		Agents:      s.agents,
	}
	assert.Equal(t, []string{
		"# app", "0 app:p1",
		"# └ feature", "1 feature:p1", "",
		"# other", "0 other:p1", "",
		"# ~ hotfix", "0 hotfix:p1", "",
		"# scratch", "0 scratch:p1", "",
	}, outline(BuildSidebar(in)), "the stray gets no corner into a stranger and no second repo row")

	v := s.build(SortActive, DefaultDisplayPolicy())
	for _, g := range v.Groups {
		assert.NotEqual(t, TreeDetached, g.Tree, "in Scope's own order a family is one block")
	}
}

func TestSecondPaneOfAWorkspaceIndents(t *testing.T) {
	s := (&scenario{}).ws("w1", "a", repo("a", true, "/wt/a")).ws("w0", "root", repo("a", false, "/src/a")).
		agent("w0", "t1", "p1", 5, DisplayIdle).
		agent("w1", "t1", "p1", 1, DisplayIdle).
		agent("w1", "t2", "p2", 2, DisplayIdle)
	assert.Equal(t, []string{
		"# root", "0 w0:p1",
		"# └ a", "1 w1:p1", "2 w1:p2", "",
	}, outline(s.build(SortActive, spaced())))
}

func TestSplitCorners(t *testing.T) {
	s := (&scenario{}).ws("w1", "proj", nil).
		agent("w1", "t1", "p1", 3, DisplayIdle).
		agent("w1", "t1", "p2", 1, DisplayIdle).
		agent("w1", "t1", "p3", 2, DisplayIdle).
		agent("w1", "t2", "p4", 10, DisplayIdle)
	p := DefaultDisplayPolicy()
	assert.Equal(t, []string{"# proj", "0 w1:p1", "1 w1:p2", "1 w1:p3", "1 w1:p4", ""}, outline(s.build(SortActive, p)),
		"by default a split draws plain rows, but its panes stay together, the split-from pane first")

	p.SplitCorner = true
	assert.Equal(t, []string{"# proj", "0 w1:p1", "1├─ w1:p2", "1└─ w1:p3", "1 w1:p4", ""}, outline(s.build(SortActive, p)))

	p.GroupIndent = 0
	assert.Equal(t, []string{"# proj", "0 w1:p1", "1 w1:p2", "1 w1:p3", "1 w1:p4", ""}, outline(s.build(SortActive, p)),
		"zero indent draws no corners")
}

func TestRowLabelsAndPrefixTrim(t *testing.T) {
	s := (&scenario{}).ws("w1", "billing", nil).agent("w1", "review", "p1", 1, DisplayIdle)
	s.agents[0].Agent.Title = "billing · fix rounding"
	p := DefaultDisplayPolicy()
	row := s.build(SortActive, p).Groups[0].Rows[0]
	assert.Equal(t, "fix rounding", row.Title)

	p.TrimGroupPrefix = false
	assert.Equal(t, "billing · fix rounding", s.build(SortActive, p).Groups[0].Rows[0].Title)

	p = DefaultDisplayPolicy()
	assert.Equal(t, "billing · fix rounding", s.build(SortRecent, p).Groups[0].Rows[0].Title, "no header in the flat view, nothing to trim")

	p.RowLabel = RowLabelBoth
	row = s.build(SortActive, p).Groups[0].Rows[0]
	assert.Equal(t, "review", row.TabLabel)
	assert.Equal(t, "fix rounding", row.Title)
}

func TestSortKeysEncodeFamilyAndSplit(t *testing.T) {
	s := (&scenario{}).ws("w1", "a", nil).agent("w1", "t1", "p1", 1, DisplayIdle).agent("w1", "t1", "p2", -1, DisplayIdle)
	v := s.build(SortActive, DefaultDisplayPolicy())
	head, second := v.Groups[0].Rows[0], v.Groups[0].Rows[1]
	minute := fmt.Sprintf("%012d", at(0).Add(-time.Minute).UnixMilli()/60000)
	assert.Equal(t, minute, head.Keys.Pane)
	assert.Equal(t, minute+"-w1-1-"+minute+"-w1", head.Keys.Workspace)
	assert.Equal(t, minute+"-w1:t1-1-"+minute, head.Keys.Tab)
	assert.Equal(t, minute+"-w1:t1-0-"+noStamp, second.Keys.Tab)
	assert.Empty(t, second.Keys.Pane)
}

func TestSpacesAggregateByUrgency(t *testing.T) {
	s := (&scenario{}).ws("w1", "a", nil).ws("w2", "b", nil).ws("w3", "empty", nil).
		agent("w1", "t1", "p1", 1, DisplayWorking).
		agent("w1", "t2", "p2", 1, DisplayBlocked).
		agent("w2", "t1", "p1", 30, DisplayIdle).
		agent("w2", "t2", "p2", 1, DisplayIdleFresh)
	s.agents[1].Agent.Kind = "codex"
	v := s.build(SortActive, DefaultDisplayPolicy())
	require.Len(t, v.Spaces, 3)
	assert.Equal(
		t,
		SpaceView{
			Workspace: "w1", Label: "a", Mark: DisplayBlocked, MarkKind: "codex",
			Kinds: []AgentKind{"claude", "codex"}, Active: []AgentKind{"claude", "codex"},
		},
		v.Spaces[0],
	)
	assert.Equal(t, DisplayIdleFresh, v.Spaces[1].Mark, "among parked agents the freshest speaks")
	assert.Empty(t, v.Spaces[1].Active, "nothing going on: no vendor is active")
	assert.Equal(t, SpaceView{Workspace: "w3", Label: "empty"}, v.Spaces[2], "a workspace with no agent has no mark")
}

func TestStaleHeaderOnlyWhenEveryAgentIsStale(t *testing.T) {
	s := (&scenario{}).ws("w1", "a", nil).
		agent("w1", "t1", "p1", 300, DisplayIdleStale).
		agent("w1", "t2", "p2", 1, DisplayIdle)
	assert.False(t, s.build(SortActive, DefaultDisplayPolicy()).Groups[0].Stale)
	s.agents[1].Display = DisplayIdleStale
	assert.True(t, s.build(SortActive, DefaultDisplayPolicy()).Groups[0].Stale)
}

func TestEmptySidebar(t *testing.T) {
	v := (&scenario{}).ws("w1", "a", nil).build(SortActive, DefaultDisplayPolicy())
	assert.Empty(t, v.Groups)
	assert.Len(t, v.Spaces, 1)
	assert.Empty(t, (&scenario{}).build(SortRecent, DefaultDisplayPolicy()).Groups)
}

func TestWorkspaceOrderMovesFamiliesAndIsIdempotent(t *testing.T) {
	s := (&scenario{}).
		ws("app", "app", repo("app", false, "/src/app")).
		ws("idle", "idle", nil).
		ws("other", "other", nil).
		ws("feature", "feature", repo("app", true, "/wt/feature")).
		agent("app", "t1", "p1", 300, DisplayIdleStale).
		agent("other", "t1", "p1", 10, DisplayIdle).
		agent("feature", "t1", "p1", 1, DisplayWorking)
	v := s.build(SortActive, DefaultDisplayPolicy())
	current := []WorkspaceID{"app", "idle", "other", "feature"}
	tree := ResolveWorktrees(s.workspaces)
	wanted := WorkspaceOrder(current, v, tree)
	assert.Equal(t, []WorkspaceID{"app", "feature", "other", "idle"}, wanted)
	assert.Equal(t, wanted, WorkspaceOrder(wanted, v, tree), "applying it twice moves nothing")
	assert.Equal(t, current, WorkspaceOrder(current, SidebarView{}, tree), "no activity, no move")
}

func TestWaitingAsksBeforeFinished(t *testing.T) {
	view := SidebarView{Groups: []SidebarGroup{
		{Rows: []SidebarRow{{Pane: "w1:p1", Display: DisplayDone}, {Pane: "w1:p2", Display: DisplayWorking}}},
		{Rows: []SidebarRow{{Pane: "w2:p1", Display: DisplayBlocked}, {Pane: "w2:p2", Display: DisplayIdleFresh}}},
		{Rows: []SidebarRow{{Pane: "w3:p1", Display: DisplayDone}, {Pane: "w3:p2", Display: DisplayBlocked}}},
	}}
	assert.Equal(t, []PaneID{"w2:p1", "w3:p2", "w1:p1", "w3:p1"}, view.Waiting(),
		"every question in panel order, then every unseen result")
	assert.Empty(t, SidebarView{}.Waiting())
}

func TestNextWaitingGoesRoundTheList(t *testing.T) {
	waiting := []PaneID{"w2:p1", "w1:p1", "w3:p1"}
	assert.Equal(t, PaneID("w2:p1"), NextWaiting(waiting, "w9:p9"), "the focus is on none of them: the first")
	assert.Equal(t, PaneID("w1:p1"), NextWaiting(waiting, "w2:p1"), "the one after the focused one")
	assert.Equal(t, PaneID("w2:p1"), NextWaiting(waiting, "w3:p1"), "round the list")
	assert.Equal(t, PaneID("w2:p1"), NextWaiting([]PaneID{"w2:p1"}, "w2:p1"), "alone: itself again")
	assert.Empty(t, NextWaiting(nil, "w2:p1"), "nobody waits")
}

func TestBuildSidebarDrawsTheTreeItIsGiven(t *testing.T) {
	s := folderScenario()
	in := SidebarInput{
		At: at(0), Mode: SortActive, Policy: DefaultDisplayPolicy(), Workspaces: s.workspaces, Tabs: s.tabs, Agents: s.agents,
	}
	given := in
	resolved := ResolveTree(s.workspaces, true, "")
	given.Tree = &resolved
	assert.Equal(t, BuildSidebar(in), BuildSidebar(given), "the tree it would resolve itself")
	flat := ResolveTree(s.workspaces, false, "")
	given.Tree = &flat
	assert.NotEqual(t, outline(BuildSidebar(in)), outline(BuildSidebar(given)), "a given tree is the one drawn")
}

func TestHereNarrowsActiveAndComesBack(t *testing.T) {
	assert.Equal(t, SortHere, SortActive.Apply(SortHereToggle))
	assert.Equal(t, SortHere, SortRecent.Apply(SortHereToggle))
	assert.Equal(t, SortActive, SortHere.Apply(SortHereToggle), "again: all of them")
	assert.Equal(t, SortRecent, SortHere.Apply(SortCycle), "the cycle carries on as from active")
	assert.True(t, SortHere.Grouped(false), "grouped like active")
	mode, ok := ParseSortMode("here")
	assert.True(t, ok)
	assert.Equal(t, SortHere, mode)
}

func TestFamilyIsTheRootAndItsMembers(t *testing.T) {
	workspaces := projectSession()
	tree := ResolveTree(workspaces, true, "/home/user")
	ids := order(workspaces)
	assert.Equal(t, []WorkspaceID{"acme", "gateway", "billing", "search"}, tree.Family("acme", ids))
	assert.Equal(t, []WorkspaceID{"acme", "gateway", "billing", "search"}, tree.Family("billing", ids), "from a member too")
	assert.Equal(t, []WorkspaceID{"notes"}, tree.Family("notes", ids), "alone in its own")
	assert.Equal(t, WorkspaceID("acme"), tree.Root("billing"))
	assert.Equal(t, WorkspaceID("acme"), tree.Root("acme"))
	assert.Equal(t, WorkspaceID("w9"), tree.Root("w9"), "a workspace the tree does not know is its own root")
}

func TestWaitingCounts(t *testing.T) {
	view := SidebarView{Groups: []SidebarGroup{{Rows: []SidebarRow{
		{Display: DisplayBlocked}, {Display: DisplayDone}, {Display: DisplayDone}, {Display: DisplayWorking},
	}}}}
	asking, finished := view.WaitingCounts()
	assert.Equal(t, 1, asking)
	assert.Equal(t, 2, finished)
}

func TestTriageOrderRanksByNeedsYouAndLongestWait(t *testing.T) {
	sinceMin := func(minutesAgo int) time.Time {
		if minutesAgo < 0 {
			return time.Time{}
		}
		return at(0).Add(-time.Duration(minutesAgo) * time.Minute)
	}
	s := (&scenario{}).
		ws("w-unknown", "unknown", nil).
		ws("w-stale", "stale", nil).
		ws("w-idle", "idle", nil).
		ws("w-fresh", "fresh", nil).
		ws("w-work", "work", nil).
		ws("w-done-new", "done-new", nil).
		ws("w-done-old", "done-old", nil).
		ws("w-blocked-zero", "blocked-zero", nil).
		ws("w-blocked-new", "blocked-new", nil).
		ws("w-blocked-old", "blocked-old", nil)
	add := func(ws WorkspaceID, activityAgo int, sinceAgo int, display DisplayState) {
		s.agent(ws, "t1", "p1", activityAgo, display)
		s.agents[len(s.agents)-1].Since = sinceMin(sinceAgo)
	}
	// Activity stamps deliberately invert urgency to verify triage ignores raw activity.
	add("w-unknown", 1, 1, DisplayUnknown)
	add("w-stale", 2, 300, DisplayIdleStale)
	add("w-idle", 3, 45, DisplayIdle)
	add("w-fresh", 4, 5, DisplayIdleFresh)
	add("w-work", 5, 10, DisplayWorking)
	add("w-done-new", 6, 3, DisplayDone)
	add("w-done-old", 20, 15, DisplayDone)
	add("w-blocked-zero", 1, -1, DisplayBlocked)
	add("w-blocked-new", 2, 4, DisplayBlocked)
	add("w-blocked-old", 50, 12, DisplayBlocked)

	v := s.build(SortTriage, DefaultDisplayPolicy())
	require.True(t, v.Grouped)
	assert.Equal(t, []string{
		"# blocked-old", "0 w-blocked-old:p1",
		"# blocked-new", "0 w-blocked-new:p1",
		"# blocked-zero", "0 w-blocked-zero:p1",
		"# done-old", "0 w-done-old:p1",
		"# done-new", "0 w-done-new:p1",
		"# work", "0 w-work:p1",
		"# fresh", "0 w-fresh:p1",
		"# idle", "0 w-idle:p1",
		"# stale (stale)", "0 w-stale:p1",
		"# unknown", "0 w-unknown:p1",
	}, outline(v))

	// Verify descending lexicographic sort of published keys mirrors BuildSidebar order.
	for i := 1; i < len(v.Groups); i++ {
		prev := v.Groups[i-1].Rows[0].Keys
		curr := v.Groups[i].Rows[0].Keys
		assert.Greater(t, prev.Workspace, curr.Workspace)
		assert.Greater(t, prev.Pane, curr.Pane)
	}
}

func TestTriageOrderKeepsActiveGroupingWorktreesSplitsAndFolds(t *testing.T) {
	// Blocked worktree pulls its stale parent up; parent is drawn first, no gap inside.
	s := (&scenario{}).
		ws("app", "app", repo("app", false, "/src/app")).
		ws("other", "other", nil).
		ws("feature", "feature/auth", repo("app", true, "/wt/feature")).
		ws("hotfix", "hotfix", repo("app", true, "/wt/hotfix")).
		agent("app", "t1", "p1", 300, DisplayIdleStale).
		agent("other", "t1", "p1", 1, DisplayWorking).
		agent("feature", "t1", "p1", 50, DisplayBlocked).
		agent("hotfix", "t1", "p1", 20, DisplayIdle)
	for i := range s.agents {
		s.agents[i].Since = s.agents[i].Activity.At
	}

	v := s.build(SortTriage, spaced())
	assert.Equal(t, []string{
		"# app (stale)", "0 app:p1",
		"# ├ feature/auth", "1 feature:p1",
		"# └ hotfix", "1 hotfix:p1", "",
		"# other", "0 other:p1", "",
	}, outline(v))

	tree := ResolveWorktrees(s.workspaces)
	assert.Equal(t, []WorkspaceID{"app", "hotfix", "feature", "other"},
		WorkspaceOrder([]WorkspaceID{"other", "app", "hotfix", "feature"}, v, tree))

	// Split corners keep the split-from pane first even when a split child is blocked.
	splits := (&scenario{}).ws("w1", "proj", nil).
		agent("w1", "t1", "p1", 10, DisplayIdle).
		agent("w1", "t1", "p2", 20, DisplayBlocked).
		agent("w1", "t1", "p3", 5, DisplayDone).
		agent("w1", "t2", "p4", 1, DisplayWorking)
	for i := range splits.agents {
		splits.agents[i].Since = splits.agents[i].Activity.At
	}
	p := DefaultDisplayPolicy()
	p.SplitCorner = true
	sv := splits.build(SortTriage, p)
	assert.Equal(t, []string{"# proj", "0 w1:p1", "1├─ w1:p2", "1└─ w1:p3", "1 w1:p4", ""}, outline(sv))
	for i := 1; i < len(sv.Groups[0].Rows); i++ {
		prev := sv.Groups[0].Rows[i-1].Keys
		curr := sv.Groups[0].Rows[i].Keys
		assert.GreaterOrEqual(t, prev.Workspace, curr.Workspace)
		assert.Greater(t, prev.Tab, curr.Tab)
	}

	// Fold line borrows the family's first row and sorts strictly before it.
	fv := folderScenario().folded(SortTriage, "acme", "acme")
	line, root := fv.Groups[0].Rows[0], fv.Groups[1].Rows[0]
	require.NotNil(t, line.Fold)
	assert.Equal(t, root.Keys.Workspace, line.Keys.Workspace)
	assert.Equal(t, root.Keys.Tab, line.Keys.Tab)
	assert.Greater(t, line.Keys.Pane, root.Keys.Pane)
}

func TestTriageOrderWithRunLedger(t *testing.T) {
	now := at(0)
	s := (&scenario{}).
		ws("w-blocked-ui", "blocked-ui", nil).
		ws("w-run-fail", "run-fail", nil).
		ws("w-review", "review", nil).
		ws("w-work", "work", nil).
		agent("w-blocked-ui", "t1", "p1", 10, DisplayBlocked).
		agent("w-run-fail", "t1", "p1", 5, DisplayIdle).
		agent("w-review", "t1", "p1", 20, DisplayIdle).
		agent("w-work", "t1", "p1", 1, DisplayWorking)
	s.agents[0].Since = at(-10 * time.Minute)
	s.agents[3].Since = at(-1 * time.Minute)

	runs := map[PaneID]RunMeta{
		"w-run-fail:p1": {
			RunID:            "run-1",
			State:            "failed",
			SignalStatus:     "failed",
			SignalReceivedAt: now.Add(-2 * time.Minute),
		},
		"w-review:p1": {
			RunID:         "run-2",
			State:         "awaiting-review",
			StepStartedAt: now.Add(-15 * time.Minute),
		},
	}

	buildWith := func(mode SortMode, runRootSet bool) SidebarView {
		return BuildSidebar(SidebarInput{
			At:         now,
			Mode:       mode,
			Policy:     DefaultDisplayPolicy(),
			Workspaces: s.workspaces,
			Agents:     s.agents,
			Runs:       runs,
			RunRootSet: runRootSet,
		})
	}

	v := buildWith(SortTriage, true)
	assert.Equal(t, []string{
		"# run-fail", "0 w-run-fail:p1",
		"# blocked-ui", "0 w-blocked-ui:p1",
		"# review", "0 w-review:p1",
		"# work", "0 w-work:p1",
	}, outline(v))
	require.NotNil(t, v.Groups[0].Rows[0].Run)
	assert.Equal(t, "run-1", v.Groups[0].Rows[0].Run.RunID)

	// When RunRootSet is false, Runs is completely ignored.
	off := buildWith(SortTriage, false)
	assert.Equal(t, []string{
		"# blocked-ui", "0 w-blocked-ui:p1",
		"# work", "0 w-work:p1",
		"# run-fail", "0 w-run-fail:p1",
		"# review", "0 w-review:p1",
	}, outline(off))
	assert.Nil(t, off.Groups[2].Rows[0].Run)

	// Non-triage orders keep identical outlines regardless of RunRootSet.
	for _, mode := range []SortMode{SortActive, SortRecent, SortHere, SortOff} {
		assert.Equal(t, outline(buildWith(mode, false)), outline(buildWith(mode, true)), mode)
	}
}

// rowIndex is a row's position in the flattened view: headers count, so the
// number is where the cursor would sit.
func rowIndex(t *testing.T, v SidebarView, pane PaneID) (int, SortKeys, DisplayState) {
	t.Helper()
	i := 0
	for _, g := range v.Groups {
		if g.Header {
			i++
		}
		for _, r := range g.Rows {
			if r.Pane == pane {
				return i, r.Keys, r.Display
			}
			i++
		}
	}
	t.Fatalf("row %s not in the view", pane)
	return 0, SortKeys{}, ""
}

// clickScenario is a worktree family whose checked-off member leads it: the
// checkout, its worktree carrying the unseen ✓, and a working neighbour.
// A click focuses the ✓ row and clears it, which is exactly what demoted it.
func clickScenario() *scenario {
	return (&scenario{}).
		ws("w1", "app", repo("app", false, "/src/app")).
		ws("w2", "app-wt", repo("app", true, "/src/app-wt")).
		ws("w3", "other", nil).
		agent("w1", "t1", "p1", 90, DisplayIdle).
		agent("w2", "t1", "p2", 5, DisplayDone).
		agent("w3", "t1", "p3", 1, DisplayWorking)
}

func (s *scenario) buildPinned(mode SortMode, pinned map[PaneID]SortKeys) SidebarView {
	return BuildSidebar(SidebarInput{
		At: at(0), Mode: mode, Policy: spaced(), Workspaces: s.workspaces, Tabs: s.tabs,
		Agents: s.agents, Pinned: pinned,
	})
}

func TestPinnedRowDoesNotMoveWhileFocused(t *testing.T) {
	cases := []struct {
		name string
		mode SortMode
		// wantMoves is whether clearing the ✓ alone would move the row.
		wantMoves bool
	}{
		// Triage ranks done (6) over working (5) over idle-fresh (4), and the
		// family rolls up its best member, so losing the ✓ drops both.
		{"triage demotes on release", SortTriage, true},
		// Active, here and recent key on activity alone: the ✓ never ranked.
		{"active is unaffected", SortActive, false},
		{"recent is unaffected", SortRecent, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := clickScenario()
			before := s.build(tc.mode, spaced())
			at, keys, _ := rowIndex(t, before, "w2:p2")

			// The click: focus clears the ✓, so the row paints idle-fresh.
			s.agents[1].Display = DisplayIdleFresh
			pinned := s.buildPinned(tc.mode, map[PaneID]SortKeys{"w2:p2": keys})

			gotAt, gotKeys, gotDisplay := rowIndex(t, pinned, "w2:p2")
			assert.Equal(t, at, gotAt, "the focused row does not move")
			assert.Equal(t, keys, gotKeys, "its three published keys are byte-identical")
			assert.Equal(t, DisplayIdleFresh, gotDisplay, "the paint stays honest: ✓ cleared, row reads idle-fresh")
			assert.Equal(t, outline(before), outline(pinned), "nothing around it moves either, the family included")

			released := s.build(tc.mode, spaced())
			releasedAt, releasedKeys, _ := rowIndex(t, released, "w2:p2")
			if tc.wantMoves {
				assert.NotEqual(t, keys, releasedKeys, "released, the row sorts by its own key")
				assert.NotEqual(t, at, releasedAt, "releasing the pin lets the row drop to its real rank")
			} else {
				assert.Equal(t, keys, releasedKeys, "activity alone was frozen: the ✓ never ranked, so the keys are the same")
				assert.Equal(t, at, releasedAt, "and in this order the position never changed either")
			}
		})
	}
}

// TestTriagePutsARunOwnerAboveEveryTier proves the chief case: the engineer's
// own pane must never be shuffled below a worker it launched, even when that
// worker has signalled done. Without the owner set the same input must produce
// today's order, so run_root being unset changes nothing.
func TestTriagePutsARunOwnerAboveEveryTier(t *testing.T) {
	now := at(0)
	// The chief sits in its own workspace with a quiet, idle history; the
	// worker in another has just signalled done and travels with its family.
	base := (&scenario{}).
		ws("w-chief", "chief", nil).
		ws("w-worker", "worker", repo("app", false, "/src/app")).
		ws("w-wt", "app/wt", repo("app", true, "/wt/app")).
		agent("w-chief", "t1", "p1", 200, DisplayIdleStale).
		agent("w-worker", "t1", "p1", 1, DisplayDone).
		agent("w-wt", "t1", "p1", 40, DisplayBlocked)
	for i := range base.agents {
		if base.agents[i].Display == DisplayBlocked {
			base.agents[i].Since = now.Add(-5 * time.Minute)
		}
	}
	runs := map[PaneID]RunMeta{
		"w-worker:p1": {RunID: "run-w", State: "awaiting-review", OwnerPane: "w-chief:p1"},
		"w-wt:p1":     {RunID: "run-x", State: "running", OwnerPane: "w-chief:p1"},
	}

	build := func(mode SortMode, owners map[PaneID]struct{}, runRootSet bool) SidebarView {
		return BuildSidebar(SidebarInput{
			At: now, Mode: mode, Policy: DefaultDisplayPolicy(),
			Workspaces: base.workspaces, Tabs: base.tabs, Agents: base.agents,
			Runs: runs, RunRootSet: runRootSet, RunOwners: owners,
		})
	}
	owners := map[PaneID]struct{}{"w-chief:p1": {}}

	t.Run("an owner row outranks a blocked worker", func(t *testing.T) {
		v := build(SortTriage, owners, true)
		assert.Equal(t, []string{
			"# chief (stale)", "0 w-chief:p1", "",
			"# worker", "0 w-worker:p1", "# └ app/wt", "1 w-wt:p1", "",
		}, outline(v))
		chiefAt, chiefKeys, chiefDisplay := rowIndex(t, v, "w-chief:p1")
		workerAt, workerKeys, _ := rowIndex(t, v, "w-worker:p1")
		assert.Equal(t, 1, chiefAt, "the chief is the first row under its header")
		assert.Equal(t, DisplayIdleStale, chiefDisplay, "the paint is untouched: it really is an idle row")
		assert.Less(t, chiefAt, workerAt)
		assert.Greater(t, chiefKeys.Pane, workerKeys.Pane, "the pane key carries the tier")
		assert.Greater(t, chiefKeys.Workspace, workerKeys.Workspace, "and the workspace rolls it up")
	})

	t.Run("without the owner set today's order is byte-identical", func(t *testing.T) {
		without := build(SortTriage, nil, true)
		assert.Equal(t, []string{
			"# worker", "0 w-worker:p1", "# └ app/wt", "1 w-wt:p1", "",
			"# chief (stale)", "0 w-chief:p1",
		}, outline(without), "without owners the done worker still floats above the idle chief")
		// and the same set with run_root off leaves everything as it was.
		assert.Equal(t, outline(without), outline(build(SortTriage, owners, false)))
	})

	t.Run("an owner's workspace rises with its row", func(t *testing.T) {
		v := build(SortTriage, owners, true)
		_, chiefKeys, _ := rowIndex(t, v, "w-chief:p1")
		_, workerKeys, _ := rowIndex(t, v, "w-worker:p1")
		assert.Greater(t, chiefKeys.Workspace, workerKeys.Workspace, "the chief's workspace travels above the blocked worker's family")
		assert.Equal(t, "9", chiefKeys.Workspace[:1], "and the family key carries the owner tier")
	})

	t.Run("a worker carrying a run is not an owner", func(t *testing.T) {
		// The ledger matched runs to both worker panes; only the owner's pane
		// takes the top tier, so the worker keeps its own done tier.
		v := build(SortTriage, owners, true)
		_, workerKeys, _ := rowIndex(t, v, "w-worker:p1")
		_, blockedKeys, _ := rowIndex(t, v, "w-wt:p1")
		assert.Equal(t, "6", workerKeys.Pane[:1], "the worker sorts by its done tier, not the top tier")
		assert.Equal(t, "7", blockedKeys.Pane[:1], "and the blocked worktree by its own tier")
	})

	t.Run("other orders ignore the owner set", func(t *testing.T) {
		for _, mode := range []SortMode{SortActive, SortRecent, SortHere, SortOff} {
			assert.Equal(t, outline(build(mode, nil, true)), outline(build(mode, owners, true)), mode)
		}
	})
}
