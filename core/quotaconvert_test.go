package core

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func digest(data string) []byte {
	sum := sha1.Sum([]byte(data))
	return sum[:]
}

func TestBuildQuotaConversionReport(t *testing.T) {
	own := func(item RemoteMediaItem) *RemoteMediaItem {
		item.IsCanonical = true
		if item.Origin == 0 {
			item.Origin = OriginSelf
		}
		return &item
	}
	items := []*RemoteMediaItem{
		own(RemoteMediaItem{MediaKey: "A", FileName: "a.jpg", DedupKey: "DA", Sha1: digest("a"), SizeBytes: 100, QuotaChargedBytes: 100, Favorite: true}),
		own(RemoteMediaItem{MediaKey: "B", FileName: "b.jpg", DedupKey: "DB", Sha1: digest("b"), SizeBytes: 200, QuotaChargedBytes: 200}),
		// No local copy.
		own(RemoteMediaItem{MediaKey: "C", FileName: "c.jpg", DedupKey: "DC", Sha1: digest("c"), SizeBytes: 300, QuotaChargedBytes: 300}),
		// Live Photo still: excluded.
		own(RemoteMediaItem{MediaKey: "D", FileName: "d.heic", DedupKey: "DD", Sha1: digest("d"), SizeBytes: 400, QuotaChargedBytes: 400, HasMotion: true}),
		// Listed twice (a shared-album copy): excluded.
		own(RemoteMediaItem{MediaKey: "E1", FileName: "e.jpg", DedupKey: "DE", Sha1: digest("e"), SizeBytes: 500, QuotaChargedBytes: 500}),
		{MediaKey: "E2", FileName: "e.jpg", DedupKey: "DE", Sha1: digest("e"), SizeBytes: 500, QuotaChargedBytes: 500, Origin: OriginShared},
		// Free already: not counted.
		own(RemoteMediaItem{MediaKey: "F", FileName: "f.jpg", DedupKey: "DF", Sha1: digest("f"), SizeBytes: 600}),
		// In the bin: not counted here.
		own(RemoteMediaItem{MediaKey: "G", FileName: "g.jpg", DedupKey: "DG", Sha1: digest("g"), SizeBytes: 700, QuotaChargedBytes: 700, Trashed: true}),
	}
	local := map[string]string{
		hex.EncodeToString(digest("a")): filepath.Join("C:", "Pictures", "2020", "a.jpg"),
		hex.EncodeToString(digest("b")): filepath.Join("C:", "Pictures", "2021", "b.jpg"),
		hex.EncodeToString(digest("d")): filepath.Join("C:", "Pictures", "2021", "d.heic"),
		hex.EncodeToString(digest("e")): filepath.Join("C:", "Pictures", "2021", "e.jpg"),
	}
	report := BuildQuotaConversionReport(items, LocalScanResult{
		Found: local, Seen: 42, SizeMatched: 5,
		// c.jpg exists here, but as a different version.
		Names: map[string]string{"c.jpg": filepath.Join("C:", "Export", "c.jpg")},
		Sizes: map[string]int64{"c.jpg": 123},
	})

	if report.CountedFiles != 5 || report.CountedBytes != 100+200+300+400+500 {
		t.Fatalf("counted = %d files, %d bytes", report.CountedFiles, report.CountedBytes)
	}
	if len(report.Convertible) != 2 || report.ConvertibleBytes != 300 {
		t.Fatalf("convertible = %+v", report.Convertible)
	}
	if report.Convertible[0].MediaKey != "B" || !report.Convertible[1].Favorite {
		t.Fatalf("order/flags = %+v", report.Convertible)
	}
	if report.NotOnComputer != 1 || report.NotOnComputerBytes != 300 {
		t.Fatalf("not on computer = %d, %d", report.NotOnComputer, report.NotOnComputerBytes)
	}
	if len(report.Excluded) != 2 {
		t.Fatalf("excluded = %+v", report.Excluded)
	}
	if report.SameNameDifferent != 1 || report.SameNameDifferentBytes != 300 || len(report.SameNameExamples) != 1 ||
		report.SameNameExamples[0].LocalSize != 123 || report.SameNameExamples[0].RemoteSize != 300 || report.SizeMatched != 5 {
		t.Fatalf("same-name diagnostics = %+v", report)
	}
	if len(report.Folders) != 2 || report.Folders[0].Bytes != 200 || report.ScannedFiles != 42 {
		t.Fatalf("folders = %+v", report.Folders)
	}
}

func TestQuotaConversionStates(t *testing.T) {
	entries := []QuotaConversionEntry{
		{SHA1Hex: hex.EncodeToString(digest("free")), FileName: "free.jpg"},
		{SHA1Hex: hex.EncodeToString(digest("gone")), FileName: "gone.jpg"},
		{SHA1Hex: hex.EncodeToString(digest("bin")), FileName: "bin.jpg"},
		{SHA1Hex: hex.EncodeToString(digest("counted")), FileName: "counted.jpg"},
		{SHA1Hex: hex.EncodeToString(digest("lost")), FileName: "lost.jpg"},
	}
	items := []*RemoteMediaItem{
		{Sha1: digest("free"), QuotaChargedBytes: 0},
		{Sha1: digest("bin"), QuotaChargedBytes: 9, Trashed: true},
		{Sha1: digest("counted"), QuotaChargedBytes: 9},
	}
	states := QuotaConversionStates(items, entries, func(entry QuotaConversionEntry) bool {
		return entry.FileName != "lost.jpg"
	})
	want := []string{QuotaStateConverted, QuotaStateReady, QuotaStateInBin, QuotaStateCounted, QuotaStateReady}
	for i, state := range states {
		if state.State != want[i] {
			t.Errorf("%s: state %s, want %s", state.FileName, state.State, want[i])
		}
	}
	if states[1].LocalMissing || !states[4].LocalMissing {
		t.Fatalf("local missing flags = %v, %v", states[1].LocalMissing, states[4].LocalMissing)
	}
}

func TestQuotaConversionLedger(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")
	if len(LoadQuotaConversions()) != 0 {
		t.Fatal("new record not empty")
	}
	for _, sum := range []string{"s1", "s2"} {
		sum := sum
		if err := updateQuotaConversions(func(entries []QuotaConversionEntry) []QuotaConversionEntry {
			return append(entries, QuotaConversionEntry{SHA1Hex: sum})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ForgetQuotaConversions([]string{"s1"}); err != nil {
		t.Fatal(err)
	}
	entries := LoadQuotaConversions()
	if len(entries) != 1 || entries[0].SHA1Hex != "s2" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestLocalCopyMatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.jpg")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !LocalCopyMatches(context.Background(), path, hex.EncodeToString(digest("hello"))) {
		t.Fatal("matching file reported as different")
	}
	if LocalCopyMatches(context.Background(), path, hex.EncodeToString(digest("other"))) {
		t.Fatal("different content reported as matching")
	}
	if LocalCopyMatches(context.Background(), path+".missing", hex.EncodeToString(digest("hello"))) {
		t.Fatal("missing file reported as matching")
	}
}

func TestProtoVarintAt(t *testing.T) {
	data := encodePBTree([]pbNode{
		nVarint(4, 7),
		nMsg(29, nVarint(1, 0)),
		nMsg(31, nVarint(2, 5), nVarint(1, 1)),
	})
	if protoVarintAt(data, 31, 1) != 1 {
		t.Fatal("favourite flag not found")
	}
	if protoVarintAt(data, 29, 1) != 0 || protoVarintAt(data, 39, 1) != 0 {
		t.Fatal("absent or zero flags reported as set")
	}
}

func TestScanLocalCopies(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	same := write(filepath.Join("2023", "IMG_1.JPG"), "original")
	write(filepath.Join("2024", "IMG_2.JPG"), "edited!!") // same size as "original", other content
	write("other.jpg", "x")

	var events int
	result, err := ScanLocalCopies(context.Background(), []string{root},
		map[int64]bool{int64(len("original")): true},
		map[string]bool{hex.EncodeToString(digest("original")): true},
		nil, func(QuotaScanProgress) { events++ })
	if err != nil {
		t.Fatal(err)
	}
	if result.Seen != 3 || result.SizeMatched != 2 || result.Found[hex.EncodeToString(digest("original"))] != same {
		t.Fatalf("result = %+v", result)
	}
	if result.Names["img_2.jpg"] == "" || events < 2 {
		t.Fatalf("names = %v, progress events = %d", result.Names, events)
	}
}

func TestReportDownloadables(t *testing.T) {
	items := []*RemoteMediaItem{
		{MediaKey: "O", FileName: "o.jpg", DedupKey: "DO", Sha1: digest("o"), SizeBytes: 100, QuotaChargedBytes: 100, IsCanonical: true, Origin: OriginSelf, OriginalQuality: true},
		{MediaKey: "S", FileName: "s.jpg", DedupKey: "DS", Sha1: digest("s"), SizeBytes: 50, QuotaChargedBytes: 50, IsCanonical: true, Origin: OriginSelf},
		{MediaKey: "L", FileName: "l.heic", DedupKey: "DL", Sha1: digest("l"), SizeBytes: 70, QuotaChargedBytes: 70, IsCanonical: true, Origin: OriginSelf, OriginalQuality: true, HasMotion: true},
	}
	report := BuildQuotaConversionReport(items, LocalScanResult{Found: map[string]string{}})
	if len(report.Downloadable) != 1 || report.Downloadable[0].MediaKey != "O" || report.DownloadableBytes != 100 {
		t.Fatalf("downloadable = %+v", report.Downloadable)
	}
	if len(report.NotDownloadable) != 2 {
		t.Fatalf("not downloadable = %+v", report.NotDownloadable)
	}

	path := filepath.Join("D:", "Originals", "2024", "o.jpg")
	report.MarkDownloaded([]QuotaDownloadOutcome{{MediaKey: "O", Status: "downloaded", LocalPath: path}})
	if len(report.Downloadable) != 0 || len(report.Convertible) != 1 || report.Convertible[0].LocalPath != path ||
		report.ConvertibleBytes != 100 || report.NotOnComputer != 2 || len(report.Folders) != 1 {
		t.Fatalf("after download = %+v", report)
	}
}

func TestDownloadTarget(t *testing.T) {
	folder := t.TempDir()
	item := QuotaConversionItem{MediaKey: "K", FileName: "a:b.jpg", CapturedAt: time.Date(2021, 5, 1, 0, 0, 0, 0, time.UTC).UnixMilli()}
	first := downloadTarget(folder, item)
	if first != filepath.Join(folder, "2021", "a_b.jpg") {
		t.Fatalf("first = %s", first)
	}
	if err := os.MkdirAll(filepath.Dir(first), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if second := downloadTarget(folder, item); second != filepath.Join(folder, "2021", "a_b (2).jpg") {
		t.Fatalf("second = %s", second)
	}
}

func TestPlanQuotaConvertAll(t *testing.T) {
	own := func(item RemoteMediaItem) *RemoteMediaItem {
		item.IsCanonical, item.Origin = true, OriginSelf
		return &item
	}
	items := []*RemoteMediaItem{
		own(RemoteMediaItem{MediaKey: "O", DedupKey: "DO", Sha1: digest("o"), SizeBytes: 120, QuotaChargedBytes: 100, OriginalQuality: true, UTCTimestamp: 3000}),
		own(RemoteMediaItem{MediaKey: "S", DedupKey: "DS", Sha1: digest("s"), SizeBytes: 900, QuotaChargedBytes: 50, UTCTimestamp: 1000}),
		own(RemoteMediaItem{MediaKey: "L", DedupKey: "DL", Sha1: digest("l"), QuotaChargedBytes: 70, OriginalQuality: true, HasMotion: true}),
		own(RemoteMediaItem{MediaKey: "F", DedupKey: "DF", Sha1: digest("f")}), // free already
		own(RemoteMediaItem{MediaKey: "T", DedupKey: "DT", Sha1: digest("t"), QuotaChargedBytes: 30, Trashed: true}),
		// A second entry of O (shared-album copy) is the same file.
		{MediaKey: "O2", DedupKey: "DO", Sha1: digest("o"), QuotaChargedBytes: 100, Origin: OriginShared},
	}
	plan, targets := PlanQuotaConvertAll(items)
	if plan.CountedFiles != 3 || plan.CountedBytes != 220 || plan.Files != 2 || plan.Bytes != 150 {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.SaverFiles != 1 || plan.LivePhotos != 1 || plan.LivePhotoBytes != 70 || plan.DownloadBytes != 120+50 {
		t.Fatalf("plan details = %+v", plan)
	}
	// Oldest first; the storage-saver file is marked.
	if len(targets) != 2 || targets[0].MediaKey != "S" || !targets[0].StoredCopyOnly || targets[1].StoredCopyOnly {
		t.Fatalf("targets = %+v", targets)
	}
}
