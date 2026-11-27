package application

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kriuchkov/herdr-scope/internal/domain"
	"github.com/kriuchkov/herdr-scope/internal/ports"
)

func frame(t *testing.T, f *fixture) FrameResult {
	t.Helper()
	result, err := f.observer.Frame(context.Background())
	require.NoError(t, err)
	return result
}

func TestFirstFrameReconciles(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusIdle))
	frame(t, f)
	assert.Equal(t, 1, f.herdr.snapshots)
	assert.Equal(t, 0, f.herdr.lists)
	assert.Equal(t, domain.DisplayIdle, f.sidebar.display("w1:p1"))
}

func TestTurnEndShowsDoneUntilFocused(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusWorking))
	frame(t, f)
	assert.Equal(t, domain.DisplayWorking, f.sidebar.display("w1:p1"))

	f.herdr.setStatus("w1:p1", domain.StatusIdle)
	f.clock.advance(2 * time.Second)
	frame(t, f)
	assert.Equal(t, domain.DisplayDone, f.sidebar.display("w1:p1"))

	f.herdr.update(func(s *domain.Session) { s.Agents[0].Focused = true })
	f.clock.advance(2 * time.Second)
	frame(t, f)
	assert.Equal(t, domain.DisplayIdleFresh, f.sidebar.display("w1:p1"))
}

func TestHeartbeatReadsTheAgentListWithoutEvents(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusIdle))
	frame(t, f)
	f.clock.advance(500 * time.Millisecond)
	frame(t, f)
	assert.Equal(t, 0, f.herdr.lists, "inside the heartbeat nothing is re-read")
	f.clock.advance(600 * time.Millisecond)
	frame(t, f)
	assert.Equal(t, 1, f.herdr.lists, "the heartbeat re-reads the agent list")
	f.clock.advance(31 * time.Second)
	frame(t, f)
	assert.Equal(t, 2, f.herdr.snapshots, "the reconcile interval re-reads the session")
}

func TestEventsOnlyMarkWhatToReread(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusIdle))
	frame(t, f)
	ev := domain.AgentEvent{Kind: domain.EventAgentStatusChanged, PaneID: "w1:p1", Status: domain.StatusWorking, Timestamp: f.clock.Now()}
	assert.True(t, f.observer.ProcessAgentEvent(ev))
	assert.Equal(t, domain.DisplayIdle, f.sidebar.display("w1:p1"), "an event changes nothing by itself")

	frame(t, f)
	assert.Equal(t, 1, f.herdr.lists, "the status event made the next frame read the agent list")
	assert.Equal(t, domain.DisplayIdle, f.sidebar.display("w1:p1"), "the authoritative list still says idle")

	f.observer.ProcessAgentEvent(domain.AgentEvent{Kind: domain.EventWorkspaceRenamed})
	frame(t, f)
	assert.Equal(t, 2, f.herdr.snapshots, "a structural event reconciles")
}

func TestReplayedEventsAreHarmless(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusWorking))
	frame(t, f)
	f.herdr.setStatus("w1:p1", domain.StatusIdle)
	f.clock.advance(2 * time.Second)
	frame(t, f)
	require.Equal(t, domain.DisplayDone, f.sidebar.display("w1:p1"))

	// A replayed backlog: a focus of the pane from long ago and duplicates.
	for range 3 {
		f.observer.ProcessAgentEvent(domain.AgentEvent{Kind: domain.EventPaneFocused, PaneID: "w1:p1"})
		f.observer.ProcessAgentEvent(domain.AgentEvent{Kind: domain.EventPaneExited, PaneID: "w1:p9"})
	}
	frame(t, f)
	assert.Equal(t, domain.DisplayDone, f.sidebar.display("w1:p1"), "a replayed focus did not acknowledge the tick")
}

func TestFailedReadSkipsTheFrameAndKeepsState(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusWorking))
	frame(t, f)
	f.herdr.setStatus("w1:p1", domain.StatusIdle)
	f.clock.advance(2 * time.Second)
	frame(t, f)
	require.Equal(t, domain.DisplayDone, f.sidebar.display("w1:p1"))

	f.herdr.fail = true
	f.observer.Invalidate()
	views := len(f.sidebar.views)
	result, err := f.observer.Frame(context.Background())
	require.ErrorIs(t, err, errDown)
	assert.Equal(t, f.clock.Now().Add(time.Second), result.Next, "a failed read is retried soon")
	assert.Len(t, f.sidebar.views, views, "a failed read is not an empty session")

	f.herdr.fail = false
	frame(t, f)
	assert.Equal(t, domain.DisplayDone, f.sidebar.display("w1:p1"), "the tick survived the outage")
}

func TestProjectionFailureKeepsMemory(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusWorking))
	frame(t, f)
	f.sidebar.fail = true
	f.herdr.setStatus("w1:p1", domain.StatusIdle)
	f.clock.advance(2 * time.Second)
	frame(t, f)
	f.sidebar.fail = false
	f.clock.advance(time.Second)
	frame(t, f)
	assert.Equal(t, domain.DisplayDone, f.sidebar.display("w1:p1"), "the frame that could not be written still advanced the state")
	assert.NotEmpty(t, f.observer.Stats().LastError)
}

func TestPaneMoveKeepsTheTick(t *testing.T) {
	f := newFixture(agentAt("term:t1", "w12:p1", domain.StatusWorking))
	frame(t, f)
	f.herdr.setStatus("w12:p1", domain.StatusIdle)
	f.clock.advance(2 * time.Second)
	frame(t, f)
	require.Equal(t, domain.DisplayDone, f.sidebar.display("w12:p1"))

	f.herdr.update(func(s *domain.Session) {
		s.Agents[0].Pane, s.Agents[0].Workspace = "w0:p2", "w0"
		s.Workspaces = append(s.Workspaces, domain.Workspace{ID: "w0", Label: "other"})
	})
	f.observer.Invalidate()
	f.clock.advance(time.Second)
	frame(t, f)
	assert.Equal(t, domain.DisplayDone, f.sidebar.display("w0:p2"), "the moved pane lost its tick")
}

func TestRestartRestoresMarkersAndActivity(t *testing.T) {
	f := newFixture(agentAt("claude:s1", "w1:p1", domain.StatusWorking))
	frame(t, f)
	f.herdr.setStatus("w1:p1", domain.StatusIdle)
	f.clock.advance(2 * time.Second)
	frame(t, f)
	f.observer.Flush(context.Background())
	require.Equal(t, domain.DisplayDone, f.sidebar.display("w1:p1"))

	// Herdr restarted: new pane id, new terminal, same agent session.
	records, err := f.state.Load(context.Background())
	require.NoError(t, err)
	f.herdr.update(func(s *domain.Session) { s.Agents[0].Pane, s.Agents[0].Terminal = "w1:p7", "t9" })
	f.observer = f.newObserver(records)
	f.activity.lookups = 0
	f.clock.advance(time.Hour)
	frame(t, f)
	assert.Equal(t, domain.DisplayDone, f.sidebar.display("w1:p7"), "the unseen tick survived the restart")
	assert.Zero(t, f.activity.lookups, "a persisted stamp needs no recovery")
}

func TestActivityRecoveryRunsOncePerAgent(t *testing.T) {
	f := newFixture(agentAt("claude:s1", "w1:p1", domain.StatusIdle))
	f.activity.stamps["claude:s1"] = f.clock.Now().Add(-3 * time.Hour)
	frame(t, f)
	assert.Equal(t, domain.DisplayIdleStale, f.sidebar.display("w1:p1"))
	f.clock.advance(2 * time.Second)
	frame(t, f)
	assert.Equal(t, 1, f.activity.lookups)
}

func TestActivityRecoveryWaitsForTheSessionID(t *testing.T) {
	detected := agentAt("term:t1", "w1:p1", domain.StatusIdle)
	detected.Session = ""
	f := newFixture(detected)
	f.activity.stamps["s1"] = f.clock.Now().Add(-3 * time.Hour)
	frame(t, f)
	assert.Equal(t, domain.DisplayIdle, f.sidebar.display("w1:p1"), "no session id: nothing to look up yet")
	assert.Zero(t, f.activity.lookups)

	// The session id is reported after detection: a new agent id, the same
	// terminal.
	f.herdr.update(func(s *domain.Session) { s.Agents[0].ID, s.Agents[0].Session = "claude:s1", "s1" })
	f.clock.advance(time.Second)
	frame(t, f)
	assert.Equal(t, domain.DisplayIdleStale, f.sidebar.display("w1:p1"), "the lookup ran once the session id arrived")
	assert.Equal(t, 1, f.activity.lookups)
}

func TestActivityRecoveryWithoutASourceIsDoneAtOnce(t *testing.T) {
	f := newFixture(agentAt("claude:s1", "w1:p1", domain.StatusIdle))
	f.observer.deps.Activity = nil
	frame(t, f)
	assert.False(t, f.observer.memory.Session("claude:s1").NeedsRecovery(), "nothing to ask: marked recovered")
}

func TestStateSavesAreDebounced(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusIdle))
	frame(t, f)
	assert.Equal(t, 1, f.state.saves)
	f.clock.advance(time.Second)
	frame(t, f)
	assert.Equal(t, 1, f.state.saves)
	f.clock.advance(5 * time.Second)
	frame(t, f)
	assert.Equal(t, 2, f.state.saves)
}

func TestStateSaveFailureDoesNotStopFrames(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusWorking))
	f.state.fail = true
	frame(t, f)
	assert.Equal(t, domain.DisplayWorking, f.sidebar.display("w1:p1"))
}

func TestNextFrameFollowsTheEarliestDeadline(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusWorking))
	result := frame(t, f)
	assert.Equal(t, f.clock.Now().Add(150*time.Millisecond), result.Next, "an animated row repaints at its next step")

	f.herdr.setStatus("w1:p1", domain.StatusIdle)
	f.observer.ProcessAgentEvent(domain.AgentEvent{Kind: domain.EventAgentStatusChanged})
	f.herdr.update(func(s *domain.Session) { s.Agents[0].Focused = true })
	result = frame(t, f)
	assert.Equal(t, f.clock.Now().Add(time.Second), result.Next, "an idle panel sleeps to its heartbeat")
}

// quiet is a fixture that re-reads Herdr only when told to, so that the
// frames in between are the animation's.
func quiet(agents ...domain.Agent) *fixture {
	f := newFixture(agents...)
	settings := f.observer.Settings()
	settings.Observer.Heartbeat, settings.Observer.ReconcileInterval = time.Hour, time.Hour
	f.observer.UpdateSettings(settings)
	return f
}

// projected reports whether the last frame built a new view rather than
// repaint the one before at a new moment.
func projected(t *testing.T, f *fixture) bool {
	t.Helper()
	views := f.sidebar.views
	require.GreaterOrEqual(t, len(views), 2)
	last, previous := views[len(views)-1], views[len(views)-2]
	require.NotEmpty(t, last.Groups)
	require.NotEmpty(t, previous.Groups)
	return &last.Groups[0] != &previous.Groups[0]
}

func TestAnimationFramesRepaintTheLastProjection(t *testing.T) {
	f := quiet(agentAt("a1", "w1:p1", domain.StatusWorking))
	frame(t, f)
	f.clock.advance(150 * time.Millisecond)
	result := frame(t, f)
	assert.False(t, projected(t, f), "nothing new: the same view, one animation step on")
	assert.Equal(t, f.clock.Now(), f.sidebar.last().At)
	assert.Equal(t, domain.DisplayWorking, f.sidebar.display("w1:p1"))
	assert.Equal(t, f.clock.Now().Add(150*time.Millisecond), result.Next, "and on to the next step")

	f.observer.ProcessAgentEvent(domain.AgentEvent{Kind: domain.EventAgentStatusChanged})
	f.clock.advance(150 * time.Millisecond)
	frame(t, f)
	assert.Equal(t, 1, f.herdr.lists)
	assert.False(t, projected(t, f), "a re-read that brings the same agents changes nothing")
}

func TestEveryInputOfTheProjectionRebuildsIt(t *testing.T) {
	changes := map[string]func(t *testing.T, f *fixture){
		"agent list": func(_ *testing.T, f *fixture) {
			f.herdr.update(func(s *domain.Session) { s.Agents[0].Title = "renamed" })
			f.observer.ProcessAgentEvent(domain.AgentEvent{Kind: domain.EventAgentStatusChanged})
		},
		"session":  func(_ *testing.T, f *fixture) { f.observer.Invalidate() },
		"settings": func(_ *testing.T, f *fixture) { f.observer.UpdateSettings(f.observer.Settings()) },
		"mode":     func(_ *testing.T, f *fixture) { f.observer.SetMode(domain.SortRecent) },
		"fold": func(t *testing.T, f *fixture) {
			_, _, err := f.observer.ToggleFold("w1")
			require.NoError(t, err)
		},
		"focus": func(_ *testing.T, f *fixture) {
			f.observer.ProcessAgentEvent(domain.AgentEvent{Kind: domain.EventWorkspaceFocused, Workspace: "w1"})
		},
		"host order":  func(_ *testing.T, f *fixture) { f.observer.deps.Host = fakeHost{grouped: false} },
		"next minute": func(_ *testing.T, f *fixture) { f.clock.advance(time.Minute) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			f := quiet(agentAt("a1", "w1:p1", domain.StatusWorking))
			frame(t, f)
			f.clock.advance(150 * time.Millisecond)
			frame(t, f)
			require.False(t, projected(t, f))
			change(t, f)
			f.clock.advance(150 * time.Millisecond)
			frame(t, f)
			assert.True(t, projected(t, f))
		})
	}
}

func TestTheGraceRunsFromTheLastAnimatedFrame(t *testing.T) {
	f := quiet(agentAt("a1", "w1:p1", domain.StatusWorking))
	settings := f.observer.Settings()
	settings.Policy.IdleGrace = 2500 * time.Millisecond
	f.observer.UpdateSettings(settings)
	frame(t, f)
	for range 6 {
		f.clock.advance(150 * time.Millisecond)
		frame(t, f)
	}
	require.False(t, projected(t, f))
	lastWorking := f.clock.Now()

	f.herdr.setStatus("w1:p1", domain.StatusIdle)
	f.observer.ProcessAgentEvent(domain.AgentEvent{Kind: domain.EventAgentStatusChanged})
	f.clock.advance(150 * time.Millisecond)
	frame(t, f)
	require.True(t, projected(t, f))
	assert.Equal(t, domain.DisplayWorking, f.sidebar.display("w1:p1"), "inside the grace")

	f.clock.advance(lastWorking.Add(2400 * time.Millisecond).Sub(f.clock.Now()))
	frame(t, f)
	assert.False(t, projected(t, f))
	assert.Equal(t, domain.DisplayWorking, f.sidebar.display("w1:p1"), "the grace counts from the last frame that saw the agent work")

	f.clock.advance(100 * time.Millisecond)
	frame(t, f)
	assert.True(t, projected(t, f), "the grace ran out: a deadline reached")
	assert.Equal(t, domain.DisplayDone, f.sidebar.display("w1:p1"))
}

func TestARepaintStillWakesForTheNextDeadline(t *testing.T) {
	f := quiet(agentAt("a1", "w1:p1", domain.StatusIdle))
	crossing := f.clock.Now().Add(10*time.Second + time.Millisecond)
	f.activity.stamps["a1"] = crossing.Add(-15*time.Minute - time.Millisecond)
	assert.Equal(t, crossing, frame(t, f).Next)
	assert.Equal(t, domain.DisplayIdleFresh, f.sidebar.display("w1:p1"))

	f.clock.advance(time.Second)
	assert.Equal(t, crossing, frame(t, f).Next, "a frame that only repaints wakes for the crossing too")
	assert.False(t, projected(t, f))

	f.clock.advance(crossing.Sub(f.clock.Now()))
	frame(t, f)
	assert.True(t, projected(t, f))
	assert.Equal(t, domain.DisplayIdle, f.sidebar.display("w1:p1"), "the crossing came: no longer fresh")
}

func TestModeChangesTheProjection(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusIdle))
	frame(t, f)
	assert.True(t, f.sidebar.last().Grouped)
	f.observer.SetMode(domain.SortRecent)
	frame(t, f)
	assert.False(t, f.sidebar.last().Grouped)
	assert.Equal(t, domain.SortRecent, f.sidebar.last().Mode)
}

func TestRepositoryFallbackForWorkspacesWithoutHerdrInfo(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusIdle))
	f.herdr.update(func(s *domain.Session) { s.Workspaces[0].Root = "/wt/feature" })
	resolver := &fakeResolver{info: domain.RepositoryInfo{ID: "/src/app/.git", Name: "app", Linked: true}}
	f.observer.deps.Repositories = resolver
	frame(t, f)
	frame(t, f)
	require.Len(t, f.sidebar.last().Groups, 1)
	assert.Equal(t, domain.TreeLastChild, f.sidebar.last().Groups[0].Tree, "the resolver's answer shaped the tree")
	assert.Equal(t, "app", f.sidebar.last().Groups[0].OrphanRepo)
	assert.Equal(t, 1, resolver.calls, "lookups are cached")
}

type fakeResolver struct {
	info  domain.RepositoryInfo
	calls int
}

func (r *fakeResolver) Resolve(context.Context, string) (domain.RepositoryInfo, error) {
	r.calls++
	return r.info, nil
}

type fakeMover struct{ moves [][]domain.WorkspaceID }

func (m *fakeMover) MoveWorkspaces(_ context.Context, order []domain.WorkspaceID) ([]domain.WorkspaceID, error) {
	m.moves = append(m.moves, order)
	return order, nil
}

func TestNestedFolderIsMovedUnderItsParentWithoutReorder(t *testing.T) {
	for _, nest := range []bool{true, false} {
		f := newFixture(agentAt("a1", "w1:p1", domain.StatusIdle))
		f.herdr.session.Workspaces = []domain.Workspace{
			{ID: "w1", Label: "acme", Root: "/home/user/acme"},
			{ID: "w2", Label: "notes", Root: "/home/user/src/notes"},
			{ID: "w3", Label: "billing", Root: "/home/user/acme/services/billing"},
		}
		f.herdr.session.Home = "/home/user"
		mover := &fakeMover{}
		settings := ports.DefaultSettings()
		settings.Policy.NestFolders = nest
		f.observer = NewObserver(ObserverDeps{
			Agents: f.herdr, Session: f.herdr, Activity: f.activity, State: f.state,
			Sidebar: f.sidebar, Host: fakeHost{grouped: true}, Mover: mover, Clock: f.clock, Logger: nopLogger{},
		}, settings, domain.SortActive, nil)
		frame(t, f)
		if !nest {
			assert.Empty(t, mover.moves, "nest_folders off and reorder_workspaces off: nothing moves")
			continue
		}
		assert.Equal(t, [][]domain.WorkspaceID{{"w1", "w3", "w2"}}, mover.moves, "only the nested folder moves, under its parent")
	}
}

func TestStableMovesNoWorkspaceEvenWithNesting(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusIdle))
	f.herdr.session.Workspaces = []domain.Workspace{
		{ID: "w1", Label: "acme", Root: "/home/user/acme"},
		{ID: "w2", Label: "notes", Root: "/home/user/src/notes"},
		{ID: "w3", Label: "billing", Root: "/home/user/acme/services/billing"},
	}
	f.herdr.session.Home = "/home/user"
	mover := &fakeMover{}
	settings := ports.DefaultSettings()
	settings.Policy.NestFolders = true
	settings.ReorderWorkspaces = true
	f.observer = NewObserver(ObserverDeps{
		Agents: f.herdr, Session: f.herdr, Activity: f.activity, State: f.state,
		Sidebar: f.sidebar, Host: fakeHost{grouped: true}, Mover: mover, Clock: f.clock, Logger: nopLogger{},
	}, settings, domain.SortStable, nil)
	frame(t, f)
	assert.Empty(t, mover.moves, "stable's keys are Herdr's numbers, and a move renumbers them")
}

func TestHereFollowsTheFocusToAnotherFamily(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusIdle))
	f.herdr.session.Workspaces = []domain.Workspace{
		{ID: "w1", Label: "acme", Root: "/home/user/acme"},
		{ID: "w2", Label: "notes", Root: "/home/user/src/notes"},
		{ID: "w3", Label: "billing", Root: "/home/user/acme/services/billing"},
	}
	f.herdr.session.FocusedWorkspace = "w1"
	f.observer.SetMode(domain.SortHere)
	frame(t, f)
	assert.Equal(t, [][]domain.WorkspaceID{{"w1", "w3"}}, f.sidebar.families, "acme with the folder in it")

	f.clock.advance(time.Second)
	frame(t, f)
	assert.Len(t, f.sidebar.families, 1, "the same family is not set again")

	f.observer.ProcessAgentEvent(domain.AgentEvent{Kind: domain.EventWorkspaceFocused, Workspace: "w2"})
	f.clock.advance(time.Second)
	frame(t, f)
	assert.Equal(t, []domain.WorkspaceID{"w2"}, f.sidebar.families[1], "notes alone")

	f.observer.SetMode(domain.SortActive)
	f.observer.ProcessAgentEvent(domain.AgentEvent{Kind: domain.EventWorkspaceFocused, Workspace: "w1"})
	f.clock.advance(time.Second)
	frame(t, f)
	assert.Len(t, f.sidebar.families, 2, "outside here the focus does not touch the view")
}

func TestTheTreeFollowsTheSettings(t *testing.T) {
	f := newFixture(agentAt("a1", "w1:p1", domain.StatusIdle))
	f.herdr.session.Workspaces = []domain.Workspace{
		{ID: "w1", Label: "acme", Root: "/home/user/acme"},
		{ID: "w3", Label: "billing", Root: "/home/user/acme/services/billing"},
	}
	f.herdr.session.FocusedWorkspace = "w1"
	frame(t, f)
	assert.Equal(t, []domain.WorkspaceID{"w1", "w3"}, f.observer.HereFamily())

	settings := f.observer.Settings()
	settings.Policy.NestFolders = false
	f.observer.UpdateSettings(settings)
	assert.Equal(t, []domain.WorkspaceID{"w1"}, f.observer.HereFamily(), "nest_folders off unnests at once, no reconcile needed")
}

func TestFoldHidesTheFamilyAndIsRemembered(t *testing.T) {
	acme := agentAt("a1", "w1:p1", domain.StatusIdle)
	billing := agentAt("a2", "w3:p1", domain.StatusIdle)
	billing.Workspace, billing.Tab = "w3", "w3:t1"
	notes := agentAt("a3", "w2:p1", domain.StatusIdle)
	notes.Workspace, notes.Tab = "w2", "w2:t1"
	f := newFixture(acme, billing, notes)
	f.herdr.session.Workspaces = []domain.Workspace{
		{ID: "w1", Label: "acme", Root: "/home/user/acme"},
		{ID: "w2", Label: "notes", Root: "/home/user/src/notes"},
		{ID: "w3", Label: "billing", Root: "/home/user/acme/services/billing"},
	}
	f.herdr.session.FocusedWorkspace = "w2"
	folds := &fakeFolds{}
	deps := ObserverDeps{
		Agents: f.herdr, Session: f.herdr, Activity: f.activity, State: f.state,
		Sidebar: f.sidebar, Host: fakeHost{grouped: true}, Folds: folds, Clock: f.clock, Logger: nopLogger{},
	}
	f.observer = NewObserver(deps, ports.DefaultSettings(), domain.SortActive, nil)
	frame(t, f)
	assert.Empty(t, f.sidebar.last().Folded)

	family, folded, err := f.observer.ToggleFold("w3")
	require.NoError(t, err)
	assert.Equal(t, "acme", family, "a nested folder folds its whole family")
	assert.True(t, folded)
	assert.Equal(t, []string{"/home/user/acme"}, folds.keys, "remembered by folder, which outlives a renumbering")
	f.clock.advance(time.Second)
	frame(t, f)
	assert.Len(t, f.sidebar.last().Folded, 1, "one row out of the panel, the other carries the fold line")

	f.observer = NewObserver(deps, ports.DefaultSettings(), domain.SortActive, nil)
	f.clock.advance(time.Second)
	frame(t, f)
	assert.Len(t, f.sidebar.last().Folded, 1, "a restarted daemon keeps the fold")

	family, folded, err = f.observer.ToggleFold("")
	require.NoError(t, err)
	assert.Equal(t, "notes", family, "no workspace given: the focus the daemon tracks")
	assert.True(t, folded)
	_, folded, err = f.observer.ToggleFold("w1")
	require.NoError(t, err)
	assert.False(t, folded, "again: unfolded")
	assert.Equal(t, []string{"/home/user/src/notes"}, folds.keys)

	_, _, err = f.observer.ToggleFold("w9")
	require.ErrorIs(t, err, domain.ErrConfiguration, "a workspace the session does not know")
}

func TestGoingToAFoldLineOpensItForTheVisit(t *testing.T) {
	acme := agentAt("a1", "w1:p1", domain.StatusIdle)
	billing := agentAt("a2", "w3:p1", domain.StatusIdle)
	billing.Workspace, billing.Tab = "w3", "w3:t1"
	notes := agentAt("a3", "w2:p1", domain.StatusIdle)
	notes.Workspace, notes.Tab, notes.Focused = "w2", "w2:t1", true
	f := newFixture(acme, billing, notes)
	f.herdr.session.Workspaces = []domain.Workspace{
		{ID: "w1", Label: "acme", Root: "/home/user/acme"},
		{ID: "w2", Label: "notes", Root: "/home/user/src/notes"},
		{ID: "w3", Label: "billing", Root: "/home/user/acme/services/billing"},
	}
	f.herdr.session.FocusedWorkspace = "w2"
	folds := &fakeFolds{keys: []string{"/home/user/acme"}}
	f.observer = NewObserver(ObserverDeps{
		Agents: f.herdr, Session: f.herdr, Activity: f.activity, State: f.state,
		Sidebar: f.sidebar, Host: fakeHost{grouped: true}, Folds: folds, Clock: f.clock, Logger: nopLogger{},
	}, ports.DefaultSettings(), domain.SortActive, nil)
	frame(t, f)
	require.Len(t, f.sidebar.last().Folded, 1)

	focus := func(pane domain.PaneID, ws domain.WorkspaceID) {
		for i := range f.herdr.session.Agents {
			f.herdr.session.Agents[i].Focused = f.herdr.session.Agents[i].Pane == pane
		}
		f.observer.ProcessAgentEvent(domain.AgentEvent{Kind: domain.EventPaneFocused, PaneID: pane, Workspace: ws})
		f.clock.advance(time.Second)
		frame(t, f)
	}
	line := func() *domain.Fold {
		for _, g := range f.sidebar.last().Groups {
			if r := g.Rows[0]; r.Fold != nil {
				return r.Fold
			}
		}
		return nil
	}
	focus("w2:p1", "w2")
	require.NotNil(t, line())
	assert.False(t, line().Open, "focus on a shown row is no visit to the line")

	focus("w1:p1", "w1")
	assert.Empty(t, f.sidebar.last().Folded, "a click on the line focuses the agent carrying it: the family opens")
	assert.True(t, line().Open, "and says it folds again")
	assert.Equal(t, []string{"/home/user/acme"}, folds.keys, "the fold itself stays")

	focus("w3:p1", "w3")
	assert.Empty(t, f.sidebar.last().Folded, "still open anywhere in the family")
	focus("w2:p1", "w2")
	assert.Len(t, f.sidebar.last().Folded, 1, "folded again once the focus left")
	assert.False(t, line().Open)

	focus("w1:p1", "w1")
	_, folded, err := f.observer.ToggleFold("w1")
	require.NoError(t, err)
	assert.True(t, folded, "the key folds a family open for a visit at once")
	f.clock.advance(time.Second)
	frame(t, f)
	require.NotNil(t, line())
	assert.True(t, line().Alone, "billing carries the line again; the focused workspace stays")
	assert.False(t, line().Open)
	_, folded, err = f.observer.ToggleFold("w1")
	require.NoError(t, err)
	assert.False(t, folded, "again: unfolded for good")
	assert.Empty(t, folds.keys)
}

type fakeRunLedger struct {
	records []ports.RunRecord
	err     error
	calls   int
}

func (l *fakeRunLedger) List(context.Context) ([]ports.RunRecord, error) {
	l.calls++
	if l.err != nil {
		return nil, l.err
	}
	return append([]ports.RunRecord(nil), l.records...), nil
}

func TestObserverRunLedgerLifecycle(t *testing.T) {
	agent := agentAt("a1", "w1:p1", domain.StatusIdle)
	f := newFixture(agent)
	ledger := &fakeRunLedger{
		records: []ports.RunRecord{{
			RunID:      "run-1",
			WorkerName: "worker-1",
			State:      "running",
			TerminalID: string(agent.Terminal),
		}},
	}
	settings := ports.DefaultSettings()
	settings.Policy.IdleGrace = 0
	f.observer = NewObserver(ObserverDeps{
		Agents: f.herdr, Session: f.herdr, Activity: f.activity, State: f.state,
		Sidebar: f.sidebar, Host: fakeHost{grouped: true}, RunLedger: ledger, Clock: f.clock, Logger: nopLogger{},
	}, settings, domain.SortTriage, nil)

	// Empty RunRoot skips RunLedger and leaves Run nil.
	frame(t, f)
	assert.Zero(t, ledger.calls)
	assert.Nil(t, f.sidebar.last().Groups[0].Rows[0].Run)

	// Setting RunRoot reads RunLedger and populates SidebarRow.Run.
	settings.RunRoot = "/tmp/runs"
	f.observer.UpdateSettings(settings)
	f.clock.advance(time.Second)
	frame(t, f)
	assert.Equal(t, 1, ledger.calls)
	require.NotNil(t, f.sidebar.last().Groups[0].Rows[0].Run)
	assert.Equal(t, "run-1", f.sidebar.last().Groups[0].Rows[0].Run.RunID)
	assert.Equal(t, "running", f.sidebar.last().Groups[0].Rows[0].Run.State)

	// Run changes trigger a rebuild on the next refresh.
	ledger.records[0].SignalStatus = "failed"
	ledger.records[0].SignalReceivedAt = f.clock.Now()
	f.clock.advance(time.Second)
	frame(t, f)
	assert.True(t, projected(t, f))
	assert.Equal(t, "failed", f.sidebar.last().Groups[0].Rows[0].Run.SignalStatus)

	// A RunLedger read error preserves previous matched runs.
	ledger.err = errDown
	f.clock.advance(time.Second)
	frame(t, f)
	require.NotNil(t, f.sidebar.last().Groups[0].Rows[0].Run)
	assert.Equal(t, "run-1", f.sidebar.last().Groups[0].Rows[0].Run.RunID)

	// Clearing RunRoot via UpdateSettings clears runs immediately.
	ledger.err = nil
	settings.RunRoot = ""
	f.observer.UpdateSettings(settings)
	f.clock.advance(time.Second)
	frame(t, f)
	assert.Nil(t, f.sidebar.last().Groups[0].Rows[0].Run)
}

// pinRow finds a pane's painted row: its index, keys and display.
func pinRow(t *testing.T, v domain.SidebarView, pane domain.PaneID) (int, domain.SortKeys, domain.DisplayState) {
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
	t.Fatalf("row %s not painted", pane)
	return 0, domain.SortKeys{}, ""
}

// focusFixture is a worktree family whose checked-off member leads it: the
// checkout (w1), its worktree w2 carrying the unseen ✓, and a working
// neighbour (w3) that the family would otherwise trail.
func focusFixture(t *testing.T) *fixture {
	t.Helper()
	repo := func(checkout string, linked bool) *domain.RepositoryInfo {
		return &domain.RepositoryInfo{ID: "/src/app/.git", Name: "app", Root: "/src/app", Checkout: domain.WorktreeID(checkout), Linked: linked}
	}
	agents := []domain.Agent{
		{ID: "a1", Pane: "w1:p1", Tab: "w1:t1", Workspace: "w1", Terminal: "t1", Kind: "claude", Status: domain.StatusIdle, Title: "checkout"},
		{ID: "a2", Pane: "w2:p2", Tab: "w2:t1", Workspace: "w2", Terminal: "t2", Kind: "claude", Status: domain.StatusIdle, Showing: domain.DisplayDone, Title: "done"},
		{ID: "a3", Pane: "w3:p3", Tab: "w3:t1", Workspace: "w3", Terminal: "t3", Kind: "claude", Status: domain.StatusWorking, Title: "busy"},
	}
	f := newFixture(agents...)
	f.herdr.session.Workspaces = []domain.Workspace{
		{ID: "w1", Label: "app", Repository: repo("/src/app", false)},
		{ID: "w2", Label: "app-wt", Repository: repo("/src/app-wt", true)},
		{ID: "w3", Label: "other"},
	}
	f.herdr.session.Tabs = []domain.Tab{
		{ID: "w1:t1", Workspace: "w1", Label: "1"},
		{ID: "w2:t1", Workspace: "w2", Label: "1"},
		{ID: "w3:t1", Workspace: "w3", Label: "1"},
	}
	f.observer.SetMode(domain.SortTriage)
	return f
}

func TestFocusFreezesRowKeys(t *testing.T) {
	f := focusFixture(t)
	frame(t, f)

	at, keys, display := pinRow(t, f.sidebar.last(), "w2:p2")
	require.Equal(t, domain.DisplayDone, display, "the ✓ leads its family before the click")

	// The click: the pane gains focus, the ✓ clears, the host republishes the
	// row's tokens as idle.
	f.herdr.update(func(s *domain.Session) {
		s.Agents[1].Focused = true
		s.Agents[1].Showing = domain.DisplayIdle
	})
	f.clock.advance(2 * time.Second)
	frame(t, f)

	gotAt, gotKeys, gotDisplay := pinRow(t, f.sidebar.last(), "w2:p2")
	assert.Equal(t, at, gotAt, "the focused row keeps its place")
	assert.Equal(t, keys, gotKeys, "its keys are frozen to the pre-focus frame")
	assert.Equal(t, domain.DisplayIdle, gotDisplay, "the paint stays honest: ✓ cleared, row token reads idle")

	checkoutAt := func() int {
		n, _, _ := pinRow(t, f.sidebar.last(), "w1:p1")
		return n
	}
	before := checkoutAt()

	// A real new turn while focused releases the freeze at once: the row sorts
	// by its own key and its family re-sorts around it, at once, not on the
	// next focus.
	f.herdr.setStatus("w2:p2", domain.StatusWorking)
	f.clock.advance(2 * time.Second)
	frame(t, f)
	turnAt, turnKeys, turnDisplay := pinRow(t, f.sidebar.last(), "w2:p2")
	assert.Equal(t, domain.DisplayWorking, turnDisplay)
	assert.NotEqual(t, keys, turnKeys, "a new turn sorts by its own key")
	assert.NotEqual(t, at, turnAt, "and moves the row at once, not on the next focus")
	assert.NotEqual(t, before, checkoutAt(), "its family moves with it")

	// Losing focus drops the pin for good: the row then sorts by its own key.
	f.herdr.update(func(s *domain.Session) { s.Agents[1].Focused = false })
	f.clock.advance(2 * time.Second)
	frame(t, f)
	assert.Empty(t, f.observer.pins, "losing focus drops the pin")
	_, releasedKeys, _ := pinRow(t, f.sidebar.last(), "w2:p2")
	assert.Equal(t, turnKeys, releasedKeys, "with no pin the row keeps sorting by its own key")
}
