// Package runledger reads Bunny orchestration run-ledger JSON files from a
// configured directory and joins them to live Herdr panes.
package runledger

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kriuchkov/herdr-scope/internal/domain"
	"github.com/kriuchkov/herdr-scope/internal/ports"
)

const cacheTTL = 30 * time.Second

// defaultRetention is the mtime window List applies by default: a .json last
// written longer ago cannot describe a live pane, so it is skipped from the
// directory listing without being opened (reading all 3432 files of a busy
// ledger cost the daemon a 21 s stall).
const defaultRetention = 24 * time.Hour

// Ledger reads and caches Bunny wave run files in a flat directory.
type Ledger struct {
	mu     sync.Mutex
	root   string
	logger ports.Logger
	clock  func() time.Time

	// retention is the mtime window; zero or negative disables it and every
	// .json is read, the pre-window behaviour.
	retention time.Duration

	loaded   bool
	dirMtime time.Time
	lastRead time.Time
	records  []ports.RunRecord
}

// New builds a RunLedger reading <root>/*.json, newest-of-window first.
func New(root string, logger ports.Logger, clock func() time.Time) *Ledger {
	if clock == nil {
		clock = time.Now
	}
	return &Ledger{
		root:      root,
		logger:    logger,
		clock:     clock,
		retention: defaultRetention,
	}
}

// SetRoot updates the directory path and invalidates any cached read when it changes.
func (l *Ledger) SetRoot(root string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.root == root {
		return
	}
	l.root = root
	l.loaded = false
	l.dirMtime = time.Time{}
	l.lastRead = time.Time{}
	l.records = nil
}

// SetRetention changes the mtime window and invalidates the cached read:
// records kept under another window would drop files that just came back
// into scope. Tests drive it with a fake clock; zero disables the window.
func (l *Ledger) SetRetention(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.retention == d {
		return
	}
	l.retention = d
	l.loaded = false
	l.dirMtime = time.Time{}
	l.lastRead = time.Time{}
	l.records = nil
}

type waveFile struct {
	RunID     string `json:"runId"`
	Timestamp string `json:"timestamp"`
	Owner     struct {
		Pane string `json:"pane"`
	} `json:"owner"`
	Workers []struct {
		RunID         string `json:"runId"`
		WorkerName    string `json:"workerName"`
		RepoID        string `json:"repoId"`
		Machine       string `json:"machine"`
		Runtime       string `json:"runtime"`
		State         string `json:"state"`
		ClosedAt      string `json:"closedAt"`
		PaneID        string `json:"paneId"`
		TerminalID    string `json:"terminalId"`
		StepStartedAt string `json:"stepStartedAt"`
		LastSignal    *struct {
			Status     string `json:"status"`
			Summary    string `json:"summary"`
			ReceivedAt string `json:"receivedAt"`
		} `json:"lastSignal"`
	} `json:"workers"`
}

// List returns run records from <root>/*.json, newest wave first, reading only
// files whose mtime is inside the retention window. Directory reads are cached
// by mtime and throttled to at most once every 30 seconds.
func (l *Ledger) List(ctx context.Context) ([]ports.RunRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("runledger list: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.root == "" {
		return nil, nil
	}
	info, err := os.Stat(l.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			l.debug("run_root does not exist", "path", l.root)
			return nil, nil
		}
		return nil, fmt.Errorf("stat run_root %s: %w", l.root, err)
	}
	now := l.clock()
	mtime := info.ModTime()
	if l.loaded {
		if mtime.Equal(l.dirMtime) || now.Sub(l.lastRead) < cacheTTL {
			return slices.Clone(l.records), nil
		}
	}

	entries, err := os.ReadDir(l.root)
	if err != nil {
		return nil, fmt.Errorf("read run_root %s: %w", l.root, err)
	}
	var records []ports.RunRecord
	for _, entry := range entries {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("runledger list: %w", ctxErr)
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(l.root, entry.Name())
		// Stale files are dropped from the listing by mtime alone, before any
		// open: state lives inside the JSON, so the listing cannot tell a
		// closed run from a running one — a closed run whose file was touched
		// within the window is therefore still read, and that is deliberate,
		// because the freshest close is exactly what `runs` and a lingering
		// pane still need to see.
		if l.retention > 0 {
			fi, infoErr := entry.Info()
			if infoErr != nil {
				l.debug("skip run file with unreadable metadata", "path", path, "error", infoErr)
				continue
			}
			if now.Sub(fi.ModTime()) > l.retention {
				continue
			}
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			l.debug("skip unreadable run file", "path", path, "error", readErr)
			continue
		}
		var wave waveFile
		if unmarshalErr := json.Unmarshal(raw, &wave); unmarshalErr != nil {
			l.debug("skip corrupt run file", "path", path, "error", unmarshalErr)
			continue
		}
		if len(wave.Workers) == 0 {
			l.debug("skip partial run file without workers", "path", path)
			continue
		}
		w := wave.Workers[0]
		runID := cmp.Or(w.RunID, wave.RunID)
		if runID == "" {
			l.debug("skip partial run file without runId", "path", path)
			continue
		}
		rec := ports.RunRecord{
			RunID:         runID,
			WorkerName:    w.WorkerName,
			RepoID:        w.RepoID,
			Machine:       w.Machine,
			Runtime:       w.Runtime,
			State:         effectiveState(w.State, w.ClosedAt),
			StepStartedAt: parseTime(w.StepStartedAt),
			OwnerPane:     wave.Owner.Pane,
			PaneID:        w.PaneID,
			TerminalID:    w.TerminalID,
			WaveTimestamp: parseTime(wave.Timestamp),
		}
		if w.LastSignal != nil {
			rec.SignalStatus = w.LastSignal.Status
			rec.SignalSummary = w.LastSignal.Summary
			rec.SignalReceivedAt = parseTime(w.LastSignal.ReceivedAt)
		}
		records = append(records, rec)
	}

	slices.SortFunc(records, func(a, b ports.RunRecord) int {
		if c := b.WaveTimestamp.Compare(a.WaveTimestamp); c != 0 {
			return c
		}
		if c := b.StepStartedAt.Compare(a.StepStartedAt); c != 0 {
			return c
		}
		return strings.Compare(a.RunID, b.RunID)
	})
	l.loaded = true
	l.dirMtime = mtime
	l.lastRead = now
	l.records = records
	return slices.Clone(l.records), nil
}

func (l *Ledger) debug(msg string, args ...any) {
	if l.logger != nil {
		l.logger.Debug(msg, args...)
	}
}

func effectiveState(state, closedAt string) string {
	if closedAt != "" {
		if state == "stopped" || state == "failed" {
			return state
		}
		return "closed"
	}
	if state == "" {
		return "running"
	}
	return state
}

func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}

// Match pairs live agents with run records, preferring terminal ID over pane ID.
// Records must be ordered newest first so the newest matching record wins.
func Match(agents []domain.Agent, records []ports.RunRecord) map[domain.PaneID]domain.RunMeta {
	return ports.MatchRuns(agents, records)
}

var _ ports.RunLedger = (*Ledger)(nil)
