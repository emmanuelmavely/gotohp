package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFindLivePhotoRepairCandidates(t *testing.T) {
	const shot = int64(1709461164526) // the still, in milliseconds
	const clip = int64(1709461163000) // its video, 1.5 s earlier
	own := func(item RemoteMediaItem) *RemoteMediaItem {
		item.IsCanonical = true
		if item.UTCTimestamp == 0 {
			if item.Type == MediaTypeVideo {
				item.UTCTimestamp = clip
			} else {
				item.UTCTimestamp = shot
			}
		}
		return &item
	}
	items := []*RemoteMediaItem{
		// A linked Live Photo: only the still is listed, no orphan video.
		own(RemoteMediaItem{MediaKey: "LP", FileName: "IMG_0001.HEIC", Type: MediaTypePhoto}),
		// An orphan pair.
		own(RemoteMediaItem{MediaKey: "P2", FileName: "IMG_0002.HEIC", Type: MediaTypePhoto}),
		own(RemoteMediaItem{MediaKey: "V2", FileName: "IMG_0002.MOV", Type: MediaTypeVideo, DedupKey: "DK2"}),
		// HEIC original plus a Lightroom JPEG export with the same name: the HEIC wins.
		own(RemoteMediaItem{MediaKey: "P3jpg", FileName: "IMG_0003.jpg", Type: MediaTypePhoto}),
		own(RemoteMediaItem{MediaKey: "P3", FileName: "IMG_0003.heic", Type: MediaTypePhoto}),
		own(RemoteMediaItem{MediaKey: "V3", FileName: "IMG_0003.mov", Type: MediaTypeVideo}),
		// Only a JPEG: excluded, it may be an edit or a shared copy.
		own(RemoteMediaItem{MediaKey: "P4", FileName: "IMG_0004.JPG", Type: MediaTypePhoto}),
		own(RemoteMediaItem{MediaKey: "V4", FileName: "IMG_0004.MOV", Type: MediaTypeVideo}),
		// Same name, months apart: excluded.
		own(RemoteMediaItem{MediaKey: "P5", FileName: "IMG_0465.HEIC", Type: MediaTypePhoto, UTCTimestamp: 1702714832564}),
		own(RemoteMediaItem{MediaKey: "V5", FileName: "IMG_0465.MOV", Type: MediaTypeVideo, UTCTimestamp: 1713330892000}),
		// Two camera originals: ambiguous.
		own(RemoteMediaItem{MediaKey: "P6a", FileName: "IMG_0006.heic", Type: MediaTypePhoto}),
		own(RemoteMediaItem{MediaKey: "P6b", FileName: "IMG_0006.heif", Type: MediaTypePhoto}),
		own(RemoteMediaItem{MediaKey: "V6", FileName: "IMG_0006.mov", Type: MediaTypeVideo}),
		// The same file listed twice counts once.
		own(RemoteMediaItem{MediaKey: "P7a", FileName: "IMG_0007.HEIC", Type: MediaTypePhoto, DedupKey: "D7"}),
		own(RemoteMediaItem{MediaKey: "P7b", FileName: "IMG_0007.HEIC", Type: MediaTypePhoto, DedupKey: "D7"}),
		own(RemoteMediaItem{MediaKey: "V7", FileName: "IMG_0007.MOV", Type: MediaTypeVideo}),
		// Shared, partner, non-canonical and trashed items never take part.
		own(RemoteMediaItem{MediaKey: "P8", FileName: "IMG_0008.HEIC", Type: MediaTypePhoto, Origin: OriginShared}),
		own(RemoteMediaItem{MediaKey: "V8", FileName: "IMG_0008.MOV", Type: MediaTypeVideo}),
		{MediaKey: "P9", FileName: "IMG_0009.HEIC", Type: MediaTypePhoto, UTCTimestamp: shot},
		own(RemoteMediaItem{MediaKey: "V9", FileName: "IMG_0009.MOV", Type: MediaTypeVideo}),
		own(RemoteMediaItem{MediaKey: "P10", FileName: "IMG_0010.HEIC", Type: MediaTypePhoto}),
		own(RemoteMediaItem{MediaKey: "V10", FileName: "IMG_0010.MOV", Type: MediaTypeVideo, Trashed: true}),
		// Non-.mov videos and videos without any still are ignored.
		own(RemoteMediaItem{MediaKey: "V11", FileName: "IMG_0011.mp4", Type: MediaTypeVideo}),
		own(RemoteMediaItem{MediaKey: "P11", FileName: "IMG_0011.HEIC", Type: MediaTypePhoto}),
		own(RemoteMediaItem{MediaKey: "V12", FileName: "CLIP_0001.mov", Type: MediaTypeVideo}),
	}

	candidates, ambiguous, excluded := FindLivePhotoRepairCandidates(items)

	got := map[string]string{}
	for _, candidate := range candidates {
		got[candidate.Stem] = candidate.Photo.MediaKey + "+" + candidate.Video.MediaKey
	}
	want := map[string]string{"img_0002": "P2+V2", "img_0003": "P3+V3", "img_0007": "P7a+V7"}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for stem, pair := range want {
		if got[stem] != pair {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
	}
	if len(ambiguous) != 1 || ambiguous[0] != "img_0006" {
		t.Fatalf("ambiguous = %v", ambiguous)
	}
	reasons := map[string]string{}
	for _, exclusion := range excluded {
		reasons[exclusion.Stem] = exclusion.Reason
	}
	if len(reasons) != 2 || !strings.Contains(reasons["img_0004"], "JPEG") || !strings.Contains(reasons["img_0465"], "not the same moment") {
		t.Fatalf("excluded = %+v", excluded)
	}

	// The report carries the same three lists.
	report := FindLivePhotoRepairReport(items)
	if len(report.Candidates) != 3 || len(report.Ambiguous) != 1 || len(report.Excluded) != 2 {
		t.Fatalf("report = %+v", report)
	}
}

func TestSameCaptureWindow(t *testing.T) {
	video := &RemoteMediaItem{UTCTimestamp: 1709461163000}
	for _, test := range []struct {
		photo int64
		ok    bool
	}{
		{1709461164526, true},  // 1.5 s after: a normal Live Photo
		{1709461162500, true},  // slightly before (video time is rounded)
		{1709461167500, false}, // 4.5 s after
		{1709461161000, false}, // 2 s before
		{0, false},             // no capture time
	} {
		problem := sameCaptureProblem(&RemoteMediaItem{UTCTimestamp: test.photo}, video)
		if (problem == "") != test.ok {
			t.Fatalf("photo at %d: problem %q, want ok=%v", test.photo, problem, test.ok)
		}
	}
}

func TestRepairLivePhotoPairSkipsWithoutNetwork(t *testing.T) {
	// Every path that fails before any network call must return a skipped
	// outcome, not touch the API.
	api := &Api{}

	candidate := LivePhotoRepairCandidate{
		Stem:  "img_0001",
		Photo: &RemoteMediaItem{MediaKey: "P", FileName: "IMG_0001.jpg"},
		Video: &RemoteMediaItem{MediaKey: "V", FileName: "IMG_0001.mov", Sha1: []byte(strings.Repeat("v", 20))},
	}
	// The library has no digest for the still.
	outcome, err := api.RepairLivePhotoPair(context.Background(), candidate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != "skipped" || !strings.Contains(outcome.Message, "no usable digest") {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}

	// The library has no capture time for the still: linking would send a
	// wrong date.
	candidate.Photo = &RemoteMediaItem{MediaKey: "P", FileName: "IMG_0001.jpg", Sha1: []byte(strings.Repeat("p", 20))}
	candidate.Video = &RemoteMediaItem{MediaKey: "V", FileName: "IMG_0001.mov", Sha1: []byte(strings.Repeat("v", 20))}
	outcome, err = api.RepairLivePhotoPair(context.Background(), candidate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != "skipped" || !strings.Contains(outcome.Message, "no capture time") {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}

	// The library has no digest for the video (saver quality).
	candidate.Photo = &RemoteMediaItem{MediaKey: "P", FileName: "IMG_0001.jpg", Sha1: []byte(strings.Repeat("p", 20))}
	candidate.Video = &RemoteMediaItem{MediaKey: "V", FileName: "IMG_0001.mov"}
	outcome, err = api.RepairLivePhotoPair(context.Background(), candidate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != "skipped" || !strings.Contains(outcome.Message, "no usable digest") {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
}

func TestRepairLivePhotoPairsStopsOnCancel(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	candidates := []LivePhotoRepairCandidate{
		{Stem: "a", Photo: &RemoteMediaItem{FileName: "A.jpg"}, Video: &RemoteMediaItem{FileName: "A.mov"}},
		{Stem: "b", Photo: &RemoteMediaItem{FileName: "B.jpg"}, Video: &RemoteMediaItem{FileName: "B.mov"}},
	}
	var events []LivePhotoRepairProgress
	outcomes := (&Api{}).RepairLivePhotoPairs(ctx, candidates, func(p LivePhotoRepairProgress) {
		events = append(events, p)
	})
	if len(outcomes) != 2 {
		t.Fatalf("expected an outcome per candidate, got %+v", outcomes)
	}
	for _, outcome := range outcomes {
		if outcome.Status != "skipped" || !strings.Contains(outcome.Message, "cancelled") {
			t.Fatalf("unexpected outcome: %+v", outcome)
		}
	}
	if len(events) != 0 {
		t.Fatalf("no pair was attempted, yet progress was reported: %+v", events)
	}
}

func TestRepairLivePhotoPairsReportsEveryPair(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")

	// Both pairs are skipped before any network call (no digests).
	candidates := []LivePhotoRepairCandidate{
		{Stem: "a", Photo: &RemoteMediaItem{FileName: "A.jpg"}, Video: &RemoteMediaItem{FileName: "A.mov"}},
		{Stem: "b", Photo: &RemoteMediaItem{MediaKey: "PB", FileName: "B.jpg"}, Video: &RemoteMediaItem{FileName: "B.mov"}},
	}
	var done []LivePhotoRepairProgress
	outcomes := (&Api{}).RepairLivePhotoPairs(context.Background(), candidates, func(p LivePhotoRepairProgress) {
		if p.Stage == RepairStageDone {
			done = append(done, p)
		}
	})
	if len(outcomes) != 2 || len(done) != 2 {
		t.Fatalf("outcomes=%+v done=%+v", outcomes, done)
	}
	if done[1].Outcome == nil || done[1].Outcome.PhotoMediaKey != "PB" {
		t.Fatalf("outcome should carry the photo's media key: %+v", done[1].Outcome)
	}
	if done[1].Index != 2 || done[1].Total != 2 || done[1].FileName != "B.mov" || done[1].Outcome == nil || done[1].Outcome.Status != "skipped" {
		t.Fatalf("unexpected final event: %+v", done[1])
	}
	if stats := LoadUploadStats(); stats.RepairedLivePhotos != 0 {
		t.Fatalf("skipped pairs must not count as repaired: %+v", stats)
	}
}

func TestCleanupStaleRepairDownloads(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("TMP", directory)
	t.Setenv("TEMP", directory)
	t.Setenv("TMPDIR", directory)

	stale := filepath.Join(directory, "gotohp-repair-old.mov")
	fresh := filepath.Join(directory, "gotohp-repair-new.mov")
	other := filepath.Join(directory, "unrelated-old.mov")
	for _, path := range []string{stale, fresh, other} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-staleRepairTempAge - time.Hour)
	for _, path := range []string{stale, other} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	CleanupStaleRepairDownloads()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale repair download should be removed, stat err = %v", err)
	}
	for _, path := range []string{fresh, other} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s should be kept: %v", filepath.Base(path), err)
		}
	}
}

func TestStallWatchdog(t *testing.T) {
	ctx, kick, stop := stallWatchdog(context.Background(), 60*time.Millisecond)
	defer stop()
	for range 4 {
		time.Sleep(30 * time.Millisecond)
		kick()
	}
	if ctx.Err() != nil {
		t.Fatal("a transfer that keeps moving must not be cancelled")
	}
	time.Sleep(150 * time.Millisecond)
	if ctx.Err() == nil {
		t.Fatal("a stalled transfer must be cancelled")
	}
	if err := transferError(ctx, ctx.Err()); err != errTransferStalled {
		t.Fatalf("transferError = %v, want errTransferStalled", err)
	}
}

func TestLibraryTimeReadsMilliseconds(t *testing.T) {
	// A real library value: 3 March 2024 10:19:24.526 UTC.
	if got := libraryTime(1709461164526).UTC(); !got.Equal(time.Date(2024, 3, 3, 10, 19, 24, 526_000_000, time.UTC)) {
		t.Fatalf("milliseconds: got %v", got)
	}
	// A value in seconds stays a plausible date instead of a far-future one.
	if got := libraryTime(1700000000).UTC(); got.Year() != 2023 {
		t.Fatalf("seconds: got %v", got)
	}
}
