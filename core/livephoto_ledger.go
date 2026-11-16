package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// The repair ledger remembers every Live Photo the repair joined. After a
// repair, Google can keep listing the linked motion part as its own video
// item with the same name and content, so without the ledger the pair would
// show up as an orphan again on the next scan.

// RepairedLivePhoto is one pair the repair joined.
type RepairedLivePhoto struct {
	Stem          string `json:"stem"`
	PhotoFileName string `json:"photoFileName"`
	VideoFileName string `json:"videoFileName"`
	PhotoMediaKey string `json:"photoMediaKey"`
	PhotoDedupKey string `json:"photoDedupKey"`
	// VideoDedupKey is the orphan video's key; the repair trashed it.
	VideoDedupKey string `json:"videoDedupKey"`
	// RepairedAt is Unix seconds.
	RepairedAt int64 `json:"repairedAt"`
}

// LivePhotoRepairLedger is the on-disk list of repaired pairs.
type LivePhotoRepairLedger struct {
	Version  int                 `json:"version"`
	Repaired []RepairedLivePhoto `json:"repaired"`
}

const liveRepairLedgerVersion = 1

// LivePhotoRepairLedgerPath returns the ledger location next to the config
// file, or an empty string when the config path is not known yet.
func LivePhotoRepairLedgerPath() string {
	if ConfigPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(ConfigPath), "live-photo-repairs.json")
}

var ledgerMu sync.Mutex

// LoadLivePhotoRepairLedger reads the ledger. A missing or unreadable file
// yields an empty ledger.
func LoadLivePhotoRepairLedger() LivePhotoRepairLedger {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	return loadLedgerLocked()
}

func loadLedgerLocked() LivePhotoRepairLedger {
	empty := LivePhotoRepairLedger{Version: liveRepairLedgerVersion}
	path := LivePhotoRepairLedgerPath()
	if path == "" {
		return empty
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return empty
	}
	var ledger LivePhotoRepairLedger
	if json.Unmarshal(data, &ledger) != nil || ledger.Version != liveRepairLedgerVersion {
		return empty
	}
	return ledger
}

// RecordRepairedLivePhoto adds one repaired pair to the ledger right away, so
// it is remembered even if the app closes in the middle of a batch.
func RecordRepairedLivePhoto(entry RepairedLivePhoto) error {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	path := LivePhotoRepairLedgerPath()
	if path == "" {
		return nil
	}
	ledger := loadLedgerLocked()
	ledger.Repaired = append(ledger.Repaired, entry)
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".live-photo-repairs-*")
	if err != nil {
		return err
	}
	_, writeErr := temporary.Write(data)
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(temporary.Name())
		return firstError(writeErr, closeErr)
	}
	return os.Rename(temporary.Name(), path)
}

// repairedEntry builds the ledger entry for a candidate that was repaired.
func repairedEntry(candidate LivePhotoRepairCandidate, at time.Time) RepairedLivePhoto {
	return RepairedLivePhoto{
		Stem:          candidate.Stem,
		PhotoFileName: candidate.Photo.FileName,
		VideoFileName: candidate.Video.FileName,
		PhotoMediaKey: candidate.Photo.MediaKey,
		PhotoDedupKey: candidate.Photo.DedupKey,
		VideoDedupKey: candidate.Video.DedupKey,
		RepairedAt:    at.Unix(),
	}
}

// LivePhotoAlreadyRepaired is a pair that looks like an orphan but was
// already joined: Google still lists the motion part as its own video.
type LivePhotoAlreadyRepaired struct {
	Stem          string
	PhotoFileName string
	PhotoMediaKey string
	Reason        string
}

// separateAlreadyRepaired moves candidates the ledger or the bin show as
// already repaired out of the candidate list. A candidate counts as repaired
// when the ledger has its photo or video, or when a copy of its video with
// the same content is in the bin (repairs made before the ledger existed
// trashed the orphan that way).
func separateAlreadyRepaired(candidates []LivePhotoRepairCandidate, items []*RemoteMediaItem, ledger LivePhotoRepairLedger) (open []LivePhotoRepairCandidate, repaired []LivePhotoAlreadyRepaired) {
	byPhoto := map[string]RepairedLivePhoto{}
	byVideo := map[string]RepairedLivePhoto{}
	for _, entry := range ledger.Repaired {
		if entry.PhotoDedupKey != "" {
			byPhoto[entry.PhotoDedupKey] = entry
		}
		if entry.VideoDedupKey != "" {
			byVideo[entry.VideoDedupKey] = entry
		}
	}
	trashedVideos := map[string]bool{}
	for _, item := range items {
		if item.Trashed && item.Type == MediaTypeVideo && item.DedupKey != "" {
			trashedVideos[item.DedupKey] = true
		}
	}

	for _, candidate := range candidates {
		reason := ""
		if candidate.Photo.HasMotion {
			// Google says the photo already has its motion part, so the video
			// item is an extra copy, not an orphan.
			repaired = append(repaired, LivePhotoAlreadyRepaired{
				Stem:          candidate.Stem,
				PhotoFileName: candidate.Photo.FileName,
				PhotoMediaKey: candidate.Photo.MediaKey,
				Reason:        "already a Live Photo in Google Photos; the separate video is an extra copy",
			})
			continue
		}
		entry, inLedger := byVideo[candidate.Video.DedupKey]
		if !inLedger {
			entry, inLedger = byPhoto[candidate.Photo.DedupKey]
		}
		switch {
		case inLedger:
			reason = fmt.Sprintf("repaired %s; Google still lists the motion part as its own video",
				time.Unix(entry.RepairedAt, 0).Format("2 Jan 2006"))
		case candidate.Video.DedupKey != "" && trashedVideos[candidate.Video.DedupKey]:
			reason = "a copy of this video is already in the bin, so it was most likely repaired before; Google still lists the motion part as its own video"
		}
		if reason == "" {
			open = append(open, candidate)
			continue
		}
		repaired = append(repaired, LivePhotoAlreadyRepaired{
			Stem:          candidate.Stem,
			PhotoFileName: candidate.Photo.FileName,
			PhotoMediaKey: candidate.Photo.MediaKey,
			Reason:        reason,
		})
	}
	return open, repaired
}

// binRetention is how long Google keeps items in the bin before deleting
// them for good.
const binRetention = 60 * 24 * time.Hour

// BinItem is one library item in the bin.
type BinItem struct {
	MediaKey string
	// DedupKey restores or deletes the item.
	DedupKey  string
	FileName  string
	IsVideo   bool
	SizeBytes int64
	// TrashedAt and DeletesAt are Unix milliseconds; 0 when unknown.
	TrashedAt int64
	DeletesAt int64
	// RepairedPhoto names the still this video was linked to when the Live
	// Photo repair put it in the bin; empty otherwise.
	RepairedPhoto         string
	RepairedPhotoMediaKey string
	// RepairGuessed is true when no record of the repair exists (it ran
	// before the ledger did) but the video looks like a repaired orphan: a
	// .mov whose content is still in the library next to a photo of the
	// same name.
	RepairGuessed bool
	// CopyInLibrary is true when another item with the same content is still
	// in the library (outside the bin).
	CopyInLibrary bool
	// Shared is true for items from shared albums or a partner's library.
	Shared bool
}

func isCameraOriginal(item *RemoteMediaItem) bool {
	_, ext := livePhotoStem(item.FileName)
	return cameraOriginalExtensions[ext]
}

// BinReport lists the bin, newest first.
type BinReport struct {
	Items      []BinItem
	TotalBytes int64
	// FromRepair counts the items the Live Photo repair put in the bin.
	FromRepair int
}

// ListBin builds the bin report from library items and the repair ledger.
func ListBin(items []*RemoteMediaItem, ledger LivePhotoRepairLedger) BinReport {
	byVideo := map[string]RepairedLivePhoto{}
	for _, entry := range ledger.Repaired {
		if entry.VideoDedupKey != "" {
			byVideo[entry.VideoDedupKey] = entry
		}
	}
	inLibrary := map[string]bool{}
	photosByStem := map[string]*RemoteMediaItem{}
	for _, item := range items {
		if item.Trashed {
			continue
		}
		if item.DedupKey != "" {
			inLibrary[item.DedupKey] = true
		}
		if stem, ext := livePhotoStem(item.FileName); stem != "" && item.Type == MediaTypePhoto && livePhotoStillExtensions[ext] {
			// Prefer the camera original when a JPEG shares the name.
			if current, ok := photosByStem[stem]; !ok || !isCameraOriginal(current) && isCameraOriginal(item) {
				photosByStem[stem] = item
			}
		}
	}

	report := BinReport{Items: []BinItem{}}
	for _, item := range items {
		if !item.Trashed {
			continue
		}
		bin := BinItem{
			MediaKey:      item.MediaKey,
			DedupKey:      item.DedupKey,
			FileName:      item.FileName,
			IsVideo:       item.Type == MediaTypeVideo,
			SizeBytes:     item.SizeBytes,
			CopyInLibrary: item.DedupKey != "" && inLibrary[item.DedupKey],
			Shared:        item.Origin == OriginShared || item.Origin == OriginPartner,
		}
		if item.TrashTimestamp > 0 {
			trashed := libraryTime(item.TrashTimestamp)
			bin.TrashedAt = trashed.UnixMilli()
			bin.DeletesAt = trashed.Add(binRetention).UnixMilli()
		}
		if entry, ok := byVideo[item.DedupKey]; ok && item.DedupKey != "" && bin.IsVideo {
			bin.RepairedPhoto = entry.PhotoFileName
			bin.RepairedPhotoMediaKey = entry.PhotoMediaKey
			report.FromRepair++
		} else if stem, ext := livePhotoStem(item.FileName); bin.IsVideo && ext == ".mov" && bin.CopyInLibrary {
			if photo := photosByStem[stem]; photo != nil {
				bin.RepairedPhoto = photo.FileName
				bin.RepairedPhotoMediaKey = photo.MediaKey
				bin.RepairGuessed = true
				report.FromRepair++
			}
		}
		report.TotalBytes += item.SizeBytes
		report.Items = append(report.Items, bin)
	}
	sort.SliceStable(report.Items, func(i, j int) bool {
		return report.Items[i].TrashedAt > report.Items[j].TrashedAt
	})
	return report
}

// BinSelection splits the chosen bin items (by media key; all of them when
// keys is empty) into the dedup keys that may be deleted for good and the
// items left alone. Items from shared albums are not this account's to
// delete, and an item whose content is also in the library is skipped: both
// share one dedup key, so deleting it could take the library copy too.
func BinSelection(report BinReport, keys []string) (dedupKeys []string, skipped []BinItem) {
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	for _, item := range report.Items {
		if len(wanted) > 0 && !wanted[item.MediaKey] {
			continue
		}
		if item.DedupKey == "" || item.Shared || item.CopyInLibrary {
			skipped = append(skipped, item)
			continue
		}
		dedupKeys = append(dedupKeys, item.DedupKey)
	}
	return dedupKeys, skipped
}
