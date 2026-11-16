package core

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Quota conversion: files that count against the storage quota are replaced
// by the same file uploaded again without quota. Google recognises files by
// their contents, so the counted copy has to be deleted for good first;
// otherwise the new upload would just match it. Everything that lives only
// in Google Photos (albums, favourites, descriptions, edits, sharing) is lost
// with the deleted copy. Only files with a bit-identical copy on this computer
// are offered, and that copy is checked again right before anything is
// deleted.

// QuotaConversionItem is a counted file with a matching copy on this computer.
type QuotaConversionItem struct {
	MediaKey  string
	DedupKey  string
	FileName  string
	SHA1Hex   string
	SizeBytes int64
	// QuotaBytes is what the file counts against the storage quota.
	QuotaBytes int64
	LocalPath  string
	// Folder is the local copy's folder.
	Folder string
	// CapturedAt is when the photo was taken, in Unix milliseconds.
	CapturedAt int64
	// Things that are lost when the counted copy is deleted.
	Favorite bool
	Archived bool
	Edited   bool
	// StoredCopyOnly is true when Google Photos keeps no original (storage
	// saver quality): the stored copy is downloaded and re-uploaded as it
	// is, and cannot be checked against the original's fingerprint.
	StoredCopyOnly bool
}

// QuotaConversionExclusion is a counted file that is not offered, with why.
type QuotaConversionExclusion struct {
	FileName string
	MediaKey string
	Bytes    int64
	Reason   string
}

// QuotaNameMismatch is a counted file whose name exists locally with
// different content.
type QuotaNameMismatch struct {
	FileName  string
	LocalPath string
	// RemoteSize and LocalSize show at a glance that the files differ.
	RemoteSize int64
	LocalSize  int64
}

// LocalScanResult is what a local folder scan found.
type LocalScanResult struct {
	// Found maps a SHA-1 (hex) to the first local file with that content.
	Found map[string]string
	// Names maps a lowercased file name to the first local file with that
	// name, and Sizes that file's size.
	Names map[string]string
	Sizes map[string]int64
	// Seen counts media files looked at; SizeMatched the ones read.
	Seen        int
	SizeMatched int
}

// quotaNameExamples caps the same-name examples in a report.
const quotaNameExamples = 25

// QuotaReasonTotal counts files left out for one reason.
type QuotaReasonTotal struct {
	Reason string
	Files  int
	Bytes  int64
}

// conversionBlocker says why a counted file cannot be converted at all, or
// returns "".
func conversionBlocker(item *RemoteMediaItem, copies map[string]int) string {
	switch {
	case item.Origin == OriginShared || item.Origin == OriginPartner || !item.IsCanonical:
		return "belongs to a shared album or a partner"
	case item.HasMotion:
		return "Live Photo: uploading the still alone would lose its motion"
	case copies[item.DedupKey] > 1:
		return fmt.Sprintf("listed %d times in the library (for example in a shared album); deleting it would remove every entry", copies[item.DedupKey])
	case item.DedupKey == "":
		return "the library gives no key to delete it by"
	}
	return ""
}

// conversionItem describes a counted file for conversion; localPath may be
// empty for a file that still has to be downloaded.
func conversionItem(item *RemoteMediaItem, localPath string) QuotaConversionItem {
	folder := ""
	if localPath != "" {
		folder = filepath.Dir(localPath)
	}
	return QuotaConversionItem{
		MediaKey:   item.MediaKey,
		DedupKey:   item.DedupKey,
		FileName:   item.FileName,
		SHA1Hex:    hex.EncodeToString(item.Sha1),
		SizeBytes:  item.SizeBytes,
		QuotaBytes: item.QuotaChargedBytes,
		LocalPath:  localPath,
		Folder:     folder,
		CapturedAt: libraryTime(item.UTCTimestamp).UnixMilli(),
		Favorite:   item.Favorite,
		Archived:   item.Archived,
		Edited:     item.EditedInPhotos,
	}
}

// QuotaFolderTotal sums the convertible files of one local folder.
type QuotaFolderTotal struct {
	Folder string
	Files  int
	Bytes  int64
}

// QuotaConversionReport describes what converting would free.
type QuotaConversionReport struct {
	// CountedFiles and CountedBytes cover every file that counts against
	// the quota, each file counted once.
	CountedFiles int
	CountedBytes int64
	// Convertible files have a matching local copy and none of the
	// exclusion reasons.
	Convertible      []QuotaConversionItem
	ConvertibleBytes int64
	// NotOnComputer counts files with no matching local copy in the
	// searched folders.
	NotOnComputer      int
	NotOnComputerBytes int64
	// SameNameDifferent counts the files of NotOnComputer that do have a
	// local file with the same name, but different content (an edited,
	// converted or re-saved copy). Examples lists a few of them.
	SameNameDifferent      int
	SameNameDifferentBytes int64
	SameNameExamples       []QuotaNameMismatch
	// SizeMatched is how many local files had the size of a counted file
	// and were therefore read and compared.
	SizeMatched int
	// Downloadable files have no copy here but can be downloaded from Google
	// Photos as the exact original (checked by SHA-1), after which they can
	// be converted like any other file.
	Downloadable      []QuotaConversionItem
	DownloadableBytes int64
	// NotDownloadable groups the other files without a local copy by why
	// they cannot be downloaded and converted.
	NotDownloadable []QuotaReasonTotal
	// Excluded files have a local copy but are not safe to convert.
	Excluded []QuotaConversionExclusion
	Folders  []QuotaFolderTotal
	// ScannedFiles is how many local files were looked at.
	ScannedFiles int
}

// BuildQuotaConversionReport matches the library's counted files with the
// local copies a folder scan found.
func BuildQuotaConversionReport(items []*RemoteMediaItem, scan LocalScanResult) QuotaConversionReport {
	local := scan.Found
	report := QuotaConversionReport{
		Convertible:      []QuotaConversionItem{},
		Excluded:         []QuotaConversionExclusion{},
		Folders:          []QuotaFolderTotal{},
		SameNameExamples: []QuotaNameMismatch{},
		Downloadable:     []QuotaConversionItem{},
		NotDownloadable:  []QuotaReasonTotal{},
		ScannedFiles:     scan.Seen,
		SizeMatched:      scan.SizeMatched,
	}
	unique, copies := UniqueQuotaChargedMedia(QuotaChargedMedia(items))
	folders := map[string]*QuotaFolderTotal{}
	reasons := map[string]*QuotaReasonTotal{}
	addReason := func(reason string, bytes int64) {
		total := reasons[reason]
		if total == nil {
			total = &QuotaReasonTotal{Reason: reason}
			reasons[reason] = total
		}
		total.Files++
		total.Bytes += bytes
	}

	for _, item := range unique {
		if item.Trashed {
			// Already on its way out; emptying the bin frees it.
			continue
		}
		report.CountedFiles++
		report.CountedBytes += item.QuotaChargedBytes

		sum := hex.EncodeToString(item.Sha1)
		localPath, found := local[sum]
		if len(item.Sha1) == 0 || !found {
			report.NotOnComputer++
			report.NotOnComputerBytes += item.QuotaChargedBytes
			name := strings.ToLower(item.FileName)
			if path, sameName := scan.Names[name]; sameName && name != "" {
				report.SameNameDifferent++
				report.SameNameDifferentBytes += item.QuotaChargedBytes
				if len(report.SameNameExamples) < quotaNameExamples {
					report.SameNameExamples = append(report.SameNameExamples, QuotaNameMismatch{
						FileName: item.FileName, LocalPath: path, RemoteSize: item.SizeBytes, LocalSize: scan.Sizes[name],
					})
				}
			}
			reason := conversionBlocker(item, copies)
			switch {
			case reason != "":
			case len(item.Sha1) == 0:
				reason = "the library has no fingerprint to check a download against"
			case !item.OriginalQuality:
				reason = "stored in storage saver quality, so the original is not in Google Photos to download"
			}
			if reason != "" {
				addReason(reason, item.QuotaChargedBytes)
				continue
			}
			report.Downloadable = append(report.Downloadable, conversionItem(item, ""))
			report.DownloadableBytes += item.QuotaChargedBytes
			continue
		}
		if reason := conversionBlocker(item, copies); reason != "" {
			report.Excluded = append(report.Excluded, QuotaConversionExclusion{
				FileName: item.FileName, MediaKey: item.MediaKey, Bytes: item.QuotaChargedBytes, Reason: reason,
			})
			continue
		}

		folder := filepath.Dir(localPath)
		report.Convertible = append(report.Convertible, conversionItem(item, localPath))
		report.ConvertibleBytes += item.QuotaChargedBytes
		total := folders[folder]
		if total == nil {
			total = &QuotaFolderTotal{Folder: folder}
			folders[folder] = total
		}
		total.Files++
		total.Bytes += item.QuotaChargedBytes
	}

	for _, total := range reasons {
		report.NotDownloadable = append(report.NotDownloadable, *total)
	}
	sort.Slice(report.NotDownloadable, func(i, j int) bool { return report.NotDownloadable[i].Bytes > report.NotDownloadable[j].Bytes })
	sort.SliceStable(report.Downloadable, func(i, j int) bool {
		return report.Downloadable[i].QuotaBytes > report.Downloadable[j].QuotaBytes
	})
	for _, total := range folders {
		report.Folders = append(report.Folders, *total)
	}
	sort.Slice(report.Folders, func(i, j int) bool { return report.Folders[i].Bytes > report.Folders[j].Bytes })
	sort.SliceStable(report.Convertible, func(i, j int) bool {
		return report.Convertible[i].QuotaBytes > report.Convertible[j].QuotaBytes
	})
	return report
}

// QuotaScanProgress reports a local folder scan.
type QuotaScanProgress struct {
	// Seen is every media file looked at; Hashed the ones whose size matched
	// a counted file and were therefore read.
	Seen    int
	Hashed  int
	Matched int
	Folder  string
}

// ScanLocalCopies walks folders (recursively) for media files whose content
// equals a counted file. Only files whose size equals a counted file's size
// are read, so most of a large folder is skipped without hashing. It also
// notes every file name, to explain counted files that exist here only as a
// different version.
func ScanLocalCopies(ctx context.Context, folders []string, wantSizes map[int64]bool, wantDigests map[string]bool, cache *HashCache, onProgress func(QuotaScanProgress)) (LocalScanResult, error) {
	result := LocalScanResult{Found: map[string]string{}, Names: map[string]string{}, Sizes: map[string]int64{}}
	var progress QuotaScanProgress
	lastReport := time.Time{}
	report := func(force bool) {
		if onProgress != nil && (force || time.Since(lastReport) >= 250*time.Millisecond) {
			lastReport = time.Now()
			onProgress(progress)
		}
	}
	finish := func(err error) (LocalScanResult, error) {
		result.Seen = progress.Seen
		result.SizeMatched = progress.Hashed
		report(true)
		return result, err
	}

	for _, root := range folders {
		// Tell the window right away that the local search started.
		progress.Folder = root
		report(true)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if err != nil {
				// Unreadable folders are skipped, not fatal.
				if entry != nil && entry.IsDir() && path != root {
					return fs.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				progress.Folder = path
				report(false)
				return nil
			}
			if !isSupportedByGooglePhotos(path) {
				return nil
			}
			progress.Seen++
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			name := strings.ToLower(entry.Name())
			if _, seen := result.Names[name]; !seen {
				result.Names[name] = path
				result.Sizes[name] = info.Size()
			}
			if !wantSizes[info.Size()] {
				report(false)
				return nil
			}
			sum, err := calculateSHA1Cached(ctx, path, cache)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return nil
			}
			progress.Hashed++
			digest := hex.EncodeToString(sum)
			if wantDigests[digest] {
				if _, seen := result.Found[digest]; !seen {
					result.Found[digest] = path
					progress.Matched++
				}
			}
			report(false)
			return nil
		})
		if err != nil {
			return finish(err)
		}
	}
	return finish(nil)
}

// QuotaScanTargets lists the sizes and digests of the counted files, so a
// local scan knows what to look for.
func QuotaScanTargets(items []*RemoteMediaItem) (sizes map[int64]bool, digests map[string]bool) {
	sizes, digests = map[int64]bool{}, map[string]bool{}
	unique, _ := UniqueQuotaChargedMedia(QuotaChargedMedia(items))
	for _, item := range unique {
		if item.Trashed || len(item.Sha1) == 0 || item.SizeBytes <= 0 {
			continue
		}
		sizes[item.SizeBytes] = true
		digests[hex.EncodeToString(item.Sha1)] = true
	}
	return sizes, digests
}

// --- Conversion record ---------------------------------------------------------

// QuotaConversionEntry is one file whose counted copy was deleted (or is being
// deleted) so it can be uploaded again without quota.
type QuotaConversionEntry struct {
	MediaKey   string `json:"mediaKey"`
	DedupKey   string `json:"dedupKey"`
	FileName   string `json:"fileName"`
	SHA1Hex    string `json:"sha1"`
	LocalPath  string `json:"localPath"`
	QuotaBytes int64  `json:"quotaBytes"`
	// DeletedAt is when the counted copy was deleted, in Unix seconds.
	DeletedAt int64 `json:"deletedAt"`
}

type quotaConversionLedger struct {
	Version int                    `json:"version"`
	Entries []QuotaConversionEntry `json:"entries"`
}

const quotaConversionLedgerVersion = 1

var quotaLedgerMu sync.Mutex

// QuotaConversionLedgerPath returns the record's location next to the config
// file, or "" when the config path is not known yet.
func QuotaConversionLedgerPath() string {
	if ConfigPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(ConfigPath), "quota-conversion.json")
}

// LoadQuotaConversions returns the recorded conversions.
func LoadQuotaConversions() []QuotaConversionEntry {
	quotaLedgerMu.Lock()
	defer quotaLedgerMu.Unlock()
	return loadQuotaLedgerLocked().Entries
}

func loadQuotaLedgerLocked() quotaConversionLedger {
	empty := quotaConversionLedger{Version: quotaConversionLedgerVersion, Entries: []QuotaConversionEntry{}}
	path := QuotaConversionLedgerPath()
	if path == "" {
		return empty
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return empty
	}
	var ledger quotaConversionLedger
	if json.Unmarshal(data, &ledger) != nil || ledger.Version != quotaConversionLedgerVersion {
		return empty
	}
	if ledger.Entries == nil {
		ledger.Entries = []QuotaConversionEntry{}
	}
	return ledger
}

// updateQuotaConversions changes the record under its lock and saves it.
func updateQuotaConversions(change func(entries []QuotaConversionEntry) []QuotaConversionEntry) error {
	quotaLedgerMu.Lock()
	defer quotaLedgerMu.Unlock()
	path := QuotaConversionLedgerPath()
	if path == "" {
		return fmt.Errorf("config path unknown")
	}
	ledger := loadQuotaLedgerLocked()
	ledger.Entries = change(ledger.Entries)
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".quota-conversion-*")
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

// ForgetQuotaConversions drops finished entries (by SHA-1) from the record.
func ForgetQuotaConversions(sha1Hex []string) error {
	drop := map[string]bool{}
	for _, sum := range sha1Hex {
		drop[sum] = true
	}
	return updateQuotaConversions(func(entries []QuotaConversionEntry) []QuotaConversionEntry {
		kept := entries[:0]
		for _, entry := range entries {
			if !drop[entry.SHA1Hex] {
				kept = append(kept, entry)
			}
		}
		return kept
	})
}

// Conversion states, worked out from the library each time.
const (
	// QuotaStateConverted: the file is back in the library and no longer
	// counts against the quota.
	QuotaStateConverted = "converted"
	// QuotaStateReady: the counted copy is gone; the local copy can be
	// uploaded.
	QuotaStateReady = "ready"
	// QuotaStateInBin: the counted copy is still in the bin (permanent
	// delete did not go through yet).
	QuotaStateInBin = "in-bin"
	// QuotaStateCounted: a counted copy is back in the library, for example
	// because it was restored or uploaded with quota.
	QuotaStateCounted = "counted"
)

// QuotaConversionStatus is a recorded conversion with its current state.
type QuotaConversionStatus struct {
	QuotaConversionEntry
	State string
	// LocalMissing is true when the local copy is gone or changed, so it can
	// no longer be uploaded.
	LocalMissing bool
}

// QuotaConversionStates works out each recorded conversion's state from the
// library. localOK, which may be nil, reports whether the local copy is still
// there.
func QuotaConversionStates(items []*RemoteMediaItem, entries []QuotaConversionEntry, localOK func(QuotaConversionEntry) bool) []QuotaConversionStatus {
	type presence struct{ counted, free, trashed bool }
	bySum := map[string]*presence{}
	for _, item := range items {
		if len(item.Sha1) == 0 {
			continue
		}
		sum := hex.EncodeToString(item.Sha1)
		p := bySum[sum]
		if p == nil {
			p = &presence{}
			bySum[sum] = p
		}
		switch {
		case item.Trashed:
			p.trashed = true
		case item.QuotaChargedBytes > 0:
			p.counted = true
		default:
			p.free = true
		}
	}

	statuses := make([]QuotaConversionStatus, 0, len(entries))
	for _, entry := range entries {
		status := QuotaConversionStatus{QuotaConversionEntry: entry}
		p := bySum[entry.SHA1Hex]
		switch {
		case p != nil && p.counted:
			status.State = QuotaStateCounted
		case p != nil && p.free:
			status.State = QuotaStateConverted
		case p != nil && p.trashed:
			status.State = QuotaStateInBin
		default:
			status.State = QuotaStateReady
		}
		if status.State == QuotaStateReady && localOK != nil {
			status.LocalMissing = !localOK(entry)
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// LocalCopyMatches reports whether path still holds exactly the content with
// the given SHA-1. It always reads the file; no cached digest is trusted.
func LocalCopyMatches(ctx context.Context, path, sha1Hex string) bool {
	sum, err := CalculateSHA1(ctx, path)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(sha1Hex)
	return err == nil && bytes.Equal(sum, want)
}

// --- Deleting the counted copy -------------------------------------------------

// QuotaDeleteOutcome reports one file of RemoveCountedCopies.
type QuotaDeleteOutcome struct {
	MediaKey string
	FileName string
	// Status is "deleted", "skipped" or "failed".
	Status  string
	Message string
}

// RemoveCountedCopies deletes the counted copies of items for good, so their
// local copies can be uploaded again without quota. Before each file its local
// copy is read again and must still match byte for byte; the file is recorded
// before anything is deleted, so an interrupted run never loses track of it.
// A file that cannot be deleted permanently is restored from the bin.
func (a *Api) RemoveCountedCopies(ctx context.Context, items []QuotaConversionItem, onProgress func(done int, outcome QuotaDeleteOutcome)) []QuotaDeleteOutcome {
	outcomes := make([]QuotaDeleteOutcome, 0, len(items))
	for i, item := range items {
		outcome := QuotaDeleteOutcome{MediaKey: item.MediaKey, FileName: item.FileName}
		finish := func(status, format string, args ...any) {
			outcome.Status = status
			outcome.Message = fmt.Sprintf(format, args...)
			outcomes = append(outcomes, outcome)
			if onProgress != nil {
				onProgress(i+1, outcome)
			}
		}
		if ctx.Err() != nil {
			finish("skipped", "stopped before this file; nothing changed")
			continue
		}
		if item.DedupKey == "" || item.SHA1Hex == "" || item.LocalPath == "" {
			finish("skipped", "incomplete entry")
			continue
		}
		if !LocalCopyMatches(ctx, item.LocalPath, item.SHA1Hex) {
			finish("skipped", "the copy on this computer changed or is gone; nothing deleted")
			continue
		}
		entry := QuotaConversionEntry{
			MediaKey: item.MediaKey, DedupKey: item.DedupKey, FileName: item.FileName,
			SHA1Hex: item.SHA1Hex, LocalPath: item.LocalPath, QuotaBytes: item.QuotaBytes,
			DeletedAt: time.Now().Unix(),
		}
		if err := updateQuotaConversions(func(entries []QuotaConversionEntry) []QuotaConversionEntry {
			for _, existing := range entries {
				if existing.SHA1Hex == entry.SHA1Hex {
					return entries
				}
			}
			return append(entries, entry)
		}); err != nil {
			finish("failed", "could not save the conversion record, so nothing was deleted: %v", err)
			continue
		}
		if err := a.MoveRemoteMediaToTrash([]string{item.DedupKey}); err != nil {
			_ = ForgetQuotaConversions([]string{item.SHA1Hex})
			finish("failed", "move to bin failed: %v", err)
			continue
		}
		if err := a.DeleteRemoteMediaPermanently([]string{item.DedupKey}); err != nil {
			restoreErr := a.RestoreMediaFromTrash([]string{item.DedupKey})
			if restoreErr == nil {
				_ = ForgetQuotaConversions([]string{item.SHA1Hex})
				finish("failed", "permanent delete failed (%v); the photo was restored from the bin", err)
			} else {
				finish("failed", "permanent delete failed (%v) and restoring failed (%v); it is in your bin", err, restoreErr)
			}
			continue
		}
		finish("deleted", "counted copy deleted; ready to upload again without quota")
	}
	return outcomes
}

// QuotaFreeUploadOptions turns upload options into ones for re-uploading
// converted files: original quality, not counted against the quota, and never
// deleting or changing anything locally.
func QuotaFreeUploadOptions(opts UploadOptions) UploadOptions {
	opts.Api.UseQuota = false
	opts.Api.Saver = false
	opts.ForceUpload = false
	opts.DeleteFromHost = false
	opts.PairLivePhotos = false
	opts.UpdateExistingPhotosToLive = false
	opts.SetDateFromFilename = false
	opts.DisableUnsupportedFilesFilter = false
	opts.Recursive = false
	opts.AlbumName = ""
	opts.AlbumAutoMode = false
	return opts
}

// QuotaConvertProgress follows a conversion run: Phase is "deleting" while
// counted copies are removed and "uploading" while a batch uploads again.
type QuotaConvertProgress struct {
	Phase   string
	Done    int
	Total   int
	Batch   int
	Batches int
	// FileName and the byte counts follow the file being downloaded.
	FileName   string
	BytesDone  int64
	BytesTotal int64
	// Outcome is set for each file whose counted copy was handled.
	Outcome *QuotaDeleteOutcome
}

// --- Downloading counted originals --------------------------------------------

// QuotaDownloadOutcome reports one file of DownloadCountedOriginals.
type QuotaDownloadOutcome struct {
	MediaKey  string
	FileName  string
	LocalPath string
	// SHA1Hex is the downloaded file's fingerprint.
	SHA1Hex string
	// Status is "downloaded", "skipped" or "failed".
	Status  string
	Message string
}

// QuotaDownloadProgress follows a download run, one file at a time.
type QuotaDownloadProgress struct {
	Index      int
	Total      int
	FileName   string
	BytesDone  int64
	BytesTotal int64
	// DoneBytes is the size of the files finished so far.
	DoneBytes int64
	Outcome   *QuotaDownloadOutcome
}

// QuotaDownloadResult is what the GUI gets back from a download run: the
// outcomes and the report with the downloaded files now convertible.
type QuotaDownloadResult struct {
	Outcomes []QuotaDownloadOutcome
	Report   QuotaConversionReport
}

// windowsReserved are characters a file name may not contain on Windows.
var windowsReserved = strings.NewReplacer("<", "_", ">", "_", ":", "_", "\"", "_", "/", "_", "\\", "_", "|", "_", "?", "_", "*", "_")

// downloadDirectory is the per-year folder a downloaded original goes into.
func downloadDirectory(folder string, item QuotaConversionItem) string {
	year := "unknown date"
	if item.CapturedAt > 0 {
		year = time.UnixMilli(item.CapturedAt).Format("2006")
	}
	return filepath.Join(folder, year)
}

// downloadName is the library's file name, made safe for Windows.
func downloadName(item QuotaConversionItem) string {
	// The library's name is only a name: take what follows the last slash
	// (either kind), then replace characters Windows does not allow. Using
	// filepath.Base here would read "a:b.jpg" as drive "a:" on Windows.
	name := item.FileName
	if cut := strings.LastIndexAny(name, `/\`); cut >= 0 {
		name = name[cut+1:]
	}
	name = windowsReserved.Replace(name)
	if name == "" || name == "." {
		name = item.MediaKey
	}
	return name
}

// downloadTarget picks a free path for a downloaded original, numbering the
// name when another file already uses it.
func downloadTarget(folder string, item QuotaConversionItem) string {
	directory, name := downloadDirectory(folder, item), downloadName(item)
	target := filepath.Join(directory, name)
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for n := 2; ; n++ {
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return target
		}
		target = filepath.Join(directory, fmt.Sprintf("%s (%d)%s", stem, n, extension))
	}
}

// DownloadCountedOriginals saves the exact originals of items into folder
// (one subfolder per year). Every download is checked against the library's
// SHA-1 and kept only when it is identical; its modification time is set to
// the capture time. Nothing in Google Photos is changed. Files already
// downloaded identically (same target name and content) are reused.
func (a *Api) DownloadCountedOriginals(ctx context.Context, items []QuotaConversionItem, folder string, onProgress func(QuotaDownloadProgress)) []QuotaDownloadOutcome {
	api := a.withResponseTimeout(repairResponseTimeout)
	outcomes := make([]QuotaDownloadOutcome, 0, len(items))
	var doneBytes int64
	for i, item := range items {
		outcome := QuotaDownloadOutcome{MediaKey: item.MediaKey, FileName: item.FileName}
		send := func(progress QuotaDownloadProgress) {
			if onProgress != nil {
				progress.Index, progress.Total, progress.FileName, progress.DoneBytes = i+1, len(items), item.FileName, doneBytes
				onProgress(progress)
			}
		}
		finish := func(status, format string, args ...any) {
			outcome.Status = status
			outcome.Message = fmt.Sprintf(format, args...)
			outcomes = append(outcomes, outcome)
			if status == "downloaded" {
				doneBytes += item.SizeBytes
			}
			result := outcome
			send(QuotaDownloadProgress{Outcome: &result})
		}
		if ctx.Err() != nil {
			finish("skipped", "stopped before this file")
			continue
		}

		// A run that was stopped earlier may have saved it already. Files
		// are only ever saved complete (download, check, then rename).
		existing := filepath.Join(downloadDirectory(folder, item), downloadName(item))
		if !item.StoredCopyOnly && LocalCopyMatches(ctx, existing, item.SHA1Hex) {
			outcome.LocalPath, outcome.SHA1Hex = existing, item.SHA1Hex
			finish("downloaded", "already downloaded")
			continue
		}
		if item.StoredCopyOnly {
			if info, err := os.Stat(existing); err == nil && info.Size() > 0 {
				if sum, err := CalculateSHA1(ctx, existing); err == nil {
					outcome.LocalPath, outcome.SHA1Hex = existing, hex.EncodeToString(sum)
					finish("downloaded", "already downloaded")
					continue
				}
			}
		}

		target := downloadTarget(folder, item)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			finish("failed", "create folder: %v", err)
			continue
		}
		temporary, err := os.CreateTemp(filepath.Dir(target), ".gotohp-download-*")
		if err != nil {
			finish("failed", "create file: %v", err)
			continue
		}
		temporaryPath := temporary.Name()
		_ = temporary.Close()

		send(QuotaDownloadProgress{BytesTotal: item.SizeBytes})
		transferCtx, kick, stop := stallWatchdog(ctx, repairStallTimeout)
		err = api.DownloadOriginalWithProgress(transferCtx, item.MediaKey, temporaryPath, func(done, total int64) {
			kick()
			send(QuotaDownloadProgress{BytesDone: done, BytesTotal: total})
		})
		stop()
		if err != nil {
			_ = os.Remove(temporaryPath)
			if ctx.Err() != nil {
				finish("skipped", "stopped while downloading; nothing kept")
			} else {
				finish("failed", "download failed: %v", transferError(transferCtx, err))
			}
			continue
		}
		if item.StoredCopyOnly {
			// No original to compare with; it must at least be a complete,
			// non-empty file.
			sum, err := CalculateSHA1(ctx, temporaryPath)
			info, statErr := os.Stat(temporaryPath)
			if err != nil || statErr != nil || info.Size() == 0 {
				_ = os.Remove(temporaryPath)
				finish("failed", "the download is empty or unreadable; nothing kept")
				continue
			}
			outcome.SHA1Hex = hex.EncodeToString(sum)
		} else {
			if !LocalCopyMatches(ctx, temporaryPath, item.SHA1Hex) {
				_ = os.Remove(temporaryPath)
				finish("failed", "the download is not the exact original (it may be an edited or reduced copy); nothing kept")
				continue
			}
			outcome.SHA1Hex = item.SHA1Hex
		}
		if err := os.Rename(temporaryPath, target); err != nil {
			_ = os.Remove(temporaryPath)
			finish("failed", "save file: %v", err)
			continue
		}
		if item.CapturedAt > 0 {
			captured := time.UnixMilli(item.CapturedAt)
			_ = os.Chtimes(target, captured, captured)
		}
		outcome.LocalPath = target
		finish("downloaded", "saved and checked")
	}
	return outcomes
}

// MarkDownloaded moves the downloaded files from Downloadable to Convertible,
// with their new local paths.
func (r *QuotaConversionReport) MarkDownloaded(outcomes []QuotaDownloadOutcome) {
	paths := map[string]string{}
	sums := map[string]string{}
	for _, outcome := range outcomes {
		if outcome.Status == "downloaded" && outcome.LocalPath != "" {
			paths[outcome.MediaKey] = outcome.LocalPath
			sums[outcome.MediaKey] = outcome.SHA1Hex
		}
	}
	if len(paths) == 0 {
		return
	}
	folders := map[string]int{}
	for i, total := range r.Folders {
		folders[total.Folder] = i
	}
	remaining := r.Downloadable[:0]
	for _, item := range r.Downloadable {
		path, ok := paths[item.MediaKey]
		if !ok {
			remaining = append(remaining, item)
			continue
		}
		item.LocalPath = path
		item.Folder = filepath.Dir(path)
		if sum := sums[item.MediaKey]; sum != "" {
			item.SHA1Hex = sum
		}
		r.Convertible = append(r.Convertible, item)
		r.ConvertibleBytes += item.QuotaBytes
		r.DownloadableBytes -= item.QuotaBytes
		r.NotOnComputer--
		r.NotOnComputerBytes -= item.QuotaBytes
		if i, seen := folders[item.Folder]; seen {
			r.Folders[i].Files++
			r.Folders[i].Bytes += item.QuotaBytes
		} else {
			folders[item.Folder] = len(r.Folders)
			r.Folders = append(r.Folders, QuotaFolderTotal{Folder: item.Folder, Files: 1, Bytes: item.QuotaBytes})
		}
	}
	r.Downloadable = remaining
}

// --- Converting everything ------------------------------------------------------

// QuotaConvertAllPlan summarises what "convert all" would do.
type QuotaConvertAllPlan struct {
	// CountedFiles and CountedBytes cover every file counting now.
	CountedFiles int
	CountedBytes int64
	// Files and Bytes are what would be converted.
	Files int
	Bytes int64
	// DownloadBytes is roughly what has to be downloaded (and kept as backup).
	DownloadBytes int64
	// SaverFiles of Files have no original in Google Photos; their stored
	// copy is downloaded and uploaded again as it is.
	SaverFiles int
	// LivePhotos are left alone: re-uploading the still alone would lose the
	// motion.
	LivePhotos     int
	LivePhotoBytes int64
	// Waiting counts earlier conversions deleted but not uploaded yet; they
	// are uploaded first.
	Waiting int
	// DefaultFolder is where downloads are kept when no folder was chosen.
	DefaultFolder string
}

// DefaultQuotaBackupFolder is Pictures\gotohp backup in the user's home.
func DefaultQuotaBackupFolder() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Pictures", "gotohp backup")
}

// PlanQuotaConvertAll lists every counted file that "convert all" handles:
// all files counting against the quota except Live Photos, each file once.
func PlanQuotaConvertAll(items []*RemoteMediaItem) (QuotaConvertAllPlan, []QuotaConversionItem) {
	plan := QuotaConvertAllPlan{DefaultFolder: DefaultQuotaBackupFolder()}
	unique, _ := UniqueQuotaChargedMedia(QuotaChargedMedia(items))
	targets := []QuotaConversionItem{}
	for _, item := range unique {
		if item.Trashed {
			continue
		}
		plan.CountedFiles++
		plan.CountedBytes += item.QuotaChargedBytes
		if item.HasMotion {
			plan.LivePhotos++
			plan.LivePhotoBytes += item.QuotaChargedBytes
			continue
		}
		if item.DedupKey == "" || item.MediaKey == "" {
			continue
		}
		target := conversionItem(item, "")
		target.StoredCopyOnly = !item.OriginalQuality || len(item.Sha1) == 0
		if target.StoredCopyOnly {
			plan.SaverFiles++
			plan.DownloadBytes += item.QuotaChargedBytes
		} else {
			plan.DownloadBytes += item.SizeBytes
		}
		plan.Files++
		plan.Bytes += item.QuotaChargedBytes
		targets = append(targets, target)
	}
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].CapturedAt < targets[j].CapturedAt })
	return plan, targets
}

// QuotaConvertAllResult is what a "convert all" run did.
type QuotaConvertAllResult struct {
	Total     int
	Converted int
	// Problems lists files that were skipped or failed, with why.
	Problems []QuotaDeleteOutcome
}
