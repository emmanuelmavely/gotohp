package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// UploadRun is one finished upload run in the history the Activity page
// shows: what went up, where, and what failed and why.
type UploadRun struct {
	// StartedAt and FinishedAt are Unix milliseconds.
	StartedAt  int64
	FinishedAt int64
	Uploaded   int
	Skipped    int
	Failed     int
	// Stopped counts files a pause or cancel left for later.
	Stopped int
	Bytes   int64
	// Album is the album name or key the run added to; empty for none.
	// AlbumAuto is set when every folder became its own album.
	Album     string
	AlbumAuto bool
	// AlbumError is set when the files uploaded but did not reach the album.
	AlbumError string `json:",omitempty"`
	// Failures lists the failed files (at most uploadHistoryFailures).
	Failures []UploadFailure
}

// UploadFailure is one file that failed, with the reason in plain words.
type UploadFailure struct {
	Path    string
	Message string
}

const (
	uploadHistoryRuns     = 30
	uploadHistoryFailures = 200
	uploadHistoryVersion  = 1
)

type uploadHistoryFile struct {
	Version int
	Runs    []UploadRun
}

var uploadHistoryMu sync.Mutex

// UploadHistoryPath returns the history file next to the config file, or ""
// when the config path is not known yet.
func UploadHistoryPath() string {
	if ConfigPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(ConfigPath), "upload-history.json")
}

// LoadUploadHistory returns the recorded runs, newest first. A missing or
// unreadable file is an empty history.
func LoadUploadHistory() []UploadRun {
	uploadHistoryMu.Lock()
	defer uploadHistoryMu.Unlock()
	return loadUploadHistoryLocked()
}

func loadUploadHistoryLocked() []UploadRun {
	runs := []UploadRun{}
	path := UploadHistoryPath()
	if path == "" {
		return runs
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return runs
	}
	var file uploadHistoryFile
	if json.Unmarshal(data, &file) != nil || file.Version != uploadHistoryVersion {
		return runs
	}
	for i := range file.Runs {
		if file.Runs[i].Failures == nil {
			file.Runs[i].Failures = []UploadFailure{}
		}
	}
	return file.Runs
}

// RecordUploadHistory adds a finished run to the front of the history. Runs
// that touched nothing are not recorded.
func RecordUploadHistory(run UploadRun) {
	if run.Uploaded == 0 && run.Skipped == 0 && run.Failed == 0 && run.Stopped == 0 {
		return
	}
	if run.FinishedAt == 0 {
		run.FinishedAt = time.Now().UnixMilli()
	}
	if len(run.Failures) > uploadHistoryFailures {
		run.Failures = run.Failures[:uploadHistoryFailures]
	}
	if run.Failures == nil {
		run.Failures = []UploadFailure{}
	}
	uploadHistoryMu.Lock()
	defer uploadHistoryMu.Unlock()
	runs := append([]UploadRun{run}, loadUploadHistoryLocked()...)
	if len(runs) > uploadHistoryRuns {
		runs = runs[:uploadHistoryRuns]
	}
	saveUploadHistoryLocked(runs)
}

// ClearUploadHistory forgets every recorded run.
func ClearUploadHistory() {
	uploadHistoryMu.Lock()
	defer uploadHistoryMu.Unlock()
	saveUploadHistoryLocked([]UploadRun{})
}

func saveUploadHistoryLocked(runs []UploadRun) {
	path := UploadHistoryPath()
	if path == "" {
		return
	}
	data, err := json.Marshal(uploadHistoryFile{Version: uploadHistoryVersion, Runs: runs})
	if err != nil {
		return
	}
	directory := filepath.Dir(path)
	if os.MkdirAll(directory, 0o755) != nil {
		return
	}
	temporary, err := os.CreateTemp(directory, ".upload-history-*")
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
