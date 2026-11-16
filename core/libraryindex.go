package core

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LibraryIndexVersion is the current on-disk format of the library index.
// Version 2 added HasMotion, version 3 the favourite, archived and edited
// flags, and version 4 the albums. An index older than 3 is replaced by a
// full scan, because a delta sync would never fill new fields for unchanged
// items; a version 3 index is kept and gets its albums from one full walk.
const LibraryIndexVersion = 4

// LibraryIndex persists the remote library's media items together with the
// sync token of the last sync cycle. With the token the server only reports
// what changed since the last sync, so a steady-state refresh is a couple of
// requests instead of paging through the whole library. The file lives next
// to gotohp.config.
type LibraryIndex struct {
	Version   int                `json:"version"`
	SyncedAt  int64              `json:"syncedAt"`
	SyncToken string             `json:"syncToken,omitempty"`
	Items     []*RemoteMediaItem `json:"items"`
	Albums    []*RemoteAlbum     `json:"albums,omitempty"`
	// AlbumsKnown is true once a full walk with albums was saved; an index
	// from version 3 keeps its items but has no albums yet.
	AlbumsKnown bool `json:"albumsKnown,omitempty"`
}

// LibraryIndexPath returns the index file location next to the config file,
// or an empty string when the config path is not known yet.
func LibraryIndexPath() string {
	if ConfigPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(ConfigPath), "library-index.json")
}

// LoadLibraryIndex reads the index file. It returns (nil, nil) when no index
// exists or the file is unreadable: the index is an optimization, and a
// corrupt one is as good as none.
func LoadLibraryIndex() (*LibraryIndex, error) {
	path := LibraryIndexPath()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var index LibraryIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, nil
	}
	// Version 3 lacks only the albums: its items stay usable, and the next
	// sync walks the whole library once to fill the albums in.
	if index.Version == 3 {
		index.Version = LibraryIndexVersion
		index.AlbumsKnown = false
	}
	if index.Version != LibraryIndexVersion {
		return nil, fmt.Errorf("unsupported library index version %d", index.Version)
	}
	return &index, nil
}

// Save persists the index atomically.
func (idx *LibraryIndex) Save() error {
	if idx == nil {
		return nil
	}
	path := LibraryIndexPath()
	if path == "" {
		return fmt.Errorf("library index path unknown")
	}
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".library-index-*")
	if err != nil {
		return err
	}
	_, writeErr := temporary.Write(data)
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(temporary.Name())
		return fmt.Errorf("write library index: %w", firstError(writeErr, closeErr))
	}
	return os.Rename(temporary.Name(), path)
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// UploadedHashes builds a set of hex SHA-1 digests of every non-trashed
// item, the same identity the upload-time deduplication uses.
func (idx *LibraryIndex) UploadedHashes() map[string]struct{} {
	set := make(map[string]struct{}, len(idx.Items))
	for _, item := range idx.Items {
		if item.Trashed || len(item.Sha1) == 0 {
			continue
		}
		set[hex.EncodeToString(item.Sha1)] = struct{}{}
	}
	return set
}

// SyncLibraryIncremental syncs the library using a cached index. When the
// index carries a sync token, the server reports only the changes since the
// last sync and the cached items are kept; otherwise a full walk rebuilds
// everything. onPage receives running totals after every fetched page and
// may be nil. The returned index is complete and ready to save.
func (a *Api) SyncLibraryIncremental(cached *LibraryIndex, onPage func(LibrarySyncStats)) (*LibraryIndex, error) {
	if cached == nil || cached.SyncToken == "" || !cached.AlbumsKnown {
		return a.fullLibraryIndex(onPage)
	}
	index, err := a.deltaLibraryIndex(cached, onPage)
	if err != nil {
		// A stale or invalid sync token is reported as a request error; a
		// full walk always recovers.
		return a.fullLibraryIndex(onPage)
	}
	return index, nil
}

// fullLibraryIndex walks the whole library and builds a fresh index.
func (a *Api) fullLibraryIndex(onPage func(LibrarySyncStats)) (*LibraryIndex, error) {
	library, syncToken, _, err := a.syncLibraryAccumulated(onPage)
	if err != nil {
		return nil, err
	}
	return &LibraryIndex{
		Version:     LibraryIndexVersion,
		SyncedAt:    time.Now().Unix(),
		SyncToken:   syncToken,
		Albums:      library.albumList(),
		AlbumsKnown: true,
		Items:       library.items(),
	}, nil
}

// deltaLibraryIndex applies the server's changes since cached.SyncToken onto
// the cached items.
func (a *Api) deltaLibraryIndex(cached *LibraryIndex, onPage func(LibrarySyncStats)) (*LibraryIndex, error) {
	// Seed the accumulator with the cached items so the running totals stay
	// exact as delta pages replace and delete entries.
	library := newLibraryAccumulator()
	for _, item := range cached.Items {
		if _, seen := library.byKey[item.MediaKey]; !seen {
			library.order = append(library.order, item.MediaKey)
		}
		library.byKey[item.MediaKey] = item
		library.count(item, 1)
	}
	for _, album := range cached.Albums {
		library.addAlbum(album)
	}
	report := func() {
		stats := LibrarySyncStats{Items: len(library.byKey), TotalBytes: library.totalBytes,
			ChargedItems: library.chargedItems, ChargedBytes: library.chargedBytes,
			UniqueChargedItems: library.uniqueChargedItems, UniqueChargedBytes: library.uniqueChargedBytes}
		if onPage != nil {
			onPage(stats)
		}
	}

	state, err := a.GetLibraryState(cached.SyncToken)
	if err != nil {
		return nil, fmt.Errorf("failed to get library delta state: %w", err)
	}
	library.apply(state)
	report()
	syncToken := state.SyncToken
	if syncToken == "" {
		syncToken = cached.SyncToken
	}

	// A delta cycle continues with the delta page request; the init request
	// only pages a fresh (from scratch) cycle.
	resumeToken := state.ResumeToken
	for resumeToken != "" {
		page, err := a.GetLibraryPage(resumeToken, syncToken)
		if err != nil {
			return nil, fmt.Errorf("failed to get library delta page: %w", err)
		}
		library.apply(page)
		report()
		if page.ResumeToken == resumeToken {
			return nil, fmt.Errorf("library delta sync did not advance: resume token repeated")
		}
		resumeToken = page.ResumeToken
	}

	return &LibraryIndex{
		Version:     LibraryIndexVersion,
		SyncedAt:    time.Now().Unix(),
		SyncToken:   syncToken,
		Albums:      library.albumList(),
		AlbumsKnown: true,
		Items:       library.items(),
	}, nil
}

// AlbumList is what the album picker shows.
type AlbumList struct {
	Albums []RemoteAlbum
	// SyncedAt is when the library was last read (Unix seconds); Known is
	// false when no library scan with albums exists yet.
	SyncedAt int64
	Known    bool
}

// AlbumsOf lists the index's titled albums, most recently changed first.
func AlbumsOf(index *LibraryIndex) AlbumList {
	list := AlbumList{Albums: []RemoteAlbum{}}
	if index == nil {
		return list
	}
	list.Known, list.SyncedAt = index.AlbumsKnown, index.SyncedAt
	for _, album := range index.Albums {
		if album != nil && strings.TrimSpace(album.Title) != "" {
			list.Albums = append(list.Albums, *album)
		}
	}
	sort.SliceStable(list.Albums, func(i, j int) bool { return list.Albums[i].LastActivity > list.Albums[j].LastActivity })
	return list
}

// DuplicateAlbumGroup is a set of albums that share a title.
type DuplicateAlbumGroup struct {
	Title  string
	Albums []RemoteAlbum
	// Items is the total item count across the group.
	Items int64
}

// DuplicateAlbums groups albums whose titles match (ignoring case and
// surrounding spaces), largest groups first. Within a group the biggest
// album comes first.
func DuplicateAlbums(albums []RemoteAlbum) []DuplicateAlbumGroup {
	byTitle := map[string]*DuplicateAlbumGroup{}
	var order []string
	for _, album := range albums {
		title := strings.TrimSpace(album.Title)
		if title == "" {
			continue
		}
		key := strings.ToLower(title)
		group, ok := byTitle[key]
		if !ok {
			group = &DuplicateAlbumGroup{Title: title}
			byTitle[key] = group
			order = append(order, key)
		}
		group.Albums = append(group.Albums, album)
		group.Items += album.ItemCount
	}
	groups := []DuplicateAlbumGroup{}
	for _, key := range order {
		group := byTitle[key]
		if len(group.Albums) < 2 {
			continue
		}
		sort.SliceStable(group.Albums, func(i, j int) bool { return group.Albums[i].ItemCount > group.Albums[j].ItemCount })
		groups = append(groups, *group)
	}
	sort.SliceStable(groups, func(i, j int) bool { return len(groups[i].Albums) > len(groups[j].Albums) })
	return groups
}
