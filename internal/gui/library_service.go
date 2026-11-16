//go:build !cli

package gui

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"app/core"
)

// LibraryService gives the frontend read access to the remote library state:
// the account's storage usage and the Live Photo repair report. GUI-only.
// Every call runs a (delta) library sync against the account and refreshes
// the local library index, so the first call of a session may take a while.
type LibraryService struct {
	// onSyncProgress, when set, receives the running totals after every page
	// of a library sync so the window can show the scan live.
	onSyncProgress func(core.LibrarySyncStats)
	// onRepairProgress, when set, follows each Live Photo repair through its
	// stages.
	onRepairProgress func(core.LivePhotoRepairProgress)

	repairMu     sync.Mutex
	cancelRepair context.CancelFunc

	// syncMu serialises library syncs and index writes.
	syncMu sync.Mutex

	// Quota conversion: the upload manager re-uploads converted files, and
	// the last scan's report names what may be converted.
	uploads         *core.UploadManager
	onQuotaScan     func(core.QuotaScanProgress)
	onQuotaConvert  func(core.QuotaConvertProgress)
	onQuotaDownload func(core.QuotaDownloadProgress)
	quotaMu         sync.Mutex
	cancelQuota     context.CancelFunc
	lastQuotaReport *core.QuotaConversionReport

	// Album takeaway groups the selected account's photos into shared albums.
	onTakeaway     func(core.TakeawayProgress)
	takeawayMu     sync.Mutex
	cancelTakeaway context.CancelFunc
}

// SetTakeawayProgress wires the album-takeaway progress callback.
func SetTakeawayProgress(s *LibraryService, onProgress func(core.TakeawayProgress)) {
	s.onTakeaway = onProgress
}

// CancelTakeaway stops a running takeaway after the current request. The
// albums already made are kept.
func CancelTakeaway(s *LibraryService) {
	s.takeawayMu.Lock()
	defer s.takeawayMu.Unlock()
	if s.cancelTakeaway != nil {
		s.cancelTakeaway()
	}
}

func (s *LibraryService) startTakeawayRun() (context.Context, func(), error) {
	s.takeawayMu.Lock()
	defer s.takeawayMu.Unlock()
	if s.cancelTakeaway != nil {
		return nil, nil, fmt.Errorf("an album takeaway is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelTakeaway = cancel
	return ctx, func() {
		cancel()
		s.takeawayMu.Lock()
		s.cancelTakeaway = nil
		s.takeawayMu.Unlock()
	}, nil
}

// SetQuotaConversion wires the upload manager and progress callbacks used by
// the quota conversion. A package function, so it is not bound.
func SetQuotaConversion(s *LibraryService, uploads *core.UploadManager, onScan func(core.QuotaScanProgress), onConvert func(core.QuotaConvertProgress), onDownload func(core.QuotaDownloadProgress)) {
	s.uploads = uploads
	s.onQuotaScan = onScan
	s.onQuotaConvert = onConvert
	s.onQuotaDownload = onDownload
}

// CancelQuotaConversion stops a running quota scan or conversion after the
// current file. The running upload batch is left to finish or be cancelled
// on the upload screen.
func CancelQuotaConversion(s *LibraryService) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if s.cancelQuota != nil {
		s.cancelQuota()
	}
}

// startQuotaRun claims the single quota run slot.
func (s *LibraryService) startQuotaRun() (context.Context, func(), error) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if s.cancelQuota != nil {
		return nil, nil, fmt.Errorf("a storage scan or conversion is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelQuota = cancel
	return ctx, func() {
		cancel()
		s.quotaMu.Lock()
		s.cancelQuota = nil
		s.quotaMu.Unlock()
	}, nil
}

// syncedIndex refreshes and saves the library index. Syncs run one at a
// time (a background album fetch and a scan the user started would otherwise
// write the index at the same moment); a second caller waits and then only
// fetches what changed meanwhile.
func (s *LibraryService) syncedIndex(api *core.Api) (*core.LibraryIndex, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	cached, _ := core.LoadLibraryIndex()
	index, err := api.SyncLibraryIncremental(cached, s.reportSyncProgress)
	if err != nil {
		return nil, err
	}
	if err := index.Save(); err != nil {
		return nil, err
	}
	return index, nil
}

// QuotaConversionScan finds the files that count against the storage quota
// and have a byte-identical copy in the given folders. Nothing is changed.
func (s *LibraryService) QuotaConversionScan(folders []string) (core.QuotaConversionReport, error) {
	if len(folders) == 0 {
		return core.QuotaConversionReport{}, fmt.Errorf("choose at least one folder to search")
	}
	ctx, done, err := s.startQuotaRun()
	if err != nil {
		return core.QuotaConversionReport{}, err
	}
	defer done()

	api, err := apiFor()
	if err != nil {
		return core.QuotaConversionReport{}, err
	}
	index, err := s.syncedIndex(api)
	if err != nil {
		return core.QuotaConversionReport{}, err
	}
	sizes, digests := core.QuotaScanTargets(index.Items)
	var cache *core.HashCache
	if s.uploads != nil {
		cache = s.uploads.HashCache()
	}
	scan, err := core.ScanLocalCopies(ctx, folders, sizes, digests, cache, func(progress core.QuotaScanProgress) {
		if s.onQuotaScan != nil {
			s.onQuotaScan(progress)
		}
	})
	if cache != nil {
		cache.Save()
	}
	if err != nil {
		return core.QuotaConversionReport{}, err
	}
	report := core.BuildQuotaConversionReport(index.Items, scan)
	s.quotaMu.Lock()
	s.lastQuotaReport = &report
	s.quotaMu.Unlock()
	return report, nil
}

// QuotaDownload downloads the exact originals of counted files that have no
// copy on this computer into folder (one subfolder per year), checking each
// against the library. mediaKeys chooses files from the last scan; empty
// means all downloadable files. Nothing in Google Photos is changed. The
// returned report lists the downloaded files as convertible.
func (s *LibraryService) QuotaDownload(mediaKeys []string, folder string) (core.QuotaDownloadResult, error) {
	if folder == "" {
		return core.QuotaDownloadResult{}, fmt.Errorf("choose a folder to save the originals in")
	}
	s.quotaMu.Lock()
	report := s.lastQuotaReport
	s.quotaMu.Unlock()
	if report == nil {
		return core.QuotaDownloadResult{}, fmt.Errorf("scan for files first")
	}
	chosen := map[string]bool{}
	for _, key := range mediaKeys {
		chosen[key] = true
	}
	var selected []core.QuotaConversionItem
	for _, item := range report.Downloadable {
		if len(chosen) == 0 || chosen[item.MediaKey] {
			selected = append(selected, item)
		}
	}
	if len(selected) == 0 {
		return core.QuotaDownloadResult{}, fmt.Errorf("nothing to download; scan again")
	}

	ctx, done, err := s.startQuotaRun()
	if err != nil {
		return core.QuotaDownloadResult{}, err
	}
	defer done()
	api, err := apiFor()
	if err != nil {
		return core.QuotaDownloadResult{}, err
	}
	outcomes := api.DownloadCountedOriginals(ctx, selected, folder, func(progress core.QuotaDownloadProgress) {
		if s.onQuotaDownload != nil {
			s.onQuotaDownload(progress)
		}
	})

	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	if s.lastQuotaReport != nil {
		s.lastQuotaReport.MarkDownloaded(outcomes)
		report = s.lastQuotaReport
	}
	return core.QuotaDownloadResult{Outcomes: outcomes, Report: *report}, nil
}

// QuotaConvertAllPlan reads the library and says what "convert all" would
// do. Nothing is changed.
func (s *LibraryService) QuotaConvertAllPlan() (core.QuotaConvertAllPlan, error) {
	api, err := apiFor()
	if err != nil {
		return core.QuotaConvertAllPlan{}, err
	}
	index, err := s.syncedIndex(api)
	if err != nil {
		return core.QuotaConvertAllPlan{}, err
	}
	plan, _ := core.PlanQuotaConvertAll(index.Items)
	for _, status := range core.QuotaConversionStates(index.Items, core.LoadQuotaConversions(), nil) {
		if status.State == core.QuotaStateReady {
			plan.Waiting++
		}
	}
	return plan, nil
}

// waitForUploads blocks until the upload manager is idle. It reports false
// when the upload was paused, which stops a conversion run.
func (s *LibraryService) waitForUploads() bool {
	for s.uploads.IsRunning() {
		time.Sleep(500 * time.Millisecond)
	}
	return !s.uploads.IsPaused()
}

// QuotaConvertAll converts every counted file except Live Photos, 20 at a
// time: download the stored file into folder (checked against the original's
// fingerprint when Google keeps the original), delete the counted copy for
// good, and upload the download again without quota. Earlier conversions
// that were deleted but not uploaded yet go first. acknowledged must be true:
// the caller confirms the user accepted what is lost. limit, when above 0,
// converts only that many files (a trial run). Running it again continues
// where it stopped.
func (s *LibraryService) QuotaConvertAll(folder string, limit int, acknowledged bool) (core.QuotaConvertAllResult, error) {
	result := core.QuotaConvertAllResult{Problems: []core.QuotaDeleteOutcome{}}
	if !acknowledged {
		return result, fmt.Errorf("the conversion was not confirmed")
	}
	if folder == "" {
		folder = core.DefaultQuotaBackupFolder()
	}
	if folder == "" {
		return result, fmt.Errorf("choose a folder to keep the downloads in")
	}
	if s.uploads == nil {
		return result, fmt.Errorf("uploads are not available")
	}
	if s.uploads.IsRunning() || s.uploads.IsPaused() {
		return result, fmt.Errorf("finish or cancel the current upload first")
	}
	ctx, done, err := s.startQuotaRun()
	if err != nil {
		return result, err
	}
	defer done()
	api, err := apiFor()
	if err != nil {
		return result, err
	}
	index, err := s.syncedIndex(api)
	if err != nil {
		return result, err
	}
	send := func(progress core.QuotaConvertProgress) {
		if s.onQuotaConvert != nil {
			s.onQuotaConvert(progress)
		}
	}
	freeUpload := func(paths []string) bool {
		if len(paths) == 0 {
			return true
		}
		s.uploads.Upload(paths, core.QuotaFreeUploadOptions((&core.ConfigManager{}).SessionUploadOptions()))
		return s.waitForUploads()
	}

	// Files deleted by an earlier run but never uploaded again go first.
	var waiting []string
	for _, status := range core.QuotaConversionStates(index.Items, core.LoadQuotaConversions(), func(entry core.QuotaConversionEntry) bool {
		return core.LocalCopyMatches(ctx, entry.LocalPath, entry.SHA1Hex)
	}) {
		if status.State == core.QuotaStateReady && !status.LocalMissing {
			waiting = append(waiting, status.LocalPath)
		}
	}
	if len(waiting) > 0 {
		send(core.QuotaConvertProgress{Phase: "uploading", Total: len(waiting)})
		if !freeUpload(waiting) {
			return result, nil
		}
	}

	_, targets := core.PlanQuotaConvertAll(index.Items)
	if limit > 0 && len(targets) > limit {
		targets = targets[:limit]
	}
	result.Total = len(targets)
	batches := (len(targets) + quotaConvertBatch - 1) / quotaConvertBatch
	for batch := 0; batch < batches && ctx.Err() == nil; batch++ {
		start := batch * quotaConvertBatch
		chunk := targets[start:min(start+quotaConvertBatch, len(targets))]

		downloads := api.DownloadCountedOriginals(ctx, chunk, folder, func(progress core.QuotaDownloadProgress) {
			send(core.QuotaConvertProgress{
				Phase: "downloading", Done: start + progress.Index - 1, Total: len(targets),
				Batch: batch + 1, Batches: batches,
				FileName: progress.FileName, BytesDone: progress.BytesDone, BytesTotal: progress.BytesTotal,
			})
		})
		var ready []core.QuotaConversionItem
		for i, outcome := range downloads {
			if outcome.Status != "downloaded" {
				result.Problems = append(result.Problems, core.QuotaDeleteOutcome{
					MediaKey: outcome.MediaKey, FileName: outcome.FileName, Status: outcome.Status, Message: outcome.Message,
				})
				continue
			}
			item := chunk[i]
			item.LocalPath, item.Folder, item.SHA1Hex = outcome.LocalPath, filepath.Dir(outcome.LocalPath), outcome.SHA1Hex
			ready = append(ready, item)
		}

		deleted := api.RemoveCountedCopies(ctx, ready, func(doneInBatch int, outcome core.QuotaDeleteOutcome) {
			send(core.QuotaConvertProgress{
				Phase: "deleting", Done: start + doneInBatch, Total: len(targets), Batch: batch + 1, Batches: batches,
				FileName: outcome.FileName,
			})
		})
		var paths []string
		for i, outcome := range deleted {
			if outcome.Status == "deleted" {
				paths = append(paths, ready[i].LocalPath)
				result.Converted++
			} else {
				result.Problems = append(result.Problems, outcome)
			}
		}
		send(core.QuotaConvertProgress{
			Phase: "uploading", Done: start + len(chunk), Total: len(targets), Batch: batch + 1, Batches: batches,
		})
		// Deleted files go up again even after Stop: they now exist only
		// in the backup folder.
		if !freeUpload(paths) {
			break
		}
	}
	return result, nil
}

// quotaConvertBatch is how many files are deleted before their batch is
// uploaded again, which keeps the time a file exists only on this computer
// short.
const quotaConvertBatch = 20

// QuotaConvert converts the chosen files from the last scan: each counted
// copy is deleted for good (after its local copy is checked again) and the
// local copy is uploaded without quota, in batches. acknowledged must be
// true: the caller confirms the user accepted what is lost.
func (s *LibraryService) QuotaConvert(mediaKeys []string, acknowledged bool) ([]core.QuotaDeleteOutcome, error) {
	if !acknowledged {
		return nil, fmt.Errorf("the conversion was not confirmed")
	}
	if len(mediaKeys) == 0 {
		return nil, fmt.Errorf("no files were selected")
	}
	if s.uploads == nil {
		return nil, fmt.Errorf("uploads are not available")
	}
	if s.uploads.IsRunning() || s.uploads.IsPaused() {
		return nil, fmt.Errorf("finish or cancel the current upload first")
	}
	s.quotaMu.Lock()
	report := s.lastQuotaReport
	s.quotaMu.Unlock()
	if report == nil {
		return nil, fmt.Errorf("scan for files first")
	}
	chosen := map[string]bool{}
	for _, key := range mediaKeys {
		chosen[key] = true
	}
	var selected []core.QuotaConversionItem
	for _, item := range report.Convertible {
		if chosen[item.MediaKey] {
			selected = append(selected, item)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("the chosen files are not in the last scan; scan again")
	}

	ctx, done, err := s.startQuotaRun()
	if err != nil {
		return nil, err
	}
	defer done()
	api, err := apiFor()
	if err != nil {
		return nil, err
	}

	reportProgress := func(progress core.QuotaConvertProgress) {
		if s.onQuotaConvert != nil {
			s.onQuotaConvert(progress)
		}
	}
	batches := (len(selected) + quotaConvertBatch - 1) / quotaConvertBatch
	outcomes := make([]core.QuotaDeleteOutcome, 0, len(selected))
	for batch := 0; batch < batches; batch++ {
		if ctx.Err() != nil {
			break
		}
		start := batch * quotaConvertBatch
		chunk := selected[start:min(start+quotaConvertBatch, len(selected))]
		chunkOutcomes := api.RemoveCountedCopies(ctx, chunk, func(doneInChunk int, outcome core.QuotaDeleteOutcome) {
			reportProgress(core.QuotaConvertProgress{
				Phase: "deleting", Done: start + doneInChunk, Total: len(selected),
				Batch: batch + 1, Batches: batches, Outcome: &outcome,
			})
		})
		outcomes = append(outcomes, chunkOutcomes...)

		var paths []string
		for i, outcome := range chunkOutcomes {
			if outcome.Status == "deleted" {
				paths = append(paths, chunk[i].LocalPath)
			}
		}
		if len(paths) == 0 {
			continue
		}
		reportProgress(core.QuotaConvertProgress{
			Phase: "uploading", Done: start + len(chunk), Total: len(selected), Batch: batch + 1, Batches: batches,
		})
		// The deleted files go up again even when the run was just
		// cancelled: they exist only on this computer now.
		s.uploads.Upload(paths, core.QuotaFreeUploadOptions((&core.ConfigManager{}).SessionUploadOptions()))
		for s.uploads.IsRunning() {
			time.Sleep(500 * time.Millisecond)
		}
		if s.uploads.IsPaused() {
			// The user paused the upload; stop converting more files.
			break
		}
	}
	return outcomes, nil
}

// QuotaRecordedCount returns how many conversions are recorded, without
// touching the network.
func (s *LibraryService) QuotaRecordedCount() int {
	return len(core.LoadQuotaConversions())
}

// QuotaConversionStatus reports every recorded conversion with its state, read
// from the library.
func (s *LibraryService) QuotaConversionStatus() ([]core.QuotaConversionStatus, error) {
	entries := core.LoadQuotaConversions()
	if len(entries) == 0 {
		return []core.QuotaConversionStatus{}, nil
	}
	api, err := apiFor()
	if err != nil {
		return nil, err
	}
	index, err := s.syncedIndex(api)
	if err != nil {
		return nil, err
	}
	return core.QuotaConversionStates(index.Items, entries, func(entry core.QuotaConversionEntry) bool {
		return core.LocalCopyMatches(context.Background(), entry.LocalPath, entry.SHA1Hex)
	}), nil
}

// QuotaUploadReady uploads, without quota, every recorded file whose counted
// copy is gone but which is not back in the library yet. It returns how many
// files were started.
func (s *LibraryService) QuotaUploadReady() (int, error) {
	if s.uploads == nil {
		return 0, fmt.Errorf("uploads are not available")
	}
	if s.uploads.IsRunning() || s.uploads.IsPaused() {
		return 0, fmt.Errorf("finish or cancel the current upload first")
	}
	statuses, err := s.QuotaConversionStatus()
	if err != nil {
		return 0, err
	}
	var paths []string
	for _, status := range statuses {
		if status.State == core.QuotaStateReady && !status.LocalMissing {
			if _, err := os.Stat(status.LocalPath); err == nil {
				paths = append(paths, status.LocalPath)
			}
		}
	}
	if len(paths) > 0 {
		s.uploads.Upload(paths, core.QuotaFreeUploadOptions((&core.ConfigManager{}).SessionUploadOptions()))
	}
	return len(paths), nil
}

// QuotaForgetConverted removes finished conversions from the record.
func (s *LibraryService) QuotaForgetConverted() error {
	statuses, err := s.QuotaConversionStatus()
	if err != nil {
		return err
	}
	var finished []string
	for _, status := range statuses {
		if status.State == core.QuotaStateConverted {
			finished = append(finished, status.SHA1Hex)
		}
	}
	return core.ForgetQuotaConversions(finished)
}

// SetLivePhotoRepairProgress routes Live Photo repair progress to fn. Like
// SetLibrarySyncProgress it is a package function so it is not bound.
func SetLivePhotoRepairProgress(s *LibraryService, fn func(core.LivePhotoRepairProgress)) {
	s.onRepairProgress = fn
}

// CancelLivePhotoRepair stops a running repair batch after the current step.
// Pairs not yet attempted are reported as skipped.
func CancelLivePhotoRepair(s *LibraryService) {
	s.repairMu.Lock()
	defer s.repairMu.Unlock()
	if s.cancelRepair != nil {
		s.cancelRepair()
	}
}

func (s *LibraryService) reportRepairProgress(progress core.LivePhotoRepairProgress) {
	if s.onRepairProgress != nil {
		s.onRepairProgress(progress)
	}
}

// SetLibrarySyncProgress routes the service's sync progress to fn. It is a
// package function rather than a method so it is not bound to the frontend.
func SetLibrarySyncProgress(s *LibraryService, fn func(core.LibrarySyncStats)) {
	s.onSyncProgress = fn
}

func (s *LibraryService) reportSyncProgress(stats core.LibrarySyncStats) {
	if s.onSyncProgress != nil {
		s.onSyncProgress(stats)
	}
}

func NewLibraryService() *LibraryService {
	return &LibraryService{}
}

// apiFor builds an Api client for the GUI's selected account with the
// current preference proxy.
func apiFor() (*core.Api, error) {
	opts := (&core.ConfigManager{}).SessionUploadOptions()
	return core.NewApi(opts.Api)
}

// CachedAlbums returns the albums from the last library scan without going
// online, so the album picker opens at once.
func (s *LibraryService) CachedAlbums() core.AlbumList {
	index, _ := core.LoadLibraryIndex()
	return core.AlbumsOf(index)
}

// RefreshAlbums reads the library's changes and returns the albums. The
// app calls it in the background at start, so the album picker is ready;
// the first time it reads the whole library.
func (s *LibraryService) RefreshAlbums() (core.AlbumList, error) {
	api, err := apiFor()
	if err != nil {
		return core.AlbumList{}, err
	}
	index, err := s.syncedIndex(api)
	if err != nil {
		return core.AlbumList{}, err
	}
	return core.AlbumsOf(index), nil
}

// AccountStorage refreshes the library index and summarizes how much storage
// the account uses.
func (s *LibraryService) AccountStorage() (core.AccountStorage, error) {
	api, err := apiFor()
	if err != nil {
		return core.AccountStorage{}, err
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return api.RefreshAccountStorageWithProgress(s.reportSyncProgress)
}

// UploadStats returns the cumulative cross-session statistics.
func (s *LibraryService) UploadStats() core.UploadStats {
	return core.LoadUploadStats()
}

// LivePhotoRepairs lists the orphan Live Photo pairs found in the library.
func (s *LibraryService) LivePhotoRepairs() (core.LivePhotoRepairReport, error) {
	api, err := apiFor()
	if err != nil {
		return core.LivePhotoRepairReport{}, err
	}
	index, err := s.syncedIndex(api)
	if err != nil {
		return core.LivePhotoRepairReport{}, err
	}
	return core.FindLivePhotoRepairReport(index.Items), nil
}

// Bin lists the items in the library's bin, marking the videos the Live
// Photo repair put there. It only reads; nothing is restored or deleted.
func (s *LibraryService) Bin() (core.BinReport, error) {
	api, err := apiFor()
	if err != nil {
		return core.BinReport{}, err
	}
	index, err := s.syncedIndex(api)
	if err != nil {
		return core.BinReport{}, err
	}
	return core.ListBin(index.Items, core.LoadLivePhotoRepairLedger()), nil
}

// RepairLivePhotos repairs the given stems one pair at a time and returns one
// outcome per pair. Progress is reported through the livePhotoRepairProgress
// event; the livePhotoRepairCancel event stops the batch. An empty list is
// rejected so a request can never repair more than was chosen.
func (s *LibraryService) RepairLivePhotos(stems []string) ([]core.LivePhotoRepairOutcome, error) {
	if len(stems) == 0 {
		return nil, fmt.Errorf("no Live Photos were selected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.repairMu.Lock()
	if s.cancelRepair != nil {
		s.repairMu.Unlock()
		return nil, fmt.Errorf("a Live Photo repair is already running")
	}
	s.cancelRepair = cancel
	s.repairMu.Unlock()
	defer func() {
		s.repairMu.Lock()
		s.cancelRepair = nil
		s.repairMu.Unlock()
	}()

	api, err := apiFor()
	if err != nil {
		return nil, err
	}
	index, err := s.syncedIndex(api)
	if err != nil {
		return nil, err
	}
	report := core.FindLivePhotoRepairReport(index.Items)

	selected := make([]core.LivePhotoRepairCandidate, 0, len(stems))
	for _, candidate := range report.Candidates {
		if containsString(stems, candidate.Stem) {
			selected = append(selected, candidate)
		}
	}
	return api.RepairLivePhotoPairs(ctx, selected, s.reportRepairProgress), nil
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// AddToAlbum adds already uploaded items to an album: an album key adds to
// that album, anything else creates a new album with that name. It is used
// when an upload's album could not be reached and the user picks another.
// It returns the album keys used.
func (s *LibraryService) AddToAlbum(mediaKeys []string, albumNameOrKey string) ([]string, error) {
	if len(mediaKeys) == 0 {
		return nil, fmt.Errorf("nothing to add")
	}
	api, err := apiFor()
	if err != nil {
		return nil, err
	}
	manager := core.NewAlbumManager(api, core.NopReporter{}, slog.Default(), nil)
	return manager.AddToAlbum(mediaKeys, albumNameOrKey)
}

// DuplicateAlbums lists albums that share a title, from the last library
// read. It never goes online.
func (s *LibraryService) DuplicateAlbums() []core.DuplicateAlbumGroup {
	index, _ := core.LoadLibraryIndex()
	return core.DuplicateAlbums(core.AlbumsOf(index).Albums)
}

// BinActionResult says what a bin action did.
type BinActionResult struct {
	Done int
	// Skipped lists the chosen items that were left alone, with Reason.
	Skipped []BinSkipped
}

// BinSkipped is a bin item an action left alone.
type BinSkipped struct {
	FileName string
	Reason   string
}

// binReport reads the bin from a fresh library sync.
func (s *LibraryService) binReport() (*core.Api, core.BinReport, error) {
	api, err := apiFor()
	if err != nil {
		return nil, core.BinReport{}, err
	}
	index, err := s.syncedIndex(api)
	if err != nil {
		return nil, core.BinReport{}, err
	}
	return api, core.ListBin(index.Items, core.LoadLivePhotoRepairLedger()), nil
}

// BinRestore puts the chosen bin items (by media key) back in the library.
func (s *LibraryService) BinRestore(mediaKeys []string) (BinActionResult, error) {
	result := BinActionResult{Skipped: []BinSkipped{}}
	if len(mediaKeys) == 0 {
		return result, fmt.Errorf("no items were chosen")
	}
	api, report, err := s.binReport()
	if err != nil {
		return result, err
	}
	wanted := map[string]bool{}
	for _, key := range mediaKeys {
		wanted[key] = true
	}
	var dedupKeys []string
	for _, item := range report.Items {
		if !wanted[item.MediaKey] {
			continue
		}
		if item.DedupKey == "" || item.Shared {
			result.Skipped = append(result.Skipped, BinSkipped{FileName: item.FileName, Reason: "not in your own library"})
			continue
		}
		dedupKeys = append(dedupKeys, item.DedupKey)
	}
	if len(dedupKeys) == 0 {
		return result, nil
	}
	if err := api.RestoreMediaFromTrash(dedupKeys); err != nil {
		return result, err
	}
	result.Done = len(dedupKeys)
	_, _ = s.syncedIndex(api)
	return result, nil
}

// BinDelete deletes the chosen bin items (by media key) for good; with
// everything set it empties the whole bin. confirmed must be true: the caller
// confirms the user accepted that this cannot be undone. Items whose content
// is also in the library, and items from shared albums, are left alone.
func (s *LibraryService) BinDelete(mediaKeys []string, everything bool, confirmed bool) (BinActionResult, error) {
	result := BinActionResult{Skipped: []BinSkipped{}}
	if !confirmed {
		return result, fmt.Errorf("deleting for good was not confirmed")
	}
	if len(mediaKeys) == 0 && !everything {
		return result, fmt.Errorf("no items were chosen")
	}
	if everything {
		mediaKeys = nil
	}
	api, report, err := s.binReport()
	if err != nil {
		return result, err
	}
	dedupKeys, skipped := core.BinSelection(report, mediaKeys)
	for _, item := range skipped {
		reason := "a copy is still in your library, so it is left for Google to remove"
		if item.Shared {
			reason = "from a shared album"
		}
		result.Skipped = append(result.Skipped, BinSkipped{FileName: item.FileName, Reason: reason})
	}
	for start := 0; start < len(dedupKeys); start += binDeleteBatch {
		batch := dedupKeys[start:min(start+binDeleteBatch, len(dedupKeys))]
		if err := api.DeleteRemoteMediaPermanently(batch); err != nil {
			_, _ = s.syncedIndex(api)
			return result, err
		}
		result.Done += len(batch)
	}
	_, _ = s.syncedIndex(api)
	return result, nil
}

// binDeleteBatch is how many items one delete request carries.
const binDeleteBatch = 200

// UploadHistory returns the recent upload runs, newest first.
func (s *LibraryService) UploadHistory() []core.UploadRun {
	return core.LoadUploadHistory()
}

// ClearUploadHistory forgets the recorded upload runs.
func (s *LibraryService) ClearUploadHistory() {
	core.ClearUploadHistory()
}

// AccountPhoto returns the selected account's photo as a data: URL, or ""
// when it has none or it cannot be fetched (the window then shows the
// account's initial).
func (s *LibraryService) AccountPhoto(email string) string {
	settings := (&core.ConfigManager{}).GetConfig().Preferences
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dataURL, _ := core.AccountPhotoDataURL(ctx, email, settings.Proxy)
	return dataURL
}

// TakeawayPlan reads the selected account's library and reports what an
// album takeaway would gather at the given album size. Switch accounts in
// the sidebar to choose the source account.
func (s *LibraryService) TakeawayPlan(albumSize int) (core.TakeawayPlan, error) {
	api, err := apiFor()
	if err != nil {
		return core.TakeawayPlan{}, err
	}
	index, err := s.syncedIndex(api)
	if err != nil {
		return core.TakeawayPlan{}, err
	}
	return core.PlanTakeaway(index.Items, albumSize), nil
}

// PrepareTakeaway groups the selected account's own photos and videos into
// shared albums named "gotohp takeaway 1, 2, …", albumSize items each. It
// only groups; nothing is uploaded, moved or deleted. confirmed must be
// true. Progress is reported through the takeawayProgress event; the
// takeawayCancel event stops it, keeping the albums already made.
func (s *LibraryService) PrepareTakeaway(albumSize int, confirmed bool) (core.TakeawayResult, error) {
	if !confirmed {
		return core.TakeawayResult{}, fmt.Errorf("the takeaway was not confirmed")
	}
	ctx, done, err := s.startTakeawayRun()
	if err != nil {
		return core.TakeawayResult{}, err
	}
	defer done()
	api, err := apiFor()
	if err != nil {
		return core.TakeawayResult{}, err
	}
	index, err := s.syncedIndex(api)
	if err != nil {
		return core.TakeawayResult{}, err
	}
	keys, _ := core.TakeawayMediaKeys(index.Items)
	return api.GatherTakeaway(ctx, keys, albumSize, func(progress core.TakeawayProgress) {
		if s.onTakeaway != nil {
			s.onTakeaway(progress)
		}
	})
}
