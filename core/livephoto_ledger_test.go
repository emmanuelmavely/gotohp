package core

import (
	"path/filepath"
	"testing"
	"time"
)

func ledgerTestItems() []*RemoteMediaItem {
	const shot = int64(1709461164526)
	const clip = int64(1709461163000)
	return []*RemoteMediaItem{
		// Repaired with the ledger: Google still lists the motion part.
		{MediaKey: "P1", FileName: "IMG_0001.HEIC", Type: MediaTypePhoto, DedupKey: "PD1", IsCanonical: true, UTCTimestamp: shot},
		{MediaKey: "V1", FileName: "IMG_0001.MOV", Type: MediaTypeVideo, DedupKey: "VD1", IsCanonical: true, UTCTimestamp: clip},
		// Repaired before the ledger: the orphan copy is in the bin.
		{MediaKey: "P2", FileName: "IMG_0002.HEIC", Type: MediaTypePhoto, DedupKey: "PD2", IsCanonical: true, UTCTimestamp: shot},
		{MediaKey: "V2", FileName: "IMG_0002.MOV", Type: MediaTypeVideo, DedupKey: "VD2", IsCanonical: true, UTCTimestamp: clip},
		{MediaKey: "V2old", FileName: "IMG_0002.MOV", Type: MediaTypeVideo, DedupKey: "VD2", IsCanonical: true, UTCTimestamp: clip, Trashed: true, TrashTimestamp: 1731600000000},
		// A real orphan.
		{MediaKey: "P3", FileName: "IMG_0003.HEIC", Type: MediaTypePhoto, DedupKey: "PD3", IsCanonical: true, UTCTimestamp: shot},
		{MediaKey: "V3", FileName: "IMG_0003.MOV", Type: MediaTypeVideo, DedupKey: "VD3", IsCanonical: true, UTCTimestamp: clip},
		// Something the user deleted.
		{MediaKey: "X", FileName: "holiday.jpg", Type: MediaTypePhoto, DedupKey: "XD", SizeBytes: 100, IsCanonical: true, Trashed: true, TrashTimestamp: 1731700000000},
	}
}

func TestReportLeavesOutAlreadyRepaired(t *testing.T) {
	ledger := LivePhotoRepairLedger{Version: liveRepairLedgerVersion, Repaired: []RepairedLivePhoto{
		{Stem: "img_0001", PhotoDedupKey: "PD1", VideoDedupKey: "VD1", RepairedAt: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC).Unix()},
	}}
	report := findLivePhotoRepairReport(ledgerTestItems(), ledger)
	if len(report.Candidates) != 1 || report.Candidates[0].Stem != "img_0003" {
		t.Fatalf("candidates = %+v, want only img_0003", report.Candidates)
	}
	if len(report.AlreadyRepaired) != 2 {
		t.Fatalf("already repaired = %+v, want img_0001 and img_0002", report.AlreadyRepaired)
	}
	got := map[string]string{}
	for _, done := range report.AlreadyRepaired {
		got[done.Stem] = done.PhotoMediaKey
	}
	if got["img_0001"] != "P1" || got["img_0002"] != "P2" {
		t.Fatalf("already repaired = %+v", report.AlreadyRepaired)
	}
}

func TestLedgerRoundTrip(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")
	if got := LoadLivePhotoRepairLedger(); len(got.Repaired) != 0 {
		t.Fatalf("new ledger not empty: %+v", got)
	}
	for _, stem := range []string{"a", "b"} {
		if err := RecordRepairedLivePhoto(RepairedLivePhoto{Stem: stem, VideoDedupKey: "V" + stem}); err != nil {
			t.Fatal(err)
		}
	}
	got := LoadLivePhotoRepairLedger()
	if len(got.Repaired) != 2 || got.Repaired[1].Stem != "b" {
		t.Fatalf("ledger = %+v", got)
	}
}

func TestListBin(t *testing.T) {
	ledger := LivePhotoRepairLedger{Version: liveRepairLedgerVersion, Repaired: []RepairedLivePhoto{
		{Stem: "img_0002", PhotoFileName: "IMG_0002.HEIC", PhotoMediaKey: "P2", VideoDedupKey: "VD2"},
	}}
	report := ListBin(ledgerTestItems(), ledger)
	if len(report.Items) != 2 || report.TotalBytes != 100 || report.FromRepair != 1 {
		t.Fatalf("report = %+v", report)
	}
	// Newest first.
	if report.Items[0].MediaKey != "X" || report.Items[1].MediaKey != "V2old" {
		t.Fatalf("order = %s, %s", report.Items[0].MediaKey, report.Items[1].MediaKey)
	}
	old := report.Items[1]
	if old.RepairedPhoto != "IMG_0002.HEIC" || old.RepairedPhotoMediaKey != "P2" || !old.CopyInLibrary || !old.IsVideo {
		t.Fatalf("repair item = %+v", old)
	}
	if want := time.UnixMilli(1731600000000).Add(60 * 24 * time.Hour).UnixMilli(); old.DeletesAt != want {
		t.Fatalf("DeletesAt = %d, want %d", old.DeletesAt, want)
	}
	if old.RepairGuessed {
		t.Fatalf("a ledger match is not a guess: %+v", old)
	}
	if report.Items[0].RepairedPhoto != "" || report.Items[0].CopyInLibrary {
		t.Fatalf("user item = %+v", report.Items[0])
	}

	// Without a ledger entry the same video is recognised as a likely repair.
	guessed := ListBin(ledgerTestItems(), LivePhotoRepairLedger{})
	if guessed.FromRepair != 1 || !guessed.Items[1].RepairGuessed || guessed.Items[1].RepairedPhotoMediaKey != "P2" {
		t.Fatalf("guessed = %+v", guessed.Items[1])
	}
}
