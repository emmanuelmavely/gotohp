package core

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestUploadHistory(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")

	if runs := LoadUploadHistory(); len(runs) != 0 {
		t.Fatalf("new history = %v", runs)
	}
	RecordUploadHistory(UploadRun{}) // nothing happened: not recorded
	for i := range uploadHistoryRuns + 5 {
		RecordUploadHistory(UploadRun{Uploaded: i + 1})
	}
	failures := make([]UploadFailure, uploadHistoryFailures+10)
	for i := range failures {
		failures[i] = UploadFailure{Path: fmt.Sprint(i), Message: "boom"}
	}
	RecordUploadHistory(UploadRun{Failed: len(failures), Failures: failures, Album: "Trip"})

	runs := LoadUploadHistory()
	if len(runs) != uploadHistoryRuns {
		t.Fatalf("kept %d runs", len(runs))
	}
	if runs[0].Album != "Trip" || len(runs[0].Failures) != uploadHistoryFailures || runs[0].FinishedAt == 0 {
		t.Fatalf("newest run = %+v", runs[0])
	}
	if runs[1].Uploaded != uploadHistoryRuns+5 || runs[1].Failures == nil {
		t.Fatalf("second run = %+v", runs[1])
	}
	ClearUploadHistory()
	if runs := LoadUploadHistory(); len(runs) != 0 {
		t.Fatalf("cleared history = %v", runs)
	}
}
