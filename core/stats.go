package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// UploadStats are cumulative, cross-session counters persisted next to the
// config so the GUI's statistics view survives restarts. They record what
// actually moved: transferred upload bytes (dedup hits are not counted as
// uploaded), files, failures, time spent in runs, and a per-day history of
// the last 30 days.
type UploadStats struct {
	Version int `json:"version"`

	UploadedBytes   int64 `json:"uploadedBytes"`
	UploadedFiles   int64 `json:"uploadedFiles"`
	FailedFiles     int64 `json:"failedFiles"`
	SkippedFiles    int64 `json:"skippedFiles"`
	DownloadedBytes int64 `json:"downloadedBytes"`
	ActiveSeconds   int64 `json:"activeSeconds"`
	Sessions        int64 `json:"sessions"`
	// RepairedLivePhotos counts Live Photos rejoined by the repair.
	RepairedLivePhotos int64 `json:"repairedLivePhotos"`

	// Daily maps "2006-01-02" to the bytes uploaded that day (local time).
	Daily map[string]int64 `json:"daily"`
}

// statsDailyHistory is how many day buckets the stats file keeps.
const statsDailyHistory = 30

// statsVersion is the current on-disk format of the stats file.
const statsVersion = 1

// UploadStatsPath returns the stats file location next to the config file,
// or an empty string when the config path is not known yet.
func UploadStatsPath() string {
	if ConfigPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(ConfigPath), "stats.json")
}

var statsMu sync.Mutex

// LoadUploadStats reads the stats file. Missing or corrupt files yield zeroed
// stats: counters are informational, not state that must survive.
func LoadUploadStats() UploadStats {
	path := UploadStatsPath()
	if path == "" {
		return newUploadStats()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return newUploadStats()
	}
	var stats UploadStats
	if json.Unmarshal(data, &stats) != nil || stats.Version != statsVersion {
		return newUploadStats()
	}
	if stats.Daily == nil {
		stats.Daily = map[string]int64{}
	}
	return stats
}

// newUploadStats returns zeroed stats that are safe to record into: Daily is
// always a usable map, including on the first run before a stats file exists.
func newUploadStats() UploadStats {
	return UploadStats{Version: statsVersion, Daily: map[string]int64{}}
}

// RecordUploadRun folds one finished run into the stats. activeSeconds is the
// wall time the run spent transferring.
func RecordUploadRun(uploadedBytes, uploadedFiles, failedFiles, skippedFiles, activeSeconds int64) {
	if uploadedBytes == 0 && uploadedFiles == 0 && failedFiles == 0 && skippedFiles == 0 {
		return
	}
	statsMu.Lock()
	defer statsMu.Unlock()

	stats := LoadUploadStats()
	stats.UploadedBytes += uploadedBytes
	stats.UploadedFiles += uploadedFiles
	stats.FailedFiles += failedFiles
	stats.SkippedFiles += skippedFiles
	stats.ActiveSeconds += activeSeconds
	stats.Sessions++
	if uploadedBytes > 0 {
		key := time.Now().Format("2006-01-02")
		stats.Daily[key] += uploadedBytes
		stats.pruneDaily()
	}
	stats.Save()
}

// RecordLivePhotoRepairs folds a batch of Live Photo repairs into the stats.
// The re-uploaded videos count as uploaded bytes (and toward the average
// speed through uploadSeconds) but not as new files: the library gains none.
func RecordLivePhotoRepairs(uploadedBytes, repaired, uploadSeconds int64) {
	if uploadedBytes == 0 && repaired == 0 {
		return
	}
	statsMu.Lock()
	defer statsMu.Unlock()

	stats := LoadUploadStats()
	stats.UploadedBytes += uploadedBytes
	stats.ActiveSeconds += uploadSeconds
	stats.RepairedLivePhotos += repaired
	if uploadedBytes > 0 {
		key := time.Now().Format("2006-01-02")
		stats.Daily[key] += uploadedBytes
		stats.pruneDaily()
	}
	stats.Save()
}

// RecordDownload folds one file download (e.g. a repair fetching a stored
// video) into the stats.
func RecordDownload(bytes int64) {
	if bytes <= 0 {
		return
	}
	statsMu.Lock()
	defer statsMu.Unlock()

	stats := LoadUploadStats()
	stats.DownloadedBytes += bytes
	stats.Save()
}

// pruneDaily keeps only the most recent day buckets. Callers hold statsMu.
func (s *UploadStats) pruneDaily() {
	if len(s.Daily) <= statsDailyHistory {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -statsDailyHistory+1).Format("2006-01-02")
	for key := range s.Daily {
		if key < cutoff {
			delete(s.Daily, key)
		}
	}
}

// Save persists the stats atomically. Errors are swallowed: the stats are an
// optimization.
func (s *UploadStats) Save() {
	path := UploadStatsPath()
	if path == "" {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return
	}
	temporary, err := os.CreateTemp(directory, ".stats-*")
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

// AverageSpeed returns the mean transfer rate across all recorded sessions.
// It returns 0 when no active time was recorded.
func (s UploadStats) AverageSpeed() int64 {
	if s.ActiveSeconds <= 0 {
		return 0
	}
	return s.UploadedBytes / s.ActiveSeconds
}
