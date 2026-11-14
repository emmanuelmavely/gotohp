package core

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LibraryIndexVersion is the current on-disk format of the library index.
const LibraryIndexVersion = 1

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
	if cached == nil || cached.SyncToken == "" {
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
	items, _, syncToken, _, err := a.SyncLibraryWithProgress(onPage)
	if err != nil {
		return nil, err
	}
	return &LibraryIndex{
		Version:   LibraryIndexVersion,
		SyncedAt:  time.Now().Unix(),
		SyncToken: syncToken,
		Items:     items,
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
		Version:   LibraryIndexVersion,
		SyncedAt:  time.Now().Unix(),
		SyncToken: syncToken,
		Items:     library.items(),
	}, nil
}
