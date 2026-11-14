package core

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Live Photo repair: the library sync exposes no content identifiers, so a
// remote still and its orphan .mov (uploaded unpaired at some point) can only
// be matched by filename stem. A properly linked Live Photo appears as a
// single library item without a separate .mov entry, so a stem match between
// a photo item and a video item means the video is not linked to anything.
//
// Repair downloads the stored .mov, verifies its digest against the library,
// re-uploads it, links it to the still (the same ReconcileLivePhoto call the
// update-existing-photos flow uses), and finally trashes the orphan entry.

// LivePhotoRepairCandidate is one remote photo + orphan .mov pair.
type LivePhotoRepairCandidate struct {
	// Stem is the shared lowercased filename stem.
	Stem  string
	Photo *RemoteMediaItem
	Video *RemoteMediaItem
}

// livePhotoStillExtensions are the still-image formats Google Photos pairs
// with .mov videos.
var livePhotoStillExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".heic": true,
	".heif": true,
	".png":  true,
}

// cameraOriginalExtensions are the formats an iPhone saves Live Photo stills
// in by default. A JPEG with the same name is far more often a Lightroom (or
// other) export or a copy shared from another device than the original.
var cameraOriginalExtensions = map[string]bool{
	".heic": true,
	".heif": true,
}

// A Live Photo's video starts about 1.5 s before the still, and video times
// are rounded to whole seconds. The still must fall inside this window after
// the video's time to count as the same capture.
const (
	livePhotoEarliestStillOffset = -1 * time.Second
	livePhotoLatestStillOffset   = 4 * time.Second
)

// livePhotoStem splits a file name into its lowercased stem and extension.
func livePhotoStem(fileName string) (stem, ext string) {
	ext = strings.ToLower(filepath.Ext(fileName))
	return strings.ToLower(strings.TrimSuffix(fileName, filepath.Ext(fileName))), ext
}

// LivePhotoExclusion is a name that has a photo and a video but is not offered
// for repair, with the reason.
type LivePhotoExclusion struct {
	Stem   string
	Reason string
}

// FindOrphanLivePhotoPairs matches remote photo items and .mov video items by
// filename stem. Stems with more than one camera original or video are
// reported as ambiguous and produce no candidate.
func FindOrphanLivePhotoPairs(items []*RemoteMediaItem) (candidates []LivePhotoRepairCandidate, ambiguous []string) {
	candidates, ambiguous, _ = FindLivePhotoRepairCandidates(items)
	return candidates, ambiguous
}

// FindLivePhotoRepairCandidates matches remote photos with orphan .mov videos.
// Only the account's own, canonical, non-trashed items are considered, and a
// pair is offered only when it is safe to link:
//
//   - the photo is a camera original (HEIC/HEIF). When a name also has JPEG or
//     PNG photos, the HEIC is preferred; a name with only a JPEG or PNG is
//     excluded, since that is usually an edit or a shared copy;
//   - the photo was taken at the same moment as the video (see the offsets
//     above), so files that merely share a name are never joined.
//
// Names with more than one camera original or more than one video are
// ambiguous. Excluded names come with the reason.
func FindLivePhotoRepairCandidates(items []*RemoteMediaItem) (candidates []LivePhotoRepairCandidate, ambiguous []string, excluded []LivePhotoExclusion) {
	type group struct {
		photos []*RemoteMediaItem
		videos []*RemoteMediaItem
		seen   map[string]bool // dedup keys, so one file listed twice counts once
	}
	groups := make(map[string]*group)
	add := func(stem string, item *RemoteMediaItem, video bool) {
		g := groups[stem]
		if g == nil {
			g = &group{seen: map[string]bool{}}
			groups[stem] = g
		}
		if item.DedupKey != "" {
			if g.seen[item.DedupKey] {
				return
			}
			g.seen[item.DedupKey] = true
		}
		if video {
			g.videos = append(g.videos, item)
		} else {
			g.photos = append(g.photos, item)
		}
	}
	for _, item := range items {
		if item.Trashed || item.FileName == "" || !item.IsCanonical {
			continue
		}
		// Shared and partner items belong to someone else's library.
		if item.Origin == OriginShared || item.Origin == OriginPartner {
			continue
		}
		stem, ext := livePhotoStem(item.FileName)
		if stem == "" {
			continue
		}
		switch {
		case item.Type == MediaTypePhoto && livePhotoStillExtensions[ext]:
			add(stem, item, false)
		case item.Type == MediaTypeVideo && ext == ".mov":
			add(stem, item, true)
		}
	}

	for _, stem := range sortedMapKeys(groups) {
		g := groups[stem]
		if len(g.photos) == 0 || len(g.videos) == 0 {
			continue
		}
		if len(g.videos) > 1 {
			ambiguous = append(ambiguous, stem)
			continue
		}
		var originals []*RemoteMediaItem
		for _, photo := range g.photos {
			if _, ext := livePhotoStem(photo.FileName); cameraOriginalExtensions[ext] {
				originals = append(originals, photo)
			}
		}
		switch {
		case len(originals) > 1:
			ambiguous = append(ambiguous, stem)
			continue
		case len(originals) == 0:
			excluded = append(excluded, LivePhotoExclusion{
				Stem:   stem,
				Reason: "only a JPEG or PNG photo has this name; it may be an edit or a shared copy, not the camera original",
			})
			continue
		}
		photo, video := originals[0], g.videos[0]
		if reason := sameCaptureProblem(photo, video); reason != "" {
			excluded = append(excluded, LivePhotoExclusion{Stem: stem, Reason: reason})
			continue
		}
		candidates = append(candidates, LivePhotoRepairCandidate{Stem: stem, Photo: photo, Video: video})
	}
	return candidates, ambiguous, excluded
}

// sameCaptureProblem explains why a photo and a video are not one Live Photo
// capture, or returns "" when their times fit.
func sameCaptureProblem(photo, video *RemoteMediaItem) string {
	if photo.UTCTimestamp <= 0 || video.UTCTimestamp <= 0 {
		return "the library has no capture time for the photo or the video"
	}
	photoTime, videoTime := libraryTime(photo.UTCTimestamp), libraryTime(video.UTCTimestamp)
	offset := photoTime.Sub(videoTime)
	if offset < livePhotoEarliestStillOffset || offset > livePhotoLatestStillOffset {
		return fmt.Sprintf("the photo was taken %s and the video %s; not the same moment",
			photoTime.UTC().Format("2006-01-02 15:04:05"), videoTime.UTC().Format("2006-01-02 15:04:05"))
	}
	return ""
}

// sortedMapKeys returns the keys in deterministic order for reporting.
func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// LivePhotoRepairReport is what detection produces, ready for display.
type LivePhotoRepairReport struct {
	Candidates []LivePhotoRepairCandidate `json:"Candidates"`
	Ambiguous  []string                   `json:"Ambiguous"`
	// Excluded names have a photo and a video that are not safe to join.
	Excluded []LivePhotoExclusion `json:"Excluded"`
}

// AccountStorage summarizes the account's remote library. It is what the
// GUI's storage chip and statistics view show after a (delta) sync.
type AccountStorage struct {
	// Summary covers the files that count against the storage quota,
	// deduplicated by file content.
	Summary QuotaSummary `json:"Summary"`
	// LibraryBytes is the raw size of all library items before dedup.
	LibraryBytes int64 `json:"LibraryBytes"`
	// Items is the number of library entries (a file can appear more than once).
	Items int `json:"Items"`
	// SyncToken reuses the delta token of the last sync; empty until the
	// first full sync completed.
	SyncToken string `json:"SyncToken"`
	// SyncedAt is the Unix time of the index refresh.
	SyncedAt int64 `json:"SyncedAt"`
}

// RefreshAccountStorage syncs the library (incrementally when the local
// index has a sync token), saves the refreshed index, and summarizes the
// account's storage use.
func (a *Api) RefreshAccountStorage() (AccountStorage, error) {
	return a.RefreshAccountStorageWithProgress(nil)
}

// RefreshAccountStorageWithProgress is RefreshAccountStorage that also calls
// onPage with the running totals after every fetched page. onPage may be nil.
func (a *Api) RefreshAccountStorageWithProgress(onPage func(LibrarySyncStats)) (AccountStorage, error) {
	cached, _ := LoadLibraryIndex()
	index, err := a.SyncLibraryIncremental(cached, onPage)
	if err != nil {
		return AccountStorage{}, err
	}
	if err := index.Save(); err != nil {
		return AccountStorage{}, err
	}

	var libraryBytes int64
	for _, item := range index.Items {
		libraryBytes += item.SizeBytes
	}
	unique, _ := UniqueQuotaChargedMedia(QuotaChargedMedia(index.Items))
	return AccountStorage{
		Summary:      SummarizeQuota(unique),
		LibraryBytes: libraryBytes,
		Items:        len(index.Items),
		SyncToken:    index.SyncToken,
		SyncedAt:     index.SyncedAt,
	}, nil
}

// FindLivePhotoRepairReport matches the library's orphan pairs.
func FindLivePhotoRepairReport(items []*RemoteMediaItem) LivePhotoRepairReport {
	candidates, ambiguous, excluded := FindLivePhotoRepairCandidates(items)
	return LivePhotoRepairReport{Candidates: candidates, Ambiguous: ambiguous, Excluded: excluded}
}

// LivePhotoRepairOutcome reports what one repair attempt did.
type LivePhotoRepairOutcome struct {
	Stem string
	// Status is "repaired", "skipped", or "failed".
	Status  string
	Message string
	// PhotoMediaKey identifies the still, so the result can link to it in
	// Google Photos.
	PhotoMediaKey string
}

// Repair stages reported through LivePhotoRepairProgress.Stage, in order.
const (
	RepairStageDownloading = "downloading"
	RepairStageVerifying   = "verifying"
	RepairStageUploading   = "uploading"
	RepairStageLinking     = "linking"
	RepairStageTrashing    = "trashing"
	RepairStageDone        = "done"
)

// LivePhotoRepairProgress reports where a batch of repairs stands. One is sent
// at every stage change, throttled byte updates while the video moves, and a
// final one per pair with Stage "done" and its Outcome.
type LivePhotoRepairProgress struct {
	// Index is the 1-based position of the current pair in the batch.
	Index int
	Total int
	Stem  string
	// FileName is the orphan video's file name.
	FileName string
	Stage    string
	// BytesDone and BytesTotal track the download or upload in progress.
	BytesDone  int64
	BytesTotal int64
	// Outcome is set when Stage is "done".
	Outcome *LivePhotoRepairOutcome
}

const (
	// repairResponseTimeout bounds how long the server may take to start
	// answering any repair request.
	repairResponseTimeout = 2 * time.Minute
	// repairStallTimeout bounds how long a download or upload may go without
	// moving a byte before it is abandoned.
	repairStallTimeout = 2 * time.Minute
	// repairTempPattern names the downloaded videos in the temp folder.
	repairTempPattern = "gotohp-repair-*.mov"
	// staleRepairTempAge is when a leftover download from an interrupted
	// repair is removed. It is long enough that a repair still running in
	// another gotohp process is never touched.
	staleRepairTempAge = 6 * time.Hour
)

// errTransferStalled marks a download or upload abandoned by the stall watchdog.
var errTransferStalled = errors.New("no data moved for 2 minutes")

// withResponseTimeout returns a copy of the client whose requests fail when
// the server does not start answering within d. Transfers themselves are not
// limited, so large videos still upload; stalls are caught by the watchdog.
func (a *Api) withResponseTimeout(d time.Duration) *Api {
	clone := *a
	if a.client == nil {
		return &clone
	}
	client := *a.client
	if transport, ok := a.client.Transport.(*http.Transport); ok {
		tuned := transport.Clone()
		tuned.ResponseHeaderTimeout = d
		client.Transport = tuned
	}
	clone.client = &client
	return &clone
}

// stallWatchdog cancels a context when kick has not been called for d.
func stallWatchdog(parent context.Context, d time.Duration) (ctx context.Context, kick func(), stop func()) {
	ctx, cancel := context.WithCancelCause(parent)
	timer := time.AfterFunc(d, func() { cancel(errTransferStalled) })
	kick = func() { timer.Reset(d) }
	stop = func() {
		timer.Stop()
		cancel(nil)
	}
	return ctx, kick, stop
}

// transferError explains why a download or upload under a watchdog ended.
func transferError(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); errors.Is(cause, errTransferStalled) {
		return errTransferStalled
	}
	return err
}

// CleanupStaleRepairDownloads removes videos left in the temp folder by
// repairs that were interrupted (app closed, crash, shutdown).
func CleanupStaleRepairDownloads() {
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), repairTempPattern))
	if err != nil {
		return
	}
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if time.Since(info.ModTime()) > staleRepairTempAge {
			_ = os.Remove(path)
		}
	}
}

// RepairLivePhotoPair links one orphan pair: it downloads the stored video,
// verifies its digest, re-uploads it, reconciles it with the still, and
// trashes the orphan library entry. Every failure inside the attempt is
// reported through the outcome rather than the error value.
func (a *Api) RepairLivePhotoPair(ctx context.Context, candidate LivePhotoRepairCandidate) (LivePhotoRepairOutcome, error) {
	result := a.withResponseTimeout(repairResponseTimeout).repairOne(ctx, candidate, nil)
	return result.outcome, nil
}

// RepairLivePhotoPairs repairs the candidates one at a time, so only one video
// is ever held in the temp folder. onProgress, which may be nil, follows each
// pair through its stages. The batch stops early when ctx is cancelled or the
// connection is lost; pairs not attempted are reported as skipped, and
// scanning again picks them up. Re-uploaded bytes and completed repairs are
// added to the statistics.
func (a *Api) RepairLivePhotoPairs(ctx context.Context, candidates []LivePhotoRepairCandidate, onProgress func(LivePhotoRepairProgress)) []LivePhotoRepairOutcome {
	CleanupStaleRepairDownloads()
	api := a.withResponseTimeout(repairResponseTimeout)

	outcomes := make([]LivePhotoRepairOutcome, 0, len(candidates))
	var uploadedBytes, uploadSeconds, repaired int64
	stopReason := ""

	for i, candidate := range candidates {
		if stopReason == "" && ctx.Err() != nil {
			stopReason = "not attempted: the repair was cancelled"
		}
		if stopReason != "" {
			outcomes = append(outcomes, LivePhotoRepairOutcome{
				Stem: candidate.Stem, Status: "skipped", Message: stopReason, PhotoMediaKey: photoMediaKey(candidate),
			})
			continue
		}

		fileName := ""
		if candidate.Video != nil {
			fileName = candidate.Video.FileName
		}
		report := func(stage string, done, total int64) {
			if onProgress != nil {
				onProgress(LivePhotoRepairProgress{
					Index: i + 1, Total: len(candidates), Stem: candidate.Stem, FileName: fileName,
					Stage: stage, BytesDone: done, BytesTotal: total,
				})
			}
		}

		result := api.repairOne(ctx, candidate, report)
		outcomes = append(outcomes, result.outcome)
		uploadedBytes += result.uploadedBytes
		uploadSeconds += int64(result.uploadTime.Round(time.Second) / time.Second)
		if result.outcome.Status == "repaired" {
			repaired++
		}
		if onProgress != nil {
			outcome := result.outcome
			onProgress(LivePhotoRepairProgress{
				Index: i + 1, Total: len(candidates), Stem: candidate.Stem, FileName: fileName,
				Stage: RepairStageDone, Outcome: &outcome,
			})
		}

		// A failure on a dead connection would repeat for every pair: stop
		// instead, and let a later scan continue where this one ended.
		if result.outcome.Status == "failed" && ctx.Err() == nil &&
			(isNetworkError(result.cause) || errors.Is(result.cause, errTransferStalled)) && !probeConnectivity() {
			stopReason = "not attempted: the connection was lost; scan again to continue"
		}
	}

	RecordLivePhotoRepairs(uploadedBytes, repaired, uploadSeconds)
	return outcomes
}

// libraryTime converts a library item's timestamp to a time. The library sync
// reports milliseconds since the Unix epoch (photos keep the milliseconds,
// videos are rounded to whole seconds). A value too small to be milliseconds
// of a real photo is read as seconds, so it can never become a far-future date.
func libraryTime(timestamp int64) time.Time {
	const smallestMillis = 100_000_000_000 // March 1973 in milliseconds, year 5138 in seconds
	if timestamp > 0 && timestamp < smallestMillis {
		return time.Unix(timestamp, 0)
	}
	return time.UnixMilli(timestamp)
}

func photoMediaKey(candidate LivePhotoRepairCandidate) string {
	if candidate.Photo == nil {
		return ""
	}
	return candidate.Photo.MediaKey
}

// repairResult is one pair's outcome plus what it transferred.
type repairResult struct {
	outcome       LivePhotoRepairOutcome
	uploadedBytes int64
	uploadTime    time.Duration
	// cause is the error behind a failed outcome, for deciding whether the
	// connection was lost.
	cause error
}

// repairOne performs one repair. report, which may be nil, receives stage
// changes and byte progress.
func (a *Api) repairOne(ctx context.Context, candidate LivePhotoRepairCandidate, report func(stage string, done, total int64)) repairResult {
	result := repairResult{outcome: LivePhotoRepairOutcome{Stem: candidate.Stem, PhotoMediaKey: photoMediaKey(candidate)}}
	finish := func(status string, cause error, format string, args ...any) repairResult {
		result.outcome.Status = status
		result.outcome.Message = fmt.Sprintf(format, args...)
		result.cause = cause
		return result
	}
	if report == nil {
		report = func(string, int64, int64) {}
	}
	// A cancel during the download, check or upload leaves the library as it
	// was: nothing is linked or trashed before those steps finish.
	cancelled := func() (repairResult, bool) {
		if ctx.Err() == nil {
			return result, false
		}
		return finish("skipped", nil, "cancelled before %s was repaired; nothing changed in the library", candidate.Video.FileName), true
	}
	if candidate.Photo == nil || candidate.Video == nil {
		return finish("skipped", nil, "incomplete candidate")
	}

	if len(candidate.Photo.Sha1) != sha1.Size {
		return finish("skipped", nil, "the library has no usable digest for %s", candidate.Photo.FileName)
	}
	if len(candidate.Video.Sha1) != sha1.Size {
		return finish("skipped", nil, "the library has no usable digest for %s (storage saver quality?); cannot verify the repair", candidate.Video.FileName)
	}
	// Linking sends the still's capture time; without one the photo's date
	// could be overwritten with a wrong one.
	if candidate.Photo.UTCTimestamp <= 0 {
		return finish("skipped", nil, "the library has no capture time for %s", candidate.Photo.FileName)
	}

	temporary, err := os.CreateTemp("", repairTempPattern)
	if err != nil {
		return finish("failed", err, "create temp file: %v", err)
	}
	tempPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(tempPath)
		return finish("failed", err, "create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tempPath) }()

	// Download the stored video, abandoning it when no data arrives for a while.
	report(RepairStageDownloading, 0, candidate.Video.SizeBytes)
	downloadCtx, kickDownload, stopDownload := stallWatchdog(ctx, repairStallTimeout)
	err = a.DownloadOriginalWithProgress(downloadCtx, candidate.Video.MediaKey, tempPath, func(done, total int64) {
		kickDownload()
		report(RepairStageDownloading, done, total)
	})
	err = transferError(downloadCtx, err)
	stopDownload()
	if err != nil {
		if stopped, ok := cancelled(); ok {
			return stopped
		}
		return finish("failed", err, "download %s: %v", candidate.Video.FileName, err)
	}

	report(RepairStageVerifying, 0, 0)
	videoSHA1, err := CalculateSHA1(ctx, tempPath)
	if err != nil {
		if stopped, ok := cancelled(); ok {
			return stopped
		}
		return finish("failed", err, "hash downloaded %s: %v", candidate.Video.FileName, err)
	}
	if !bytes.Equal(videoSHA1, candidate.Video.Sha1) {
		return finish("skipped", nil, "downloaded %s does not match its library digest (edited or recompressed); refusing to repair",
			candidate.Video.FileName)
	}

	size := candidate.Video.SizeBytes
	if info, statErr := os.Stat(tempPath); statErr == nil {
		size = info.Size()
	}
	report(RepairStageUploading, 0, size)
	uploadToken, err := a.GetUploadToken(base64.StdEncoding.EncodeToString(videoSHA1), size)
	if err != nil {
		return finish("failed", err, "get upload token for %s: %v", candidate.Video.FileName, err)
	}
	uploadCtx, kickUpload, stopUpload := stallWatchdog(ctx, repairStallTimeout)
	uploadStarted := time.Now()
	videoToken, err := a.UploadFileWithProgress(uploadCtx, tempPath, uploadToken, func(done, total int64, _ int) {
		kickUpload()
		report(RepairStageUploading, done, total)
	})
	err = transferError(uploadCtx, err)
	stopUpload()
	result.uploadTime = time.Since(uploadStarted)
	if err != nil {
		if stopped, ok := cancelled(); ok {
			return stopped
		}
		return finish("failed", err, "re-upload %s: %v", candidate.Video.FileName, err)
	}
	result.uploadedBytes = size

	// The still keeps its original timestamp; only its video side changes.
	report(RepairStageLinking, 0, 0)
	timestamp := libraryTime(candidate.Photo.UTCTimestamp)
	policy := buildLivePhotoCommitPolicy(a)
	mediaKey, err := a.ReconcileLivePhoto(LivePhotoReconcileRequest{
		VideoToken:       videoToken,
		FileName:         candidate.Video.FileName,
		PhotoSHA1:        candidate.Photo.Sha1,
		VideoSHA1:        videoSHA1,
		CreatedAt:        timestamp,
		ModifiedAt:       timestamp,
		StoragePolicy:    policy.StoragePolicy,
		UploadQuality:    policy.UploadQuality,
		UploadDeviceInfo: policy.UploadDeviceInfo,
	})
	if err != nil {
		return finish("failed", err, "link %s to its still: %v", candidate.Video.FileName, err)
	}
	if mediaKey == "" {
		return finish("failed", nil, "link %s to its still: no media key received", candidate.Video.FileName)
	}

	// The orphan entry is a duplicate once its content is linked to the still.
	if candidate.Video.DedupKey != "" {
		report(RepairStageTrashing, 0, 0)
		if err := a.MoveRemoteMediaToTrash([]string{candidate.Video.DedupKey}); err != nil {
			return finish("failed", err, "trash orphan %s: %v (the still is already linked)", candidate.Video.FileName, err)
		}
	}
	return finish("repaired", nil, "linked %s to %s and trashed the orphan entry",
		candidate.Video.FileName, candidate.Photo.FileName)
}
