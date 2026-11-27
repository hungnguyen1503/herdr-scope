package domain

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stableScenario is scenario with Herdr's workspace numbers, which the stable
// order sorts by.
type stableScenario struct {
	workspaces []Workspace
	tabs       []Tab
	agents     []ObservedAgent
}

func (s *stableScenario) ws(id WorkspaceID, label string, number int, repo *RepositoryInfo) *stableScenario {
	s.workspaces = append(s.workspaces, Workspace{ID: id, Label: label, Number: number, Repository: repo})
	return s
}

func (s *stableScenario) agent(ws WorkspaceID, tab, pane string, minutesAgo int, display DisplayState) *stableScenario {
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

func (s *stableScenario) build(mode SortMode) SidebarView {
	return BuildSidebar(SidebarInput{
		At: at(0), Mode: mode, Policy: spaced(), Workspaces: s.workspaces, Tabs: s.tabs, Agents: s.agents,
	})
}

func TestChiefTagSplitsOnTheFirstDot(t *testing.T) {
	for _, tc := range []struct {
		label, want, why string
	}{
		{"c1.scope.triage-order", "c1", "the first segment"},
		{"c1.app", "c1", "two segments"},
		{"c1.", "c1", "an empty remainder is opaque"},
		{"app", "", "no dot names no tag"},
		{"", "", "an empty label names none"},
		{".app.x", "", "an empty first segment names none"},
		{"c1...", "c1", "the rest of the label is opaque"},
		{"a.b.c.d", "a", ""},
		{"abcdefghijklmnop.extra", "abcdefghijklmnop", "16 characters fit"},
		{"abcdefghijklmnopq.extra", "abcdefghijklmnop", "a longer tag is capped at 16"},
	} {
		assert.Equal(t, tc.want, ChiefTag(tc.label), "%q: %s", tc.label, tc.why)
	}
}

func TestStableKeepsWorkspaceNumberOrder(t *testing.T) {
	// Created out of order: w2 is the oldest, w1 the newest.
	s := (&stableScenario{}).ws("w1", "late", 3, nil).ws("w2", "early", 1, nil).ws("w3", "middle", 2, nil).
		agent("w1", "t1", "p1", 1, DisplayWorking).
		agent("w2", "t1", "p1", 90, DisplayIdle).
		agent("w3", "t1", "p2", 30, DisplayIdle)
	assert.Equal(t, []string{
		"# early", "0 w2:p1", "",
		"# middle", "0 w3:p2", "",
		"# late", "0 w1:p1", "",
	}, outline(s.build(SortStable)), "the creation order, not the busiest")
}

func TestStableRowsNeverMoveOnAStatusChange(t *testing.T) {
	build := func(displays []DisplayState, focused string, minutes []int) SidebarView {
		s := (&stableScenario{}).ws("w1", "one", 1, nil).ws("w2", "two", 2, nil).ws("w3", "three", 3, nil)
		for i, id := range []WorkspaceID{"w1", "w2", "w3"} {
			s.agent(id, "t1", "p1", minutes[i], displays[i])
			if focused == string(id) {
				s.agents[len(s.agents)-1].Agent.Focused = true
			}
		}
		return s.build(SortStable)
	}
	keys := func(v SidebarView) map[PaneID]SortKeys {
		out := map[PaneID]SortKeys{}
		for _, g := range v.Groups {
			for _, r := range g.Rows {
				out[r.Pane] = r.Keys
			}
		}
		return out
	}
	idle := build([]DisplayState{DisplayIdle, DisplayIdle, DisplayIdle}, "", []int{50, 40, 30})
	for _, tc := range []struct {
		name     string
		displays []DisplayState
		focused  string
		minutes  []int
	}{
		{"a worker starts and finishes", []DisplayState{DisplayWorking, DisplayDone, DisplayBlocked}, "", []int{1, 2, 3}},
		{"a row is focused", []DisplayState{DisplayIdle, DisplayIdle, DisplayIdle}, "w3", []int{50, 40, 30}},
		{"every stamp moves", []DisplayState{DisplayIdleFresh, DisplayIdle, DisplayIdle}, "", []int{0, 100, 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := build(tc.displays, tc.focused, tc.minutes)
			assert.Equal(t, outline(idle), outline(got), "no re-sort")
			assert.Equal(t, keys(idle), keys(got), "and no key moved either")
		})
	}
}

func TestStableFallsBackToHerdrListOrderWithoutNumbers(t *testing.T) {
	// Herdr did not number these: the snapshot's own order is the creation
	// order then, and a workspace only an agent named sorts last.
	s := (&stableScenario{}).ws("w2", "second", 0, nil).ws("w1", "first", 0, nil).
		agent("w2", "t1", "p1", 1, DisplayWorking).
		agent("w1", "t1", "p1", 90, DisplayIdle).
		agent("w9", "t1", "p1", 5, DisplayIdle)
	assert.Equal(t, []string{
		"# second", "0 w2:p1", "",
		"# first", "0 w1:p1", "",
		"# w9", "0 w9:p1", "",
	}, outline(s.build(SortStable)), "Herdr's list order, then the workspace only an agent named")
}

func TestStableGroupsWorkersUnderTheirChiefTag(t *testing.T) {
	s := (&stableScenario{}).ws("w2", "c1.scope.triage-order", 2, nil).ws("w1", "plain", 1, nil).
		ws("w3", "c1.scope.activate-ledger", 3, nil).
		agent("w2", "t1", "p1", 5, DisplayWorking).
		agent("w1", "t1", "p1", 1, DisplayWorking).
		agent("w3", "t1", "p1", 60, DisplayIdle)
	assert.Equal(t, []string{
		"# plain", "0 w1:p1", "",
		"# c1", "1 w2:p1", "1 w3:p1", "",
	}, outline(s.build(SortStable)), "one header names the chief, both workers under it")
}

func TestStableKeepsEachChiefsWorkersTogether(t *testing.T) {
	// c2 was created between c1's two workers: each tag keeps its own block,
	// the elder chief's first.
	s := (&stableScenario{}).ws("w1", "c1.app.one", 1, nil).ws("w2", "c2.app.two", 2, nil).
		ws("w3", "c1.app.three", 3, nil).
		agent("w1", "t1", "p1", 90, DisplayIdle).
		agent("w2", "t1", "p1", 1, DisplayWorking).
		agent("w3", "t1", "p1", 5, DisplayWorking)
	assert.Equal(t, []string{
		"# c1", "1 w1:p1", "1 w3:p1", "",
		"# c2", "1 w2:p1", "",
	}, outline(s.build(SortStable)), "one block per tag, the eldest member deciding where it stands")
}

func TestStableHangsAnUntaggedWorktreeOffItsTaggedCheckout(t *testing.T) {
	// The checkout names a chief, its untagged worktree keeps the family git
	// gives it: the corner hangs off the block above, and the tag is not the
	// worktree's business.
	s := (&stableScenario{}).
		ws("w1", "c1.app.main", 1, repo("app", false, "/src/app")).
		ws("w2", "feature", 2, repo("app", true, "/wt/feature")).
		agent("w1", "t1", "p1", 5, DisplayIdle).
		agent("w2", "t1", "p1", 1, DisplayWorking)
	assert.Equal(t, []string{
		"# c1", "1 w1:p1",
		"# └ feature", "1 w2:p1", "",
	}, outline(s.build(SortStable)), "the worktree rides in the tag's block, its corner off the checkout")
}

func TestStableKeepsUntaggedWorkspacesOnTheGitTree(t *testing.T) {
	s := (&stableScenario{}).
		ws("w1", "app", 1, repo("app", false, "/src/app")).
		ws("w2", "feature", 2, repo("app", true, "/wt/feature")).
		ws("w3", "notes", 3, nil).
		agent("w1", "t1", "p1", 5, DisplayIdle).
		agent("w2", "t1", "p1", 1, DisplayWorking).
		agent("w3", "t1", "p1", 10, DisplayIdle)
	assert.Equal(t, []string{
		"# app", "0 w1:p1",
		"# └ feature", "1 w2:p1", "",
		"# notes", "0 w3:p1", "",
	}, outline(s.build(SortStable)), "a worktree still rides with its checkout")
}

func TestStableTagBeatsTheWorktree(t *testing.T) {
	s := (&stableScenario{}).
		ws("w1", "app", 1, repo("app", false, "/src/app")).
		ws("w2", "c1.app.worker", 2, repo("app", true, "/wt/worker")).
		agent("w1", "t1", "p1", 5, DisplayIdle).
		agent("w2", "t1", "p1", 1, DisplayWorking)
	assert.Equal(t, []string{
		"# app", "0 w1:p1", "",
		"# c1", "1 w2:p1", "",
	}, outline(s.build(SortStable)), "the label's tag wins over the shared repository")

	// An untagged worktree of the same checkout keeps the family git gives it:
	// the tag moves only the workspace that names it.
	s = (&stableScenario{}).
		ws("w1", "app", 1, repo("app", false, "/src/app")).
		ws("w2", "c1.app.worker", 2, repo("app", true, "/wt/worker")).
		ws("w3", "hotfix", 3, repo("app", true, "/wt/hotfix")).
		agent("w1", "t1", "p1", 5, DisplayIdle).
		agent("w2", "t1", "p1", 1, DisplayWorking).
		agent("w3", "t1", "p1", 20, DisplayIdle)
	assert.Equal(t, []string{
		"# app", "0 w1:p1",
		"# └ hotfix", "1 w3:p1", "",
		"# c1", "1 w2:p1", "",
	}, outline(s.build(SortStable)), "only the tagged label leaves the checkout's family")
}

func TestStableLeavesOtherOrdersAlone(t *testing.T) {
	s := (&stableScenario{}).ws("w1", "c1.app.one", 1, nil).ws("w2", "c1.app.two", 2, nil).
		agent("w1", "t1", "p1", 5, DisplayIdle).
		agent("w2", "t1", "p1", 1, DisplayWorking)
	// Without a tag read, the two are unrelated workspaces: each its own block.
	assert.Equal(t, []string{
		"# c1.app.two", "0 w2:p1", "",
		"# c1.app.one", "0 w1:p1", "",
	}, outline(s.build(SortActive)), "active never parses a label")
}

func TestStableModeJoinsTheSortTable(t *testing.T) {
	mode, ok := ParseSortMode("stable")
	require.True(t, ok)
	assert.Equal(t, SortStable, mode)
	assert.True(t, SortStable.Grouped(false))
	assert.False(t, SortStable.Folds(), "a fold line appearing would shift rows below it")
	assert.Equal(t, SortOff, SortStable.Apply(SortNative))
}
