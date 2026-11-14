package core

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app/generated"

	"google.golang.org/protobuf/proto"
)

// readTestHex loads one of the reference byte streams in core/testdata. The
// hex files were produced by xob0t/gpmc's own blackboxprotobuf encoder (see
// scripts/gen_library_requests.py), so a byte-exact match proves the Go
// encoder matches the Google Photos mobile app.
func readTestHex(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".hex"))
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	data, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("decode testdata %s: %v", name, err)
	}
	return data
}

// The test values must match scripts/gen_library_requests.py.
const (
	testSyncToken         = "SYNC_TEST_TOKEN"
	testResumeToken       = "RESUME_TEST_TOKEN"
	testMediaKey          = "MEDIA_TEST_KEY"
	testDedupKey          = "DEDUP_TEST_KEY"
	testClientVersion     = int64(49029607)
	testAndroidAPIVersion = int64(28)
)

func assertBytes(t *testing.T, name string, got []byte) {
	t.Helper()
	want := readTestHex(t, name)
	if !bytes.Equal(got, want) {
		t.Fatalf("%s =\n %x\nwant\n %x", name, got, want)
	}
}

func TestLibraryRequestBodiesMatchGPMC(t *testing.T) {
	assertBytes(t, "library_state", encodePBTree(libraryStateRequestBody(testSyncToken)))
	assertBytes(t, "library_page_init", encodePBTree(libraryPageInitRequestBody(testResumeToken)))
	assertBytes(t, "library_page", encodePBTree(libraryPageRequestBody(testResumeToken, testSyncToken)))
}

func TestMediaStateRequestsMatchGPMC(t *testing.T) {
	trash := mediaStateUpdate(mediaStateActionTrash, mediaStateConfirmTrash, true, testClientVersion, testAndroidAPIVersion, []string{testDedupKey})
	assertBytes(t, "media_state_trash", mustMarshal(t, trash))

	deleteReq := mediaStateUpdate(mediaStateActionDelete, mediaStateConfirmDelete, true, testClientVersion, testAndroidAPIVersion, []string{testDedupKey})
	assertBytes(t, "media_state_delete", mustMarshal(t, deleteReq))

	restore := mediaStateUpdate(mediaStateActionRestore, mediaStateConfirmDelete, false, testClientVersion, testAndroidAPIVersion, []string{testDedupKey})
	assertBytes(t, "media_state_restore", mustMarshal(t, restore))
}

func TestPrepareDownloadRequestMatchesGPMC(t *testing.T) {
	assertBytes(t, "prepare_download", mustMarshal(t, prepareDownloadRequest(testMediaKey)))
}

func mustMarshal(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestParseLibrarySyncResponse(t *testing.T) {
	// A canonical quota-charged item: the dedup key arrives in the unmodeled
	// container at 2.21 (subfield 11 here), preceded by an unrelated subfield
	// that must be skipped.
	item := nMsg(2,
		nStr(1, "MEDIA_KEY_1"),
		nMsg(2,
			nStr(4, "IMG_0001.jpg"),
			nVarint(7, 1700000000),
			nVarint(10, 54321),
			nMsg(5, nVarint(1, 27)), // non-canonical marker
			nMsg(13, nBytes(1, "\xaa\xbb\xcc")),
			nMsg(16, nVarint(3, 1690000000)),
			nVarint(26, 7),
			nMsg(30, nVarint(1, OriginShared)),
			nMsg(35, nVarint(2, 424242), nVarint(3, 2)),
			nMsg(21,
				nStr(2, "not-a-dedup-key"),
				nStr(11, "DEDUP_FROM_SERVER"),
			),
		),
		nMsg(5, nVarint(1, MediaTypePhoto)),
	)
	// A trashed video item without a usable dedup container: the dedup key
	// falls back to the URL-safe base64 of the SHA-1.
	trashedItem := nMsg(2,
		nStr(1, "MEDIA_KEY_2"),
		nMsg(2,
			nStr(4, "VID_0002.mp4"),
			nVarint(7, 1700000200),
			nVarint(10, 9999999),
			nMsg(13, nBytes(1, "\x01\x02\x03\x04")),
			nMsg(16, nVarint(3, 1690000500)),
			nMsg(30, nVarint(1, OriginSelf)),
			nMsg(35, nVarint(2, 100), nVarint(3, 2)),
			nMsg(21, nStr(2, "ignored")),
		),
		nMsg(5, nVarint(1, MediaTypeVideo)),
	)
	// An uncharged item with neither container nor SHA-1: the media key is
	// the last-resort dedup key.
	plainItem := nMsg(2,
		nStr(1, "MEDIA_KEY_3"),
		nMsg(2,
			nStr(4, "P_0003.jpg"),
			nVarint(7, 1700000400),
			nVarint(10, 123),
			nMsg(35, nVarint(2, 0), nVarint(3, 1)),
		),
		nMsg(5, nVarint(1, MediaTypePhoto)),
	)
	body := encodePBTree([]pbNode{
		nMsg(1,
			nStr(1, "PAGE_RESUME"),
			item,
			trashedItem,
			plainItem,
			nStr(6, "NEXT_SYNC"),
			nMsg(9, nMsg(1, nVarint(1, 1), nMsg(2, nStr(1, "MEDIA_KEY_DELETED")))),
		),
	})

	page, err := parseLibrarySyncResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if page.ResumeToken != "PAGE_RESUME" || page.SyncToken != "NEXT_SYNC" {
		t.Fatalf("tokens = %q/%q", page.ResumeToken, page.SyncToken)
	}
	if len(page.Deletions) != 1 || page.Deletions[0] != "MEDIA_KEY_DELETED" {
		t.Fatalf("deletions = %v", page.Deletions)
	}
	if len(page.Items) != 3 {
		t.Fatalf("items = %d", len(page.Items))
	}

	first, second, third := page.Items[0], page.Items[1], page.Items[2]
	if first.MediaKey != "MEDIA_KEY_1" || first.FileName != "IMG_0001.jpg" ||
		first.SizeBytes != 54321 || first.QuotaChargedBytes != 424242 ||
		!first.OriginalQuality || first.IsCanonical || first.Origin != OriginShared ||
		first.Type != MediaTypePhoto || !first.Trashed ||
		first.TrashTimestamp != 1690000000 || first.ContentVersion != 7 ||
		first.UTCTimestamp != 1700000000 {
		t.Fatalf("first item = %#v", first)
	}
	if first.DedupKey != "DEDUP_FROM_SERVER" {
		t.Fatalf("first dedup key = %q", first.DedupKey)
	}
	if second.DedupKey != "AQIDBA" { // urlsafe base64 of 01020304, no padding
		t.Fatalf("second dedup key = %q", second.DedupKey)
	}
	if !second.Trashed || second.Type != MediaTypeVideo || second.Origin != OriginSelf {
		t.Fatalf("second item = %#v", second)
	}
	if third.QuotaChargedBytes != 0 || third.OriginalQuality || third.Trashed {
		t.Fatalf("third item = %#v", third)
	}
	if third.DedupKey != "MEDIA_KEY_3" {
		t.Fatalf("third dedup key = %q", third.DedupKey)
	}
}

func TestSummarizeQuota(t *testing.T) {
	items := []*RemoteMediaItem{
		{QuotaChargedBytes: 100, Trashed: true},
		{QuotaChargedBytes: 250},
		{QuotaChargedBytes: 0},
	}
	summary := SummarizeQuota(items)
	if summary.TotalBytes != 350 || summary.TrashedBytes != 100 ||
		summary.ItemCount != 2 || summary.TrashedCount != 1 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestUniqueQuotaChargedMedia(t *testing.T) {
	items := []*RemoteMediaItem{
		{MediaKey: "copy", DedupKey: "a", QuotaChargedBytes: 100},
		{MediaKey: "free", DedupKey: "f"},
		{MediaKey: "canonical", DedupKey: "a", QuotaChargedBytes: 100, IsCanonical: true},
		{MediaKey: "same-name-other-photo", DedupKey: "b", QuotaChargedBytes: 40, IsCanonical: true},
		{MediaKey: "trashed-canonical", DedupKey: "a", QuotaChargedBytes: 100, IsCanonical: true, Trashed: true},
	}
	unique, copies := UniqueQuotaChargedMedia(items)
	if len(unique) != 2 || unique[0].MediaKey != "canonical" || unique[1].MediaKey != "same-name-other-photo" {
		t.Fatalf("unique = %#v", unique)
	}
	if copies["a"] != 3 || copies["b"] != 1 || copies["f"] != 0 {
		t.Fatalf("copies = %#v", copies)
	}
	if summary := SummarizeQuota(unique); summary.TotalBytes != 140 || summary.ItemCount != 2 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestPrepareDownloadResponseParsing(t *testing.T) {
	body := encodePBTree([]pbNode{
		nMsg(1,
			nMsg(5,
				nMsg(2,
					nStr(5, "https://edited.example/file"),
					nStr(6, "https://original.example/file"),
				),
			),
		),
	})
	var pbResp generated.PrepareDownloadResponse
	if err := proto.Unmarshal(body, &pbResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := pbResp.GetField1().GetField5().GetField2().GetOriginalUrl(); got != "https://original.example/file" {
		t.Fatalf("original url = %q", got)
	}
	if got := pbResp.GetField1().GetField5().GetField2().GetEditedUrl(); got != "https://edited.example/file" {
		t.Fatalf("edited url = %q", got)
	}
}
