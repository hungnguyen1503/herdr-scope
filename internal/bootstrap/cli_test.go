package bootstrap

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kriuchkov/herdr-scope/internal/domain"
	"github.com/kriuchkov/herdr-scope/internal/ports"
)

func TestWriteRunsTableJoinsTerminalAndPaneIDs(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 10, 0, 0, time.UTC)
	records := []ports.RunRecord{
		{
			RunID:            "run-term",
			WorkerName:       "alpha",
			State:            "running",
			SignalStatus:     "done",
			SignalReceivedAt: now.Add(-4 * time.Minute),
			TerminalID:       "term-1",
			PaneID:           "w9:p9",
		},
		{
			RunID:         "run-pane",
			WorkerName:    "beta",
			State:         "closed",
			StepStartedAt: now.Add(-2 * time.Hour),
			PaneID:        "w2:p1",
		},
		{
			RunID: "run-unmatched",
		},
	}
	agents := []domain.Agent{
		{Pane: "w1:p1", Terminal: "term-1"},
		{Pane: "w2:p1", Terminal: "term-2"},
	}

	var out bytes.Buffer
	require.NoError(t, writeRunsTable(&out, now, records, agents))
	want := "RUN ID         WORKER  STATE    SIGNAL  AGE  PANE\n" +
		"run-term       alpha   running  done    4m   w1:p1\n" +
		"run-pane       beta    closed   -       2h   w2:p1\n" +
		"run-unmatched  -       -        -       -    -\n"
	assert.Equal(t, want, out.String())
}

func TestMainRunsCommand(t *testing.T) {
	cfgDir := t.TempDir()
	stateDir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", cfgDir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(t.TempDir(), "missing.sock"))

	var stdout, stderr bytes.Buffer
	code := Main(t.Context(), Build{Version: "test"}, []string{"runs"}, &stdout, &stderr)
	assert.Zero(t, code)
	assert.Empty(t, stderr.String())
	assert.Contains(t, stdout.String(), "run_root is not configured")

	runDir := t.TempDir()
	wave := `{
  "runId": "run-cli-1",
  "timestamp": "2026-11-26T12:00:00Z",
  "workers": [{
    "runId": "run-cli-1",
    "workerName": "cli-worker",
    "state": "running",
    "lastSignal": {
      "status": "blocked",
      "receivedAt": "2026-11-26T12:05:00Z"
    }
  }]
}`
	require.NoError(t, os.WriteFile(filepath.Join(runDir, "run-cli-1.json"), []byte(wave), 0o600))

	stdout.Reset()
	stderr.Reset()
	code = Main(t.Context(), Build{Version: "test"}, []string{"runs", "--run-root", runDir}, &stdout, &stderr)
	assert.Zero(t, code)
	assert.Empty(t, stderr.String())
	assert.Contains(t, stdout.String(), "run-cli-1")
	assert.Contains(t, stdout.String(), "cli-worker")
	assert.Contains(t, stdout.String(), "blocked")
}
