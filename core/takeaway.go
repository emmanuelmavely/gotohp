package core

import (
	"context"
	"fmt"
)

// TakeawayAlbumPrefix is the base name of the albums the takeaway creates.
const TakeawayAlbumPrefix = "gotohp takeaway"

// takeawayAddBatch is how many items one add-to-album request carries.
const takeawayAddBatch = AlbumBatchSize

// DefaultTakeawayAlbumSize is the album size used when none is given. It is
// smaller than Google's 20,000 limit so each album is quicker to share and
// save on a phone.
const DefaultTakeawayAlbumSize = 2000

// TakeawayAlbum is one album the takeaway created.
type TakeawayAlbum struct {
	Title    string `json:"Title"`
	AlbumKey string `json:"AlbumKey"`
	Count    int    `json:"Count"`
}

// TakeawayPlan is what a takeaway would gather, without doing anything.
type TakeawayPlan struct {
	// Items is how many of the account's own items would be gathered.
	Items int `json:"Items"`
	// Albums is how many albums that would make at the given album size.
	Albums int `json:"Albums"`
	// Skipped counts items left out (trashed, shared, partner, duplicates).
	Skipped int `json:"Skipped"`
}

// TakeawayProgress follows a running takeaway.
type TakeawayProgress struct {
	// Album is the 1-based album being filled, of AlbumTotal.
	Album      int    `json:"Album"`
	AlbumTotal int    `json:"AlbumTotal"`
	AlbumTitle string `json:"AlbumTitle"`
	// Done and Total count items across the whole run.
	Done  int `json:"Done"`
	Total int `json:"Total"`
}

// TakeawayResult is the outcome of a takeaway.
type TakeawayResult struct {
	Albums []TakeawayAlbum `json:"Albums"`
	Items  int             `json:"Items"`
	// Stopped is true when the run was cancelled before it finished; the
	// albums already made are still listed.
	Stopped bool `json:"Stopped"`
}

// TakeawayMediaKeys lists the account's own items worth gathering: not in the
// bin, not a shared or partner item, and each stored file once. Order is kept
// as the library reports it (newest first), so a stopped run is predictable.
func TakeawayMediaKeys(items []*RemoteMediaItem) (keys []string, skipped int) {
	seen := map[string]bool{}
	for _, item := range items {
		if item == nil || item.MediaKey == "" {
			continue
		}
		if item.Trashed || item.Origin == OriginShared || item.Origin == OriginPartner || (item.Origin != 0 && item.Origin != OriginSelf) {
			skipped++
			continue
		}
		// One stored file can appear under several library entries; add it once.
		if item.DedupKey != "" {
			if seen[item.DedupKey] {
				skipped++
				continue
			}
			seen[item.DedupKey] = true
		}
		keys = append(keys, item.MediaKey)
	}
	return keys, skipped
}

// NormalizeTakeawayAlbumSize clamps an album size to a sane range.
func NormalizeTakeawayAlbumSize(size int) int {
	if size <= 0 {
		return DefaultTakeawayAlbumSize
	}
	if size > AlbumLimit {
		return AlbumLimit
	}
	return size
}

// PlanTakeaway reports what a takeaway of these items would gather.
func PlanTakeaway(items []*RemoteMediaItem, albumSize int) TakeawayPlan {
	albumSize = NormalizeTakeawayAlbumSize(albumSize)
	keys, skipped := TakeawayMediaKeys(items)
	albums := (len(keys) + albumSize - 1) / albumSize
	return TakeawayPlan{Items: len(keys), Albums: albums, Skipped: skipped}
}

// GatherTakeaway puts the given media keys into shared albums named
// "gotohp takeaway 1, 2, …", albumSize items each, creating each album with
// its first items and appending the rest in API-sized batches. It groups
// only; nothing is uploaded, moved or deleted. Progress is reported through
// onProgress; ctx cancels it after the current request, and the albums made
// so far are returned with Stopped set.
func (a *Api) GatherTakeaway(ctx context.Context, mediaKeys []string, albumSize int, onProgress func(TakeawayProgress)) (TakeawayResult, error) {
	albumSize = NormalizeTakeawayAlbumSize(albumSize)
	result := TakeawayResult{Albums: []TakeawayAlbum{}}
	if len(mediaKeys) == 0 {
		return result, fmt.Errorf("nothing to gather")
	}
	total := len(mediaKeys)
	albumTotal := (total + albumSize - 1) / albumSize
	done := 0

	report := func(albumIndex int, title string) {
		if onProgress != nil {
			onProgress(TakeawayProgress{
				Album: albumIndex, AlbumTotal: albumTotal, AlbumTitle: title,
				Done: done, Total: total,
			})
		}
	}

	for start := 0; start < total; start += albumSize {
		if ctx.Err() != nil {
			result.Stopped = true
			break
		}
		albumIndex := start/albumSize + 1
		title := fmt.Sprintf("%s %d", TakeawayAlbumPrefix, albumIndex)
		chunk := mediaKeys[start:min(start+albumSize, total)]
		report(albumIndex, title)

		albumKey := ""
		for offset := 0; offset < len(chunk); offset += takeawayAddBatch {
			if ctx.Err() != nil {
				result.Stopped = true
				break
			}
			batch := chunk[offset:min(offset+takeawayAddBatch, len(chunk))]
			if albumKey == "" {
				key, err := a.CreateAlbum(title, batch)
				if err != nil {
					return result, fmt.Errorf("could not create album %q: %w", title, err)
				}
				albumKey = key
				result.Albums = append(result.Albums, TakeawayAlbum{Title: title, AlbumKey: key, Count: len(batch)})
			} else {
				if err := a.AddMediaToAlbum(albumKey, batch); err != nil {
					return result, fmt.Errorf("could not add to album %q: %w", title, err)
				}
				result.Albums[len(result.Albums)-1].Count += len(batch)
			}
			done += len(batch)
			result.Items += len(batch)
			report(albumIndex, title)
		}
		if result.Stopped {
			break
		}
	}
	return result, nil
}
