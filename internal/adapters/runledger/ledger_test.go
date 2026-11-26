package runledger

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kriuchkov/herdr-scope/internal/domain"
	"github.com/kriuchkov/herdr-scope/internal/ports"
)

type spyLogger struct {
	debugs []string
}

func (l *spyLogger) Debug(msg string, _ ...any) { l.debugs = append(l.debugs, msg) }
func (l *spyLogger) Info(string, ...any)        {}
func (l *spyLogger) Warn(string, ...any)        {}
func (l *spyLogger) Error(string, ...any)       {}

func writeFixture(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestLedgerListParsesValidAndSkipsBadFiles(t *testing.T) {
	dir := t.TempDir()
	logger := &spyLogger{}
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

	// Subdirectory and non-JSON file must be ignored without recursing.
	sub := filepath.Join(dir, "nested")
	require.NoError(t, os.Mkdir(sub, 0o700))
	writeFixture(t, sub, "ignored.json", `{"runId":"nested","workers":[{"workerName":"w"}]}`)
	writeFixture(t, dir, "notes.txt", "not json")

	// Corrupt JSON, missing workers, and missing runId are skipped with debug logs.
	writeFixture(t, dir, "corrupt.json", "{bad json")
	writeFixture(t, dir, "no-workers.json", `{"runId":"r-empty"}`)
	writeFixture(t, dir, "no-runid.json", `{"workers":[{"workerName":"w"}]}`)

	// Valid active run with unknown state preserved.
	writeFixture(t, dir, "run-older.json", `{
		"runId": "run-1",
		"timestamp": "2026-11-26T09:00:00Z",
		"owner": {"pane": "w0:p1"},
		"workers": [{
			"workerName": "worker-a",
			"repoId": "herdr-scope",
			"machine": "devbox",
			"runtime": "agy",
			"state": "awaiting-review",
			"paneId": "w1:p1",
			"terminalId": "t1",
			"stepStartedAt": "2026-11-26T09:05:00Z",
			"lastSignal": {
				"status": "done",
				"summary": "ready",
				"receivedAt": "2026-11-26T09:10:00Z"
			}
		}]
	}`)

	// Closed run maps closedAt -> "closed" unless stopped/failed; worker runId overrides wave runId.
	writeFixture(t, dir, "run-newer.json", `{
		"runId": "wave-2",
		"timestamp": "2026-11-26T09:30:00Z",
		"workers": [{
			"runId": "run-2",
			"workerName": "worker-b",
			"state": "running",
			"closedAt": "2026-11-26T09:40:00Z",
			"paneId": "w1:p2"
		}]
	}`)

	// Closed run with failed state keeps "failed".
	writeFixture(t, dir, "run-failed.json", `{
		"runId": "run-3",
		"timestamp": "2026-11-26T08:00:00Z",
		"workers": [{
			"workerName": "worker-c",
			"state": "failed",
			"closedAt": "2026-11-26T08:05:00Z",
			"lastSignal": {"status": "failed"}
		}]
	}`)

	ledger := New(dir, logger, func() time.Time { return now })
	records, err := ledger.List(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 3)

	assert.Equal(t, []string{"run-2", "run-1", "run-3"}, []string{records[0].RunID, records[1].RunID, records[2].RunID})
	assert.Equal(t, "closed", records[0].State)
	assert.Equal(t, "awaiting-review", records[1].State)
	assert.Equal(t, "done", records[1].SignalStatus)
	assert.Equal(t, "w0:p1", records[1].OwnerPane)
	assert.Equal(t, "failed", records[2].State)
	assert.Len(t, logger.debugs, 3, "corrupt, missing-workers, and missing-runId each emit a debug log")
}

func TestLedgerMissingAndEmptyRoot(t *testing.T) {
	logger := &spyLogger{}
	empty := New("", logger, time.Now)
	recs, err := empty.List(context.Background())
	require.NoError(t, err)
	assert.Nil(t, recs)

	missing := New(filepath.Join(t.TempDir(), "does-not-exist"), logger, time.Now)
	recs, err = missing.List(context.Background())
	require.NoError(t, err)
	assert.Nil(t, recs)
	assert.NotEmpty(t, logger.debugs)
}

func TestLedgerCachesByMtimeAndThirtySecondThrottle(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	ledger := New(dir, nil, func() time.Time { return now })

	t0 := now
	writeFixture(t, dir, "r1.json", `{"runId":"r1","timestamp":"2026-11-26T09:00:00Z","workers":[{"workerName":"w1"}]}`)
	require.NoError(t, os.Chtimes(dir, t0, t0))

	recs, err := ledger.List(context.Background())
	require.NoError(t, err)
	require.Len(t, recs, 1)

	// New file changes dir mtime, but within 30s the cache is returned without re-reading.
	writeFixture(t, dir, "r2.json", `{"runId":"r2","timestamp":"2026-11-26T09:05:00Z","workers":[{"workerName":"w2"}]}`)
	t1 := t0.Add(10 * time.Second)
	require.NoError(t, os.Chtimes(dir, t1, t1))
	now = t1

	recs, err = ledger.List(context.Background())
	require.NoError(t, err)
	assert.Len(t, recs, 1, "inside 30s window, cached records are kept")

	// Past 30s with changed mtime, the directory is re-read.
	now = t0.Add(31 * time.Second)
	recs, err = ledger.List(context.Background())
	require.NoError(t, err)
	assert.Len(t, recs, 2, "past 30s with new mtime, new file is read")

	// SetRoot to a new root invalidates cache immediately.
	dir2 := t.TempDir()
	ledger.SetRoot(dir2)
	recs, err = ledger.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, recs)
}

func TestMatchPrefersTerminalIDAndNewestWave(t *testing.T) {
	agents := []domain.Agent{
		{ID: "a1", Pane: "w1:p1", Terminal: "term-1"},
		{ID: "a2", Pane: "w1:p2", Terminal: "term-2"},
	}
	records := []ports.RunRecord{
		{
			RunID:      "run-term-newest",
			TerminalID: "term-1",
			PaneID:     "w9:p9",
			State:      "running",
		},
		{
			RunID:      "run-pane-older",
			TerminalID: "other-term",
			PaneID:     "w1:p1",
			State:      "closed",
		},
		{
			RunID:  "run-pane-only",
			PaneID: "w1:p2",
			State:  "awaiting-review",
		},
	}

	matched := Match(agents, records)
	require.Len(t, matched, 2)
	assert.Equal(t, "run-term-newest", matched["w1:p1"].RunID, "terminal ID match beats pane ID match")
	assert.Equal(t, "run-pane-only", matched["w1:p2"].RunID, "falls back to pane ID when terminal ID is absent")
}

func TestLedgerRetentionWindow(t *testing.T) {
	const (
		validBody   = `{"runId":"r-1","timestamp":"2026-11-26T09:00:00Z","workers":[{"workerName":"w","state":"running","paneId":"w1:p1"}]}`
		closedBody  = `{"runId":"r-2","timestamp":"2026-11-26T09:00:00Z","workers":[{"workerName":"w","state":"running","closedAt":"2026-11-26T09:30:00Z","paneId":"w1:p2"}]}`
		corruptBody = "{bad json"
	)
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		body        string
		age         time.Duration // mtime = now - age; negative is a future mtime
		wantRecords int
		wantState   string
		// wantOpened is proven through the corrupt body: "skip corrupt run
		// file" can only be logged if ReadFile ran. A permission stub is not
		// usable here because Windows turns 0o000 into read-only, not
		// unreadable, so the debug log is the open-counter on every OS.
		wantOpened bool
	}{
		{"stale valid file is skipped unopened", validBody, 25 * time.Hour, 0, "", false},
		{"stale corrupt file is skipped unopened", corruptBody, 25 * time.Hour, 0, "", false},
		{"fresh valid file is read", validBody, time.Hour, 1, "running", false},
		{"fresh corrupt file is opened and logged", corruptBody, time.Hour, 0, "", true},
		{"mtime exactly at the window edge is read", corruptBody, defaultRetention, 0, "", true},
		{"future mtime is read", validBody, -time.Hour, 1, "running", false},
		{"closed run with fresh mtime is still read", closedBody, time.Hour, 1, "closed", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			logger := &spyLogger{}
			path := writeFixture(t, dir, "f.json", tc.body)
			mtime := now.Add(-tc.age)
			require.NoError(t, os.Chtimes(path, mtime, mtime))

			ledger := New(dir, logger, func() time.Time { return now })
			records, err := ledger.List(context.Background())
			require.NoError(t, err)
			assert.Len(t, records, tc.wantRecords)
			if tc.wantState != "" {
				assert.Equal(t, tc.wantState, records[0].State)
			}
			assert.Equal(t, tc.wantOpened, slices.Contains(logger.debugs, "skip corrupt run file"),
				"the corrupt-file debug line proves whether the file was opened")
		})
	}
}

func TestLedgerSetRetentionDisablesAndInvalidates(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	path := writeFixture(t, dir, "old.json",
		`{"runId":"r-old","timestamp":"2026-11-26T09:00:00Z","workers":[{"workerName":"w"}]}`)
	mtime := now.Add(-25 * time.Hour)
	require.NoError(t, os.Chtimes(path, mtime, mtime))

	ledger := New(dir, &spyLogger{}, func() time.Time { return now })
	records, err := ledger.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, records, "the default 24h window skips a 25h-old file")

	ledger.SetRetention(0)
	records, err = ledger.List(context.Background())
	require.NoError(t, err)
	assert.Len(t, records, 1, "a disabled window reads every file and SetRetention drops the cache")
}
