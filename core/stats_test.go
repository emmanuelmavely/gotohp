package core

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUploadStatsRecordAndAverage(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")

	before := LoadUploadStats()
	if before.UploadedBytes != 0 || before.Sessions != 0 {
		t.Fatalf("expected zeroed stats, got %+v", before)
	}

	RecordUploadRun(1500, 3, 1, 2, 10)
	RecordUploadRun(500, 1, 0, 0, 5)
	RecordDownload(999)

	stats := LoadUploadStats()
	if stats.UploadedBytes != 2000 || stats.UploadedFiles != 4 {
		t.Fatalf("uploaded counters off: %+v", stats)
	}
	if stats.FailedFiles != 1 || stats.SkippedFiles != 2 {
		t.Fatalf("failure counters off: %+v", stats)
	}
	if stats.DownloadedBytes != 999 {
		t.Fatalf("download counter off: %+v", stats)
	}
	if stats.Sessions != 2 || stats.ActiveSeconds != 15 {
		t.Fatalf("session counters off: %+v", stats)
	}
	if got := stats.AverageSpeed(); got != 2000/15 {
		t.Fatalf("average speed = %d", got)
	}

	today := time.Now().Format("2006-01-02")
	if stats.Daily[today] != 2000 {
		t.Fatalf("daily history off: %+v", stats.Daily)
	}
}

func TestRecordLivePhotoRepairs(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")

	RecordLivePhotoRepairs(0, 0, 0) // nothing happened: no file written
	if stats := LoadUploadStats(); stats.RepairedLivePhotos != 0 || stats.UploadedBytes != 0 {
		t.Fatalf("expected zeroed stats, got %+v", stats)
	}

	RecordUploadRun(1000, 2, 0, 0, 4)
	RecordLivePhotoRepairs(300, 2, 1)

	stats := LoadUploadStats()
	if stats.RepairedLivePhotos != 2 {
		t.Fatalf("repaired = %d", stats.RepairedLivePhotos)
	}
	if stats.UploadedBytes != 1300 || stats.ActiveSeconds != 5 {
		t.Fatalf("bytes/seconds off: %+v", stats)
	}
	if stats.UploadedFiles != 2 || stats.Sessions != 1 {
		t.Fatalf("repairs must not count as new files or upload runs: %+v", stats)
	}
	if today := time.Now().Format("2006-01-02"); stats.Daily[today] != 1300 {
		t.Fatalf("daily history off: %+v", stats.Daily)
	}
}
