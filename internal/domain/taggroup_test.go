package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tagGroups is the chief-tag headers a view carries, and the rows under each.
func tagGroups(v SidebarView) map[string]int {
	out := map[string]int{}
	for _, g := range v.Groups {
		if g.Header {
			out[g.Label] += len(g.Rows)
		}
	}
	return out
}

// TestWorkerNameSplitsOnTheLastDot pins the token's rule at the layer that
// owns label parsing: the chief owns the header, so only the tail identifies
// the row, and a tail that is the whole label identifies nothing.
func TestWorkerNameSplitsOnTheLastDot(t *testing.T) {
	for _, tc := range []struct {
		label, want, why string
	}{
		{"c1.scope.triage-order", "triage-order", "the legacy three segments"},
		{"c1.bug-worker", "bug-worker", "the two-segment form"},
		{"bunny-chief-1.herdr-scope.bug-worker", "bug-worker", "a long chief does not shift the tail"},
		{"c1.", "", "an empty tail names no worker"},
		{"c1...", "", "the last of an empty segment is still empty"},
		{"bunny-chief-of-staff", "", "no dot names no worker"},
		{"", "", "an empty label names none"},
		{".c1", "c1", "an empty chief still leaves a tail"},
		{"a.b.c.d", "d", "only the last segment counts"},
	} {
		assert.Equal(t, tc.want, WorkerName(tc.label), "%q: %s", tc.label, tc.why)
	}
}

// TestStablePutsOneHeaderOnATagWithUnrelatedWorkers is the reported bug: two
// workers of one chief in different repositories, cut from nothing in common,
// rendered as two chief-2 blocks because the run split on every workspace.
func TestStablePutsOneHeaderOnATagWithUnrelatedWorkers(t *testing.T) {
	s := (&stableScenario{}).
		ws("w1", "chief-2.alpha.one", 1, repo("alpha", false, "/src/alpha")).
		ws("w2", "chief-2.beta.two", 2, repo("beta", false, "/src/beta")).
		agent("w1", "t1", "p1", 5, DisplayWorking).
		agent("w2", "t1", "p1", 1, DisplayWorking)
	v := s.build(SortStable)
	assert.Equal(t, []string{
		"# chief-2", "1 w1:p1", "1 w2:p1", "",
	}, outline(v), "one header for the tag, both workers under it")
	assert.Equal(t, map[string]int{"chief-2": 2}, tagGroups(v), "and no second chief-2 block")
}

// TestStablePutsOneHeaderOnAWorktreePairOfOneTag covers the members git does
// relate: the tag still owns the block, so the members' own git parentage
// cannot split it.
func TestStablePutsOneHeaderOnAWorktreePairOfOneTag(t *testing.T) {
	for _, tc := range []struct {
		name string
		main *RepositoryInfo
		wt   *RepositoryInfo
	}{
		{"checkout and its worktree", repo("alpha", false, "/src/alpha"), repo("alpha", true, "/wt/two")},
		{"two worktrees of one repo", repo("alpha", true, "/wt/one"), repo("alpha", true, "/wt/two")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := (&stableScenario{}).
				ws("w1", "chief-2.alpha.one", 1, tc.main).
				ws("w2", "chief-2.alpha.two", 2, tc.wt).
				agent("w1", "t1", "p1", 5, DisplayWorking).
				agent("w2", "t1", "p1", 1, DisplayWorking)
			v := s.build(SortStable)
			assert.Equal(t, []string{
				"# chief-2", "1 w1:p1", "1 w2:p1", "",
			}, outline(v), "git parentage does not split the tag")
			assert.Equal(t, map[string]int{"chief-2": 2}, tagGroups(v))
		})
	}
}

// TestStableTagHeaderOutlivesAMemberWithoutAgents: a tagged workspace runs no
// agent, and its sibling still draws the one header rather than a block of its
// own.
func TestStableTagHeaderOutlivesAMemberWithoutAgents(t *testing.T) {
	s := (&stableScenario{}).
		ws("w1", "chief-2.alpha.one", 1, nil).
		ws("w2", "chief-2.beta.two", 2, nil).
		agent("w1", "t1", "p1", 5, DisplayWorking)
	v := s.build(SortStable)
	assert.Equal(t, []string{"# chief-2", "1 w1:p1", ""}, outline(v))
	assert.Equal(t, map[string]int{"chief-2": 1}, tagGroups(v))
}

// TestStableTagCapLeavesTheBunnyIDIntact: bunny-chief-7 is 14 runes, under the
// 16-rune cap, so it groups exactly as a short tag does; no truncation can
// split it from a sibling.
func TestStableTagCapLeavesTheBunnyIDIntact(t *testing.T) {
	build := func(tag string) SidebarView {
		return (&stableScenario{}).
			ws("w1", tag+".alpha.one", 1, nil).
			ws("w2", tag+".beta.two", 2, nil).
			agent("w1", "t1", "p1", 5, DisplayWorking).
			agent("w2", "t1", "p1", 1, DisplayWorking).
			build(SortStable)
	}
	require.Len(t, "bunny-chief-9999", 16, "the longest bunny id fits the cap")
	short := build("chief-7")
	long := build("bunny-chief-7")
	assert.Equal(t, map[string]int{"chief-7": 2}, tagGroups(short))
	assert.Equal(t, map[string]int{"bunny-chief-7": 2}, tagGroups(long), "the whole tag, uncut")
	assert.Equal(t, 2, tagGroups(short)["chief-7"], "no split either way")
	assert.Equal(t, map[string]int{"bunny-chief-9999": 2},
		tagGroups(build("bunny-chief-9999")), "the cap boundary still groups")
}
