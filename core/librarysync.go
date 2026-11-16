package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"app/generated"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// LibrarySyncEndpoint is the library sync RPC returning library state,
// initial pages and delta pages; all three share the response shape. It is a
// variable so tests can point it at a local server.
var LibrarySyncEndpoint = "https://photosdata-pa.googleapis.com/6439526531001121323/18047484249733410717"

// librarySyncPageTimeout bounds one library sync request, response body
// included.
const librarySyncPageTimeout = 2 * time.Minute

// Media type values reported at item path 5.1.
const (
	MediaTypePhoto int64 = 1
	MediaTypeVideo int64 = 2
)

// Origin values reported at item path 2.30.1.
const (
	OriginSelf    int64 = 1
	OriginPartner int64 = 3
	OriginShared  int64 = 4
)

// RemoteMediaItem is one media item as reported by the library sync.
type RemoteMediaItem struct {
	MediaKey          string
	FileName          string
	DedupKey          string
	Sha1              []byte
	SizeBytes         int64
	QuotaChargedBytes int64
	// OriginalQuality reports whether the item is stored in its original
	// (quota-charged) quality.
	OriginalQuality bool
	Type            int64
	Origin          int64
	// IsCanonical is false for non-canonical duplicates of shared or
	// partner items.
	IsCanonical bool
	Trashed     bool
	// TrashTimestamp is 0 for items that are not in the trash.
	TrashTimestamp int64
	ContentVersion int64
	// UTCTimestamp is when the item was taken, in milliseconds since the Unix
	// epoch. Photos keep the milliseconds; videos are rounded to seconds.
	UTCTimestamp int64
	// HasMotion is true for a photo stored with a motion part: a Live Photo
	// or motion photo, reported at item path 5.5.2.4 (micro video).
	HasMotion bool `json:",omitempty"`
	// Favorite (2.31.1), Archived (2.29.1) and EditedInPhotos (5.2.4, an
	// edit saved in Google Photos) describe what only exists in the library.
	Favorite       bool `json:",omitempty"`
	Archived       bool `json:",omitempty"`
	EditedInPhotos bool `json:",omitempty"`
}

// RemoteAlbum is one album (a library collection) as the library sync
// reports it at response path 1.3. Paths follow gpmc's collection parser.
type RemoteAlbum struct {
	// MediaKey is the album key used to add items (AF1Qip...).
	MediaKey string
	Title    string
	// ItemCount is the number of items in the album (2.7).
	ItemCount int64
	// Type is the collection type (2.8); its values are not documented.
	Type int64
	// CoverMediaKey names the cover item (2.17.1).
	CoverMediaKey string
	// LastActivity is the last change, in Unix milliseconds (2.10.10).
	LastActivity int64
}

// LibrarySyncPage is one parsed library sync response.
type LibrarySyncPage struct {
	// ResumeToken continues pagination within the current sync cycle; it is
	// empty once all pages were fetched.
	ResumeToken string
	// SyncToken identifies the sync context for the next cycle.
	SyncToken string
	Items     []*RemoteMediaItem
	// Deletions lists media keys that were deleted remotely since the last
	// sync cycle.
	Deletions []string
	// Albums lists albums that are new or changed; AlbumDeletions the keys
	// of albums that were deleted.
	Albums         []*RemoteAlbum
	AlbumDeletions []string
}

// postLibrarySync sends a request body to the library sync endpoint.
func (a *Api) postLibrarySync(body []byte) ([]byte, error) {
	bearerToken, err := a.BearerToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get bearer token: %w", err)
	}

	// The shared client has no timeout (uploads can be long), so bound each
	// sync page here; otherwise a stalled connection hangs the sync silently.
	ctx, cancel := context.WithTimeout(context.Background(), librarySyncPageTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", LibrarySyncEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Accept-Language", a.language)
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("User-Agent", a.userAgent)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("x-goog-ext-173412678-bin", "CgcIAhClARgC")
	req.Header.Set("x-goog-ext-174067345-bin", "CgIIAg==")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := ReadResponseBody(resp)
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return ReadResponseBody(resp)
}

// GetLibraryState fetches the library state for a sync token. An empty
// syncToken starts a full (CURRENT_SYNC from scratch) cycle; passing the
// token from a previous cycle requests the delta.
func (a *Api) GetLibraryState(syncToken string) (*LibrarySyncPage, error) {
	return a.fetchLibrarySyncPage(encodePBTree(libraryStateRequestBody(syncToken)))
}

// GetLibraryPageInit fetches the first library page of a new sync cycle.
func (a *Api) GetLibraryPageInit(resumeToken string) (*LibrarySyncPage, error) {
	return a.fetchLibrarySyncPage(encodePBTree(libraryPageInitRequestBody(resumeToken)))
}

// GetLibraryPage fetches a delta library page of the sync cycle identified
// by syncToken.
func (a *Api) GetLibraryPage(resumeToken, syncToken string) (*LibrarySyncPage, error) {
	return a.fetchLibrarySyncPage(encodePBTree(libraryPageRequestBody(resumeToken, syncToken)))
}

func (a *Api) fetchLibrarySyncPage(body []byte) (*LibrarySyncPage, error) {
	respBytes, err := a.postLibrarySync(body)
	if err != nil {
		return nil, err
	}
	return parseLibrarySyncResponse(respBytes)
}

// parseLibrarySyncResponse decodes one library sync response.
func parseLibrarySyncResponse(body []byte) (*LibrarySyncPage, error) {
	var pbResp generated.LibrarySyncResponse
	if err := proto.Unmarshal(body, &pbResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal library sync response: %w", err)
	}

	page := &LibrarySyncPage{
		ResumeToken: pbResp.GetField1().GetResumeToken(),
		SyncToken:   pbResp.GetField1().GetSyncToken(),
	}
	for _, pbItem := range pbResp.GetField1().GetItems() {
		page.Items = append(page.Items, parseRemoteMediaItem(pbItem))
	}
	for _, pbDeletion := range pbResp.GetField1().GetDeletions() {
		if key := pbDeletion.GetField1().GetField2().GetField1(); key != "" {
			page.Deletions = append(page.Deletions, key)
			continue
		}
		// Deletion type 4 is a collection (album), keyed at 1.5.2.
		if deletion := pbDeletion.GetField1(); deletion != nil && deletion.GetField1() == 4 {
			if key := protoStringAt(deletion.ProtoReflect().GetUnknown(), 5, 2); key != "" {
				page.AlbumDeletions = append(page.AlbumDeletions, key)
			}
		}
	}
	// Collections (albums) are at 1.3, outside the modeled schema.
	if field1 := pbResp.GetField1(); field1 != nil {
		for _, raw := range protoRepeatedBytes(field1.ProtoReflect().GetUnknown(), 3) {
			if album := parseRemoteAlbum(raw); album != nil {
				page.Albums = append(page.Albums, album)
			}
		}
	}
	return page, nil
}

// parseRemoteAlbum reads one collection entry; nil when it has no key.
func parseRemoteAlbum(raw []byte) *RemoteAlbum {
	key := protoStringAt(raw, 1)
	if key == "" {
		return nil
	}
	return &RemoteAlbum{
		MediaKey:      key,
		Title:         protoStringAt(raw, 2, 5),
		ItemCount:     int64(protoVarintAt(raw, 2, 7)),
		Type:          int64(protoVarintAt(raw, 2, 8)),
		CoverMediaKey: protoStringAt(raw, 2, 17, 1),
		LastActivity:  int64(protoVarintAt(raw, 2, 10, 10)),
	}
}

// protoRepeatedBytes returns every length-delimited value of field num.
func protoRepeatedBytes(data []byte, num protowire.Number) [][]byte {
	var values [][]byte
	for len(data) > 0 {
		fieldNum, wireType, tagLen := protowire.ConsumeTag(data)
		if tagLen < 0 {
			return values
		}
		data = data[tagLen:]
		if wireType == protowire.BytesType {
			value, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return values
			}
			if fieldNum == num {
				values = append(values, value)
			}
			data = data[n:]
			continue
		}
		n := protowire.ConsumeFieldValue(fieldNum, wireType, data)
		if n < 0 {
			return values
		}
		data = data[n:]
	}
	return values
}

// protoStringAt returns the length-delimited value at the given path as a
// string, or "" when it is absent.
func protoStringAt(data []byte, path ...protowire.Number) string {
	if len(path) == 0 {
		return ""
	}
	for _, value := range protoRepeatedBytes(data, path[0]) {
		if len(path) == 1 {
			return string(value)
		}
		if found := protoStringAt(value, path[1:]...); found != "" {
			return found
		}
	}
	return ""
}

// parseRemoteMediaItem converts one protobuf media item. Paths not modeled in
// the proto schema stay unneeded; the dedup key is recovered from the item's
// unknown fields.
func parseRemoteMediaItem(pbItem *generated.LibrarySyncResponseMediaItemType) *RemoteMediaItem {
	field2 := pbItem.GetField2()
	item := &RemoteMediaItem{
		MediaKey:          pbItem.GetMediaKey(),
		FileName:          field2.GetFileName(),
		Sha1:              field2.GetSha1().GetField1(),
		SizeBytes:         field2.GetSizeBytes(),
		QuotaChargedBytes: field2.GetQualityInfo().GetQuotaChargedBytes(),
		OriginalQuality:   field2.GetQualityInfo().GetQuality() == 2,
		Type:              pbItem.GetField5().GetField1(),
		Origin:            field2.GetOrigin().GetField1(),
		Trashed:           field2.GetTrash().GetField3() > 0,
		TrashTimestamp:    field2.GetTrash().GetField3(),
		ContentVersion:    field2.GetContentVersion(),
		UTCTimestamp:      field2.GetUtcTimestamp(),
	}
	// Property 1 == 27 marks non-canonical duplicates of shared or partner
	// items.
	item.IsCanonical = true
	for _, prop := range field2.GetField5() {
		if prop.GetField1() == 27 {
			item.IsCanonical = false
			break
		}
	}
	item.DedupKey = deriveDedupKey(field2, item.MediaKey)
	item.HasMotion = hasMicroVideo(pbItem.GetField5())
	field2Unknown := field2.ProtoReflect().GetUnknown()
	item.Favorite = protoVarintAt(field2Unknown, 31, 1) == 1
	item.Archived = protoVarintAt(field2Unknown, 29, 1) == 1
	if field5 := pbItem.GetField5(); field5 != nil {
		item.EditedInPhotos = hasProtoPath(field5.ProtoReflect().GetUnknown(), 2, 4)
	}
	return item
}

// protoVarintAt returns the varint at the given path of field numbers (all
// but the last are nested messages), or 0 when it is absent.
func protoVarintAt(data []byte, path ...protowire.Number) uint64 {
	for len(data) > 0 && len(path) > 0 {
		num, wireType, tagLen := protowire.ConsumeTag(data)
		if tagLen < 0 {
			return 0
		}
		data = data[tagLen:]
		switch {
		case num == path[0] && len(path) == 1 && wireType == protowire.VarintType:
			value, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return 0
			}
			return value
		case num == path[0] && len(path) > 1 && wireType == protowire.BytesType:
			value, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return 0
			}
			if found := protoVarintAt(value, path[1:]...); found != 0 {
				return found
			}
			data = data[n:]
		default:
			n := protowire.ConsumeFieldValue(num, wireType, data)
			if n < 0 {
				return 0
			}
			data = data[n:]
		}
	}
	return 0
}

// hasMicroVideo reports whether the item's type container (path 5) carries
// micro video data at 5.5.2.4, which marks a photo with a motion part. The
// path is not modeled in the proto schema, so it is read from the unknown
// fields.
func hasMicroVideo(field5 *generated.LibrarySyncResponseMediaItemTypeField5Type) bool {
	if field5 == nil {
		return false
	}
	return hasProtoPath(field5.ProtoReflect().GetUnknown(), 5, 2, 4)
}

// hasProtoPath reports whether raw protobuf bytes contain a length-delimited
// field at the given path of field numbers.
func hasProtoPath(data []byte, path ...protowire.Number) bool {
	if len(path) == 0 {
		return true
	}
	for len(data) > 0 {
		num, wireType, tagLen := protowire.ConsumeTag(data)
		if tagLen < 0 {
			return false
		}
		data = data[tagLen:]
		if wireType == protowire.BytesType {
			value, valueLen := protowire.ConsumeBytes(data)
			if valueLen < 0 {
				return false
			}
			if num == path[0] && hasProtoPath(value, path[1:]...) {
				return true
			}
			data = data[valueLen:]
			continue
		}
		valueLen := protowire.ConsumeFieldValue(num, wireType, data)
		if valueLen < 0 {
			return false
		}
		data = data[valueLen:]
	}
	return false
}

// deriveDedupKey recovers the item's dedup key: the server sends it in the
// unmodeled container at item path 2.21, whose subfield numbers vary between
// responses. The first subfield whose number starts with "1" holds the key.
// Fallbacks are the URL-safe base64 of the item's SHA-1 and, finally, the
// media key.
func deriveDedupKey(field2 *generated.LibrarySyncResponseMediaItemTypeField2Type, mediaKey string) string {
	if key := dedupKeyFromUnknowns(field2); key != "" {
		return key
	}
	if len(field2.GetSha1().GetField1()) > 0 {
		return urlSafeBase64(base64.StdEncoding.EncodeToString(field2.GetSha1().GetField1()))
	}
	return mediaKey
}

func dedupKeyFromUnknowns(field2 *generated.LibrarySyncResponseMediaItemTypeField2Type) string {
	unknowns := field2.ProtoReflect().GetUnknown()
	for len(unknowns) > 0 {
		fieldNum, wireType, tagLen := protowire.ConsumeTag(unknowns)
		if tagLen < 0 {
			return ""
		}
		unknowns = unknowns[tagLen:]
		var value []byte
		var valueLen int
		if wireType == protowire.BytesType {
			value, valueLen = protowire.ConsumeBytes(unknowns)
		} else {
			valueLen = protowire.ConsumeFieldValue(fieldNum, wireType, unknowns)
		}
		if valueLen < 0 {
			return ""
		}
		if fieldNum == 21 && wireType == protowire.BytesType {
			if key := dedupKeyFromContainer(value); key != "" {
				return key
			}
		}
		unknowns = unknowns[valueLen:]
	}
	return ""
}

// dedupKeyFromContainer walks the field 21 sub-message and returns the first
// subfield whose number starts with "1" (the server varies the exact number).
func dedupKeyFromContainer(container []byte) string {
	for len(container) > 0 {
		fieldNum, wireType, tagLen := protowire.ConsumeTag(container)
		if tagLen < 0 {
			return ""
		}
		container = container[tagLen:]
		var value []byte
		var valueLen int
		if wireType == protowire.BytesType {
			value, valueLen = protowire.ConsumeBytes(container)
		} else {
			valueLen = protowire.ConsumeFieldValue(fieldNum, wireType, container)
		}
		if valueLen < 0 {
			return ""
		}
		if wireType == protowire.BytesType && strings.HasPrefix(strconv.Itoa(int(fieldNum)), "1") {
			return string(value)
		}
		container = container[valueLen:]
	}
	return ""
}

// urlSafeBase64 converts a standard base64 string to its URL-safe form
// without padding, matching what the mobile app sends.
func urlSafeBase64(b64 string) string {
	return strings.NewReplacer("+", "-", "/", "_").Replace(strings.TrimRight(b64, "="))
}

// LibrarySyncStats reports how much a full library sync has fetched so far.
type LibrarySyncStats struct {
	// Pages is the number of responses fetched, the library state included.
	Pages int
	// Items is the number of distinct media items seen.
	Items int
	// TotalBytes is the combined file size of those items.
	TotalBytes int64
	// ChargedItems and ChargedBytes cover the items that count against the
	// storage quota.
	ChargedItems int
	ChargedBytes int64
	// UniqueChargedItems and UniqueChargedBytes count each stored file once
	// (by dedup key), which is how the account is charged. While a sync is
	// running they are a close estimate; UniqueQuotaChargedMedia gives the
	// final figure.
	UniqueChargedItems int
	UniqueChargedBytes int64
}

// SyncLibrary walks the full library sync: it requests the library state and
// then pages through the library until the resume token runs out. It returns
// the library's items and the deletions seen across the pages.
func (a *Api) SyncLibrary() (items []*RemoteMediaItem, deletions []string, syncToken string, err error) {
	items, deletions, syncToken, _, err = a.SyncLibraryWithStats()
	return items, deletions, syncToken, err
}

// SyncLibraryWithStats is SyncLibrary plus counters describing the sync.
func (a *Api) SyncLibraryWithStats() (items []*RemoteMediaItem, deletions []string, syncToken string, stats LibrarySyncStats, err error) {
	return a.SyncLibraryWithProgress(nil)
}

// SyncLibraryWithProgress is SyncLibraryWithStats that also calls onPage with
// the running totals after every fetched page. onPage may be nil. The server
// does not announce the library size up front, so there is no percentage.
func (a *Api) SyncLibraryWithProgress(onPage func(LibrarySyncStats)) (items []*RemoteMediaItem, deletions []string, syncToken string, stats LibrarySyncStats, err error) {
	library, syncToken, stats, err := a.syncLibraryAccumulated(onPage)
	if err != nil {
		return nil, nil, "", stats, err
	}
	return library.items(), library.deletions, syncToken, stats, nil
}

// syncLibraryAccumulated walks the whole library and returns the merged
// result, albums included.
func (a *Api) syncLibraryAccumulated(onPage func(LibrarySyncStats)) (library *libraryAccumulator, syncToken string, stats LibrarySyncStats, err error) {
	report := func(library *libraryAccumulator) {
		stats.Items = len(library.byKey)
		stats.TotalBytes = library.totalBytes
		stats.ChargedItems = library.chargedItems
		stats.ChargedBytes = library.chargedBytes
		stats.UniqueChargedItems = library.uniqueChargedItems
		stats.UniqueChargedBytes = library.uniqueChargedBytes
		if onPage != nil {
			onPage(stats)
		}
	}

	state, err := a.GetLibraryState("")
	if err != nil {
		return nil, "", stats, fmt.Errorf("failed to get library state: %w", err)
	}
	library = newLibraryAccumulator()
	library.apply(state)
	syncToken = state.SyncToken
	stats.Pages = 1
	report(library)

	// Every page of a full sync is fetched with the init request and its resume
	// token alone, as gpmc does. The delta page request (GetLibraryPage) only
	// continues a cycle that was started from an earlier sync token, so using
	// it here ends the walk after the first pages.
	resumeToken := state.ResumeToken
	for resumeToken != "" {
		if stats.Pages > 10000 {
			return nil, "", stats, fmt.Errorf("library sync did not terminate: too many pages")
		}
		page, err := a.GetLibraryPageInit(resumeToken)
		if err != nil {
			return nil, "", stats, fmt.Errorf("failed to get library page %d: %w", stats.Pages+1, err)
		}
		library.apply(page)
		stats.Pages++
		report(library)
		if page.ResumeToken == resumeToken {
			return nil, "", stats, fmt.Errorf("library sync did not advance: resume token repeated on page %d", stats.Pages)
		}
		resumeToken = page.ResumeToken
	}

	return library, syncToken, stats, nil
}

// libraryAccumulator merges sync pages the way a local cache would: a media
// key seen again replaces the earlier copy, and a deletion removes it. Without
// this an item repeated across pages would be counted twice.
type libraryAccumulator struct {
	order     []string
	byKey     map[string]*RemoteMediaItem
	deletions []string

	// Albums by key, in first-seen order.
	albumOrder []string
	albums     map[string]*RemoteAlbum

	// Running totals over byKey, kept exact as items are replaced or deleted.
	totalBytes   int64
	chargedItems int
	chargedBytes int64

	// Quota-charged entries per dedup key, and the totals when every stored
	// file is counted once.
	chargedCopies      map[string]int
	uniqueChargedItems int
	uniqueChargedBytes int64
}

func newLibraryAccumulator() *libraryAccumulator {
	return &libraryAccumulator{
		byKey:         make(map[string]*RemoteMediaItem),
		chargedCopies: make(map[string]int),
		albums:        make(map[string]*RemoteAlbum),
	}
}

func (l *libraryAccumulator) apply(page *LibrarySyncPage) {
	for _, item := range page.Items {
		if previous, seen := l.byKey[item.MediaKey]; seen {
			l.count(previous, -1)
		} else {
			l.order = append(l.order, item.MediaKey)
		}
		l.byKey[item.MediaKey] = item
		l.count(item, 1)
	}
	for _, key := range page.Deletions {
		if item, ok := l.byKey[key]; ok {
			l.count(item, -1)
			delete(l.byKey, key)
		}
	}
	l.deletions = append(l.deletions, page.Deletions...)
	for _, album := range page.Albums {
		if _, seen := l.albums[album.MediaKey]; !seen {
			l.albumOrder = append(l.albumOrder, album.MediaKey)
		}
		l.albums[album.MediaKey] = album
	}
	for _, key := range page.AlbumDeletions {
		delete(l.albums, key)
	}
}

// addAlbum seeds a cached album.
func (l *libraryAccumulator) addAlbum(album *RemoteAlbum) {
	if _, seen := l.albums[album.MediaKey]; !seen {
		l.albumOrder = append(l.albumOrder, album.MediaKey)
	}
	l.albums[album.MediaKey] = album
}

// albumList returns the surviving albums in first-seen order.
func (l *libraryAccumulator) albumList() []*RemoteAlbum {
	albums := make([]*RemoteAlbum, 0, len(l.albums))
	seen := map[string]bool{}
	for _, key := range l.albumOrder {
		if album, ok := l.albums[key]; ok && !seen[key] {
			seen[key] = true
			albums = append(albums, album)
		}
	}
	return albums
}

// count adds (sign 1) or removes (sign -1) one item from the running totals.
func (l *libraryAccumulator) count(item *RemoteMediaItem, sign int) {
	l.totalBytes += int64(sign) * item.SizeBytes
	if item.QuotaChargedBytes > 0 {
		l.chargedItems += sign
		l.chargedBytes += int64(sign) * item.QuotaChargedBytes

		// The first copy of a file adds it to the unique totals and the last
		// copy to go removes it again.
		copies := l.chargedCopies[item.DedupKey]
		if sign > 0 {
			if copies == 0 {
				l.uniqueChargedItems++
				l.uniqueChargedBytes += item.QuotaChargedBytes
			}
			l.chargedCopies[item.DedupKey] = copies + 1
		} else if copies > 0 {
			if copies == 1 {
				l.uniqueChargedItems--
				l.uniqueChargedBytes -= item.QuotaChargedBytes
				delete(l.chargedCopies, item.DedupKey)
			} else {
				l.chargedCopies[item.DedupKey] = copies - 1
			}
		}
	}
}

// items returns the surviving items in the order they were first seen.
func (l *libraryAccumulator) items() []*RemoteMediaItem {
	items := make([]*RemoteMediaItem, 0, len(l.byKey))
	for _, key := range l.order {
		if item, ok := l.byKey[key]; ok {
			items = append(items, item)
			delete(l.byKey, key) // a key re-added after a deletion appears twice in order
		}
	}
	return items
}

// QuotaChargedMedia returns the items that count against the storage quota.
func QuotaChargedMedia(items []*RemoteMediaItem) []*RemoteMediaItem {
	var charged []*RemoteMediaItem
	for _, item := range items {
		if item.QuotaChargedBytes > 0 {
			charged = append(charged, item)
		}
	}
	return charged
}

// UniqueQuotaChargedMedia collapses library entries that point at the same
// stored file. The library lists a file once per place it appears (for
// example again as a non-canonical or shared copy), each entry repeating the
// file's quota charge, while the account is charged for the file once.
// Entries are matched by dedup key, which is derived from the file contents,
// so different photos that merely share a file name stay separate.
//
// It returns one representative entry per file, in first-seen order, and the
// number of entries per dedup key. The canonical, non-trashed entry is
// preferred as the representative.
func UniqueQuotaChargedMedia(items []*RemoteMediaItem) (unique []*RemoteMediaItem, copies map[string]int) {
	copies = make(map[string]int)
	index := make(map[string]int)
	rank := func(item *RemoteMediaItem) int {
		score := 0
		if item.IsCanonical {
			score += 2
		}
		if !item.Trashed {
			score++
		}
		return score
	}
	for _, item := range items {
		if item.QuotaChargedBytes <= 0 {
			continue
		}
		copies[item.DedupKey]++
		i, seen := index[item.DedupKey]
		if !seen {
			index[item.DedupKey] = len(unique)
			unique = append(unique, item)
			continue
		}
		if rank(item) > rank(unique[i]) {
			unique[i] = item
		}
	}
	return unique, copies
}

// ListQuotaChargedMedia returns all items that count against the storage
// quota, across the whole library.
func (a *Api) ListQuotaChargedMedia() ([]*RemoteMediaItem, error) {
	items, _, _, err := a.SyncLibrary()
	if err != nil {
		return nil, err
	}
	return QuotaChargedMedia(items), nil
}

// QuotaSummary totals the storage usage of one library sync.
type QuotaSummary struct {
	// TotalBytes is the sum of the quota-charged bytes of every item.
	TotalBytes int64
	// TrashedBytes is the quota-charged bytes of items in the trash; these
	// still count against the quota until they are deleted permanently.
	TrashedBytes int64
	// ItemCount and TrashedCount are the numbers of quota-charged items.
	ItemCount    int
	TrashedCount int
}

// SummarizeQuota totals the quota usage of the given items.
func SummarizeQuota(items []*RemoteMediaItem) QuotaSummary {
	var s QuotaSummary
	for _, item := range items {
		if item.QuotaChargedBytes <= 0 {
			continue
		}
		s.TotalBytes += item.QuotaChargedBytes
		s.ItemCount++
		if item.Trashed {
			s.TrashedBytes += item.QuotaChargedBytes
			s.TrashedCount++
		}
	}
	return s
}

// SortRemoteMediaItems orders items deterministically for reporting: by
// quota-charged bytes, largest first.
func SortRemoteMediaItems(items []*RemoteMediaItem) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].QuotaChargedBytes > items[j].QuotaChargedBytes
	})
}
