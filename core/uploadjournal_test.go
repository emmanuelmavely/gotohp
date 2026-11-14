package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUploadJournalRoundTripAndResume(t *testing.T) {
	// Point the journal at a temp config location.
	directory := t.TempDir()
	configPath := filepath.Join(directory, "gotohp.config")
	original := ConfigPath
	ConfigPath = configPath
	defer func() { ConfigPath = original }()

	file := writeTestFile(t, directory, "photo.jpg", "data")
	opts := UploadOptions{AlbumName: "trip", AlbumAutoMode: false}
	workItems := []UploadWorkItem{
		{Kind: UploadWorkSingle, Single: &SingleMedia{Path: file}},
	}
	journal := newUploadJournal([]string{file}, opts, workItems, nil)
	journal.dirty = true
	journal.Save()

	loaded, err := loadUploadJournal()
	if err != nil || loaded == nil {
		t.Fatalf("load journal: %v %v", loaded, err)
	}
	if len(loaded.Items) != 1 || loaded.Items[0].Status != journalItemPending {
		t.Fatalf("unexpected items: %+v", loaded.Items)
	}
	if loaded.Options.AlbumName != "trip" {
		t.Fatalf("album choice not persisted: %+v", loaded.Options)
	}

	loaded.RecordResult(FileUploadResult{Path: file, MediaKey: "abc"})
	loaded.Save()
	if !loaded.Complete() {
		t.Fatal("expected journal to be complete after the only result")
	}

	summary := PendingUploadJournal()
	if summary.Exists {
		t.Fatal("a complete journal must not be offered for resume")
	}
	if _, err := os.Stat(UploadJournalPath()); !os.IsNotExist(err) {
		t.Fatalf("expected journal file removed, got %v", err)
	}
}

func TestUploadJournalKeepsPendingOnCancelLikeResults(t *testing.T) {
	directory := t.TempDir()
	original := ConfigPath
	ConfigPath = filepath.Join(directory, "gotohp.config")
	defer func() { ConfigPath = original }()

	file := writeTestFile(t, directory, "video.mp4", "data")
	journal := newUploadJournal(
		[]string{file},
		UploadOptions{},
		[]UploadWorkItem{{Kind: UploadWorkSingle, Single: &SingleMedia{Path: file}}},
		nil,
	)
	journal.dirty = true
	journal.Save()

	loaded, err := loadUploadJournal()
	if err != nil || loaded == nil {
		t.Fatalf("load journal: %v %v", loaded, err)
	}
	// No results arrive (cancel): the item stays pending and is offered.
	summary := PendingUploadJournal()
	if !summary.Exists || summary.Pending != 1 || summary.PendingBytes <= 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	remaining := loaded.Remaining()
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining item, got %d", len(remaining))
	}

	// A failed result must keep the item eligible for resume retry.
	loaded.RecordResult(FileUploadResult{Path: file, IsError: true, ErrorMessage: "boom"})
	if items := loaded.Remaining(); len(items) != 1 {
		t.Fatalf("failed item should be retried on resume, got %d", len(items))
	}
}
