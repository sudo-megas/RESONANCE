package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const activityFileName = "activity.json"
const maxActivityEntries = 10

// activityMu serializes every read-modify-write of activity.json.
// recordActivity's load-append-save sequence has no other coordination —
// two operations completing close together (e.g. within an Update-All
// batch) could otherwise both load the same pre-update log, each append
// their own entry, and each save, with the second save silently
// discarding the first's entry.
var activityMu sync.Mutex

// ActivityEntry records one completed add/update/restore/undo operation for
// the recent-activity popup.
type ActivityEntry struct {
	Kind      string `json:"kind"` // "add" | "update" | "restore" | "undo" | "remove" | "edit"
	AppName   string `json:"appName"`
	Summary   string `json:"summary"`   // precomputed human-readable description
	Timestamp string `json:"timestamp"` // RFC3339 UTC
}

// ActivityLog is the on-disk shape of activityLogPath()'s file, oldest
// entry first — GetRecentActivity reverses this for display.
type ActivityLog struct {
	Entries []ActivityEntry `json:"entries"`
}

// activityLogPath is resonanceStateDir()/activity.json. Since v1.4.0 removed
// undo it is the only thing in that directory.
func activityLogPath() (string, error) {
	dir, err := resonanceStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, activityFileName), nil
}

// loadActivityLog treats any failure (missing, corrupt, unreadable) as an
// empty log, never a crash. A log is a convenience; nothing depends on it. Entries is
// always normalized to non-nil (matches UpdateResult's established "nil
// slice marshals to JSON null" convention) so GetRecentActivity never hands
// the frontend a null that breaks entries.length.
func loadActivityLog() ActivityLog {
	empty := ActivityLog{Entries: []ActivityEntry{}}

	path, err := activityLogPath()
	if err != nil {
		return empty
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return empty
	}
	var log ActivityLog
	if err := json.Unmarshal(data, &log); err != nil {
		return empty
	}
	if log.Entries == nil {
		log.Entries = []ActivityEntry{}
	}
	return log
}

// saveActivityLog writes the log with a plain os.WriteFile at 0644, matching
// saveManifest's simplicity. There is no crash-safety requirement for a log,
// so an atomic-rename dance would be over-engineering. MkdirAll covers the
// one-time case where resonanceStateDir() doesn't exist yet.
func saveActivityLog(log ActivityLog) error {
	path, err := activityLogPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(log, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// recordActivity appends one entry, capped at maxActivityEntries (oldest
// dropped from the front). Best-effort by design: a logging failure must
// never fail the primary operation it's recording — no return value.
func recordActivity(kind, appName, summary string) {
	activityMu.Lock()
	defer activityMu.Unlock()
	log := loadActivityLog()
	log.Entries = append(log.Entries, ActivityEntry{
		Kind:      kind,
		AppName:   appName,
		Summary:   summary,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if len(log.Entries) > maxActivityEntries {
		log.Entries = log.Entries[len(log.Entries)-maxActivityEntries:]
	}
	_ = saveActivityLog(log)
}

// GetRecentActivity returns the persisted log, newest first.
func (a *App) GetRecentActivity() ([]ActivityEntry, error) {
	activityMu.Lock()
	defer activityMu.Unlock()
	log := loadActivityLog()
	entries := make([]ActivityEntry, len(log.Entries))
	for i, e := range log.Entries {
		entries[len(entries)-1-i] = e
	}
	return entries, nil
}
