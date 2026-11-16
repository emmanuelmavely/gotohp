package core

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"app/generated"

	"google.golang.org/protobuf/proto"
)

func TestLibraryIndexRoundTripAndHashes(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")

	shaA := []byte{0xaa, 0xbb, 0xcc}
	index := &LibraryIndex{
		Version:   LibraryIndexVersion,
		SyncedAt:  1700000000,
		SyncToken: "TOKEN_1",
		Items: []*RemoteMediaItem{
			{MediaKey: "K1", FileName: "a.jpg", Sha1: shaA, SizeBytes: 100, QuotaChargedBytes: 100},
			{MediaKey: "K2", FileName: "b.jpg", Sha1: []byte{0x01}, SizeBytes: 200, QuotaChargedBytes: 200, Trashed: true},
		},
	}
	if err := index.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadLibraryIndex()
	if err != nil || loaded == nil {
		t.Fatalf("load: %v %v", loaded, err)
	}
	if loaded.SyncToken != "TOKEN_1" || len(loaded.Items) != 2 {
		t.Fatalf("unexpected index: %+v", loaded)
	}
	if string(loaded.Items[0].Sha1) != string(shaA) || loaded.Items[1].FileName != "b.jpg" {
		t.Fatalf("items not preserved: %+v", loaded.Items)
	}

	hashes := loaded.UploadedHashes()
	if len(hashes) != 1 {
		t.Fatalf("expected only the non-trashed hash, got %d", len(hashes))
	}
	if _, ok := hashes[hex.EncodeToString(shaA)]; !ok {
		t.Fatal("expected shaA in the uploaded hash set")
	}
}

// syncApi builds an Api around a test server with a long-lived bearer token.
func syncApi(server *httptest.Server) *Api {
	return &Api{
		client:            server.Client(),
		language:          "en-US",
		userAgent:         "test",
		authResponseCache: map[string]string{"Expiry": "9999999999", "Auth": "test-token"},
	}
}

var originalEndpoint = LibrarySyncEndpoint

func syncLibraryResponse(resumeToken, syncToken string, items []*generated.LibrarySyncResponseMediaItemType, deletedKeys []string) []byte {
	var deletions []*generated.LibrarySyncResponseDeletionItemType
	for _, key := range deletedKeys {
		deletions = append(deletions, &generated.LibrarySyncResponseDeletionItemType{
			Field1: &generated.LibrarySyncResponseDeletionItemTypeField1Type{
				Field2: &generated.LibrarySyncResponseDeletionItemTypeField1TypeField2Type{
					Field1: key,
				},
			},
		})
	}
	body, err := proto.Marshal(&generated.LibrarySyncResponse{
		Field1: &generated.LibrarySyncResponseField1Type{
			ResumeToken: resumeToken,
			SyncToken:   syncToken,
			Items:       items,
			Deletions:   deletions,
		},
	})
	if err != nil {
		panic(err)
	}
	return body
}

func readAllBody(r *http.Request) []byte {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil
	}
	return body
}

func contains(haystack, needle []byte) bool {
	return bytes.Contains(haystack, needle)
}

func TestSyncLibraryIncrementalAppliesDelta(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")

	// The cached library: K1 (100 bytes, charged) and K3 (70 bytes, charged).
	cached := &LibraryIndex{
		Version:     LibraryIndexVersion,
		SyncToken:   "TOKEN_OLD",
		AlbumsKnown: true,
		Items: []*RemoteMediaItem{
			{MediaKey: "K1", FileName: "a.jpg", SizeBytes: 100, QuotaChargedBytes: 100, IsCanonical: true},
			{MediaKey: "K3", FileName: "c.jpg", SizeBytes: 70, QuotaChargedBytes: 70, IsCanonical: true},
		},
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("TOKEN_OLD")) {
			t.Errorf("delta state request must carry the cached sync token, body: %x", body)
		}
		if requests > 1 {
			t.Errorf("expected a single delta request, got %d", requests)
		}
		// Delta: K1 replaced by a 200-byte version, K3 deleted, K2 added.
		items := []*generated.LibrarySyncResponseMediaItemType{
			{
				MediaKey: "K1",
				Field2: &generated.LibrarySyncResponseMediaItemTypeField2Type{
					FileName:    "a.jpg",
					SizeBytes:   200,
					Sha1:        &generated.LibrarySyncResponseMediaItemTypeField2TypeField13Type{Field1: []byte{0x11}},
					QualityInfo: &generated.LibrarySyncResponseMediaItemTypeField2TypeField35Type{QuotaChargedBytes: 200},
					Origin:      &generated.LibrarySyncResponseMediaItemTypeField2TypeField30Type{Field1: OriginSelf},
				},
				Field5: &generated.LibrarySyncResponseMediaItemTypeField5Type{Field1: MediaTypePhoto},
			},
			{
				MediaKey: "K2",
				Field2: &generated.LibrarySyncResponseMediaItemTypeField2Type{
					FileName:    "b.jpg",
					SizeBytes:   50,
					QualityInfo: &generated.LibrarySyncResponseMediaItemTypeField2TypeField35Type{QuotaChargedBytes: 50},
					Origin:      &generated.LibrarySyncResponseMediaItemTypeField2TypeField30Type{Field1: OriginSelf},
				},
				Field5: &generated.LibrarySyncResponseMediaItemTypeField5Type{Field1: MediaTypePhoto},
			},
		}
		_, _ = w.Write(syncLibraryResponse("", "TOKEN_NEW", items, []string{"K3"}))
	}))
	defer server.Close()
	defer func() { LibrarySyncEndpoint = originalEndpoint }()
	LibrarySyncEndpoint = server.URL

	var lastStats LibrarySyncStats
	index, err := syncApi(server).SyncLibraryIncremental(cached, func(s LibrarySyncStats) { lastStats = s })
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected 1 request, got %d", requests)
	}
	if index.SyncToken != "TOKEN_NEW" {
		t.Fatalf("sync token = %q", index.SyncToken)
	}
	if len(index.Items) != 2 {
		t.Fatalf("expected 2 items, got %+v", index.Items)
	}
	// K1 keeps its first-seen position and was replaced by the newer entry.
	if index.Items[0].MediaKey != "K1" || index.Items[0].SizeBytes != 200 {
		t.Fatalf("K1 not replaced in place: %+v", index.Items[0])
	}
	if index.Items[1].MediaKey != "K2" {
		t.Fatalf("unexpected second item: %+v", index.Items[1])
	}
	if lastStats.TotalBytes != 250 || lastStats.ChargedBytes != 250 || lastStats.ChargedItems != 2 {
		t.Fatalf("running totals off: %+v", lastStats)
	}
}

// TestSyncLibraryIncrementalFallbackToFull checks that a rejected delta (for
// example an expired sync token) falls back to a full walk.
func TestSyncLibraryIncrementalFallbackToFull(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")

	cached := &LibraryIndex{Version: LibraryIndexVersion, SyncToken: "STALE", AlbumsKnown: true,
		Items: []*RemoteMediaItem{{MediaKey: "K1", SizeBytes: 100, QuotaChargedBytes: 100}}}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad sync token"))
			return
		}
		// Full sync: state page with no resume token.
		items := []*generated.LibrarySyncResponseMediaItemType{
			{
				MediaKey: "K1",
				Field2: &generated.LibrarySyncResponseMediaItemTypeField2Type{
					FileName:    "a.jpg",
					SizeBytes:   100,
					QualityInfo: &generated.LibrarySyncResponseMediaItemTypeField2TypeField35Type{QuotaChargedBytes: 100},
				},
				Field5: &generated.LibrarySyncResponseMediaItemTypeField5Type{Field1: MediaTypePhoto},
			},
		}
		_, _ = w.Write(syncLibraryResponse("", "TOKEN_FULL", items, nil))
	}))
	defer server.Close()
	defer func() { LibrarySyncEndpoint = originalEndpoint }()
	LibrarySyncEndpoint = server.URL

	index, err := syncApi(server).SyncLibraryIncremental(cached, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected 2 requests (rejected delta + full), got %d", requests)
	}
	if index.SyncToken != "TOKEN_FULL" || len(index.Items) != 1 {
		t.Fatalf("unexpected index: %+v", index)
	}
}

func TestBuildDryRunReportClassifiesByHash(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")

	directory := t.TempDir()
	uploaded := writeTestFile(t, directory, "uploaded.jpg", "already there")
	fresh := writeTestFile(t, directory, "fresh.jpg", "brand new")

	sumUploaded := sha1.Sum([]byte("already there"))
	uploadedHashes := map[string]struct{}{
		hex.EncodeToString(sumUploaded[:]): {},
	}

	for _, tc := range []struct {
		name         string
		force        bool
		wantUploaded int
		wantUpload   int
	}{
		{"default", false, 1, 1},
		{"force", true, 0, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := UploadOptions{ForceUpload: tc.force}
			report, err := BuildDryRunReport(context.Background(), []string{directory}, opts, nil, uploadedHashes)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if report.SkippedCount != 0 {
				t.Fatalf("unexpected skips: %+v", report)
			}
			if report.UploadedCount != tc.wantUploaded || report.UploadCount != tc.wantUpload {
				t.Fatalf("counts = %d/%d, want %d/%d (entries: %+v)",
					report.UploadedCount, report.UploadCount, tc.wantUploaded, tc.wantUpload, report.Entries)
			}
			if !tc.force {
				if report.UploadedBytes != int64(len("already there")) {
					t.Fatalf("uploaded bytes = %d", report.UploadedBytes)
				}
				if report.UploadBytes != int64(len("brand new")) {
					t.Fatalf("upload bytes = %d", report.UploadBytes)
				}
				byName := make(map[string]DryRunEntry, len(report.Entries))
				for _, entry := range report.Entries {
					byName[filepath.Base(entry.Path)] = entry
				}
				if !byName[filepath.Base(uploaded)].Uploaded {
					t.Fatalf("uploaded.jpg must be marked uploaded: %+v", byName)
				}
				if byName[filepath.Base(fresh)].Uploaded {
					t.Fatalf("fresh.jpg must be marked to-upload: %+v", byName)
				}
			}
		})
	}
}

func TestBuildDryRunReportSkipsIncompleteLivePhotos(t *testing.T) {
	restoreConfigGlobals(t)
	ConfigPath = filepath.Join(t.TempDir(), "gotohp.config")

	directory := t.TempDir()
	_ = writeTestFile(t, directory, "IMG_0001.jpg", "photo")
	// No matching IMG_0001.mov: the pair is incomplete and skipped.

	report, err := BuildDryRunReport(context.Background(), []string{directory},
		UploadOptions{PairLivePhotos: true, SkipIncompleteLivePhotos: true, IgnoreAppleMetadata: true}, nil, nil)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if report.SkippedCount != 1 {
		t.Fatalf("expected 1 preflight skip, got %+v", report)
	}
	if len(report.Entries) != 0 {
		t.Fatalf("skipped items must not be entries: %+v", report.Entries)
	}
}
