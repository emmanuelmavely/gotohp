package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Upload journal states. Items start pending and reach a terminal status when
// their FileUploadResult arrives. Cancellation leaves unprocessed items
// pending so a later resume retries them; server-side hash-check dedup makes
// redoing a half-uploaded file harmless.
const (
	journalItemPending = "pending"
	journalItemDone    = "done"
	journalItemSkipped = "skipped"
	journalItemFailed  = "failed"
)

// UploadJournalItem tracks one work item across app restarts.
type UploadJournalItem struct {
	// Kind mirrors UploadWorkItem: "single" or "live-photo".
	Kind string `json:"kind"`
	// Paths are the component paths; the first one is the primary path.
	Paths []string `json:"paths"`
	Size  int64    `json:"size"`
	// Status is pending, done, skipped, or failed.
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// UploadJournal persists the manifest of an upload run so an interrupted run
// can be offered for resume on the next start. The file lives next to
// gotohp.config and holds at most one run: a new run replaces it.
type UploadJournal struct {
	Version     int               `json:"version"`
	StartedAt   int64             `json:"startedAt"`
	SourcePaths []string          `json:"sourcePaths"`
	Options     UploadOptions     `json:"options"`
	Items       []UploadJournalItem `json:"items"`

	mu     sync.Mutex
	dirty  bool
}

// UploadJournalPath returns the journal file location next to the config
// file, or an empty string when the config path is not known yet.
func UploadJournalPath() string {
	if ConfigPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(ConfigPath), "upload-journal.json")
}

// newUploadJournal snapshots a classified work list with pending statuses.
// Preflight-skipped items are recorded as skipped so the counts match what
// the UI reported.
func newUploadJournal(sourcePaths []string, opts UploadOptions, workItems []UploadWorkItem, preflightWarnings []PreflightWarning) *UploadJournal {
	journal := &UploadJournal{
		Version:     1,
		StartedAt:   time.Now().Unix(),
		SourcePaths: append([]string(nil), sourcePaths...),
		Options:     opts,
	}
	for _, item := range workItems {
		paths := uploadWorkPaths(item)
		journal.Items = append(journal.Items, UploadJournalItem{
			Kind:   string(item.Kind),
			Paths:  append([]string(nil), paths...),
			Size:   sumPathSizes(paths),
			Status: journalItemPending,
		})
	}
	for _, warning := range preflightWarnings {
		if !isSkippedPreflightWarning(warning.Code) {
			continue
		}
		journal.Items = append(journal.Items, UploadJournalItem{
			Kind:   string(UploadWorkLivePhoto),
			Paths:  append([]string(nil), warning.Paths...),
			Size:   sumPathSizes(warning.Paths),
			Status: journalItemSkipped,
		})
	}
	return journal
}

// loadUploadJournal reads the journal file. It returns nil without error when
// no journal exists.
func loadUploadJournal() (*UploadJournal, error) {
	path := UploadJournalPath()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var journal UploadJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		// A corrupt journal is as good as none.
		return nil, nil
	}
	if journal.Version != 1 {
		return nil, fmt.Errorf("unsupported journal version %d", journal.Version)
	}
	return &journal, nil
}

// Save persists the journal atomically.
func (j *UploadJournal) Save() {
	if j == nil {
		return
	}
	path := UploadJournalPath()
	if path == "" {
		return
	}
	j.mu.Lock()
	if !j.dirty {
		j.mu.Unlock()
		return
	}
	j.dirty = false
	j.mu.Unlock()

	data, err := json.Marshal(j)
	if err != nil {
		return
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return
	}
	temporary, err := os.CreateTemp(directory, ".upload-journal-*")
	if err != nil {
		return
	}
	_, writeErr := temporary.Write(data)
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(temporary.Name())
		return
	}
	_ = os.Rename(temporary.Name(), path)
}

// markDirty flags the journal for the next Save. Saving the full manifest on
// every file result would rewrite a potentially large JSON; callers throttle.
func (j *UploadJournal) markDirty() {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.dirty = true
	j.mu.Unlock()
}

// RecordResult folds a FileUploadResult into the item matching its primary
// path. Unknown paths are ignored.
func (j *UploadJournal) RecordResult(result FileUploadResult) {
	if j == nil {
		return
	}
	j.mu.Lock()
	for index := range j.Items {
		item := &j.Items[index]
		if len(item.Paths) == 0 || item.Paths[0] != result.Path {
			continue
		}
		switch {
		case result.Interrupted:
			// Stopped by a pause or cancel: leave it for the resume.
		case result.IsError:
			item.Status = journalItemFailed
			item.Error = result.ErrorMessage
		case result.Skipped:
			item.Status = journalItemSkipped
		default:
			item.Status = journalItemDone
			item.Error = ""
		}
	}
	j.mu.Unlock()
	j.markDirty()
}

// Remaining returns the items a resume would process: pending and failed.
func (j *UploadJournal) Remaining() []UploadJournalItem {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	var items []UploadJournalItem
	for _, item := range j.Items {
		if item.Status == journalItemPending || item.Status == journalItemFailed {
			items = append(items, item)
		}
	}
	return items
}

// Complete reports whether every item reached a terminal status.
func (j *UploadJournal) Complete() bool {
	if j == nil {
		return true
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, item := range j.Items {
		if item.Status == journalItemPending {
			return false
		}
	}
	return true
}

// Remove deletes the journal file. Missing files are not an error.
func (j *UploadJournal) Remove() {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.dirty = false
	j.mu.Unlock()
	if path := UploadJournalPath(); path != "" {
		_ = os.Remove(path)
	}
}

// UploadJournalSummary is what the GUI and CLI show for a pending journal.
type UploadJournalSummary struct {
	Exists       bool   `json:"Exists"`
	StartedAt    int64  `json:"StartedAt"`
	Total        int    `json:"Total"`
	Done         int    `json:"Done"`
	Skipped      int    `json:"Skipped"`
	Failed       int    `json:"Failed"`
	Pending      int    `json:"Pending"`
	PendingBytes int64  `json:"PendingBytes"`
	SourcePaths  string `json:"SourcePaths"`
}

// PendingUploadJournal summarizes the on-disk journal, if any.
func PendingUploadJournal() UploadJournalSummary {
	journal, err := loadUploadJournal()
	if err != nil || journal == nil {
		return UploadJournalSummary{}
	}
	summary := UploadJournalSummary{Exists: true, StartedAt: journal.StartedAt}
	if len(journal.SourcePaths) > 0 {
		summary.SourcePaths = journal.SourcePaths[0]
		if len(journal.SourcePaths) > 1 {
			summary.SourcePaths = fmt.Sprintf("%s (+%d more)", summary.SourcePaths, len(journal.SourcePaths)-1)
		}
	}
	for _, item := range journal.Items {
		summary.Total++
		switch item.Status {
		case journalItemDone:
			summary.Done++
		case journalItemSkipped:
			summary.Skipped++
		case journalItemFailed:
			summary.Failed++
		default:
			summary.Pending++
			summary.PendingBytes += item.Size
		}
	}
	if summary.Pending == 0 {
		// Nothing left to do; a fully processed journal is stale.
		journal.Remove()
		return UploadJournalSummary{}
	}
	return summary
}

// DiscardUploadJournal deletes a pending journal without uploading.
func DiscardUploadJournal() {
	if journal, err := loadUploadJournal(); err == nil {
		journal.Remove()
	}
}

// markJournalItemFailed records a failure discovered outside the normal
// result flow, e.g. a source file that disappeared before resume.
func (j *UploadJournal) markJournalItemFailed(primaryPath string, message string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	for index := range j.Items {
		item := &j.Items[index]
		if len(item.Paths) > 0 && item.Paths[0] == primaryPath && item.Status == journalItemPending {
			item.Status = journalItemFailed
			item.Error = message
		}
	}
	j.mu.Unlock()
	j.markDirty()
}
