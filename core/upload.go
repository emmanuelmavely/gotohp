package core

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// FilesDroppedEvent is emitted when files are dropped on any drop zone
type FilesDroppedEvent struct {
	Files    []string `json:"files"`
	DropZone string   `json:"dropZone"`
}

// StartUploadEvent is received from frontend to start upload
type StartUploadEvent struct {
	Files []string `json:"files"`
}

type UploadManager struct {
	mu       sync.Mutex
	wg       sync.WaitGroup
	cancel   chan struct{}
	canceled bool
	running  bool
	reporter UploadReporter
	logger   *slog.Logger
	// apiOptions is captured from the options of the run in progress.
	apiOptions ApiOptions
	// hashCache persists file digests across runs. Created lazily on the
	// first Upload and saved after each run.
	hashCache *HashCache
	// autoResume enables pausing a run when the network fails and resuming
	// it automatically when connectivity returns. Only the GUI enables it;
	// the CLI ends the run and leaves the journal for an explicit resume.
	autoResume bool
	// paused is true between a network pause and the automatic resume. A
	// user cancel during the pause also discards the journal.
	paused bool
}

// NewUploadManager creates a manager that reports progress to reporter. A nil
// reporter or logger discards output.
func NewUploadManager(reporter UploadReporter, logger *slog.Logger) *UploadManager {
	if reporter == nil {
		reporter = NopReporter{}
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &UploadManager{
		reporter: reporter,
		logger:   logger,
	}
}

// HashCache returns the run-scoped digest cache, creating it on first use.
// The watch folder manager shares it so a startup rescan can tell changed
// files from unchanged ones.
func (m *UploadManager) HashCache() *HashCache {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hashCache == nil {
		m.hashCache = NewHashCache(HashCachePath())
	}
	return m.hashCache
}

func (m *UploadManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func (m *UploadManager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil && !m.canceled {
		close(m.cancel)
		m.canceled = true
		// Don't set to nil - readers still need to detect closure via select
	}
	// A cancel during a network pause ends the stopped run for good: drop
	// the journal so the next start does not offer it again.
	if m.paused {
		m.paused = false
		go DiscardUploadJournal()
	}
}

// SetAutoResume enables pausing and automatic resuming on network failures.
func (m *UploadManager) SetAutoResume(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.autoResume = enabled
}

// isCancelled checks if cancellation has been requested
func (m *UploadManager) isCancelled() bool {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel == nil {
		return false
	}
	select {
	case <-cancel:
		return true
	default:
		return false
	}
}

// getCancelChan returns the cancel channel safely
func (m *UploadManager) getCancelChan() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancel
}

type UploadBatchStart struct {
	Total      int   `json:"Total"`
	TotalBytes int64 `json:"TotalBytes"`
}

type FileUploadResult struct {
	MediaKey     string   `json:"MediaKey"`
	IsError      bool     `json:"IsError"`
	IsLivePhoto  bool     `json:"IsLivePhoto"`
	Skipped      bool     `json:"Skipped"`
	SkipCode     string   `json:"SkipCode"`
	SkipReason   string   `json:"SkipReason"`
	Error        error    `json:"-"`
	ErrorMessage string   `json:"ErrorMessage"`
	Path         string   `json:"Path"`
	Paths        []string `json:"Paths"`
	// Bytes is the total size of the item's files. Zero when the size is
	// unknown or when a Live Photo delta already adjusted the reported total.
	Bytes int64 `json:"Bytes"`
	// Transferred is true only when the bytes actually went over the wire.
	// False for hash-check hits, skipped duplicates, and errors.
	Transferred bool `json:"Transferred"`
}

type ThreadStatus struct {
	WorkerID      int    `json:"WorkerID"`
	Status        string `json:"Status"` // "idle", "hashing", "checking", "uploading", "finalizing", "completed", "skipped", "error"
	FilePath      string `json:"FilePath"`
	FileName      string `json:"FileName"`
	Message       string `json:"Message"`
	BytesUploaded int64  `json:"BytesUploaded"`
	BytesTotal    int64  `json:"BytesTotal"`
	Attempt       int    `json:"Attempt"` // Current attempt number (1-based), 0 if not applicable
}

// Upload starts an upload run in the background. Progress is delivered through
// the manager's UploadReporter; UploadStop marks the end of the run.
func (m *UploadManager) Upload(paths []string, opts UploadOptions) {
	opts = opts.normalized()
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.cancel = make(chan struct{})
	m.canceled = false
	m.apiOptions = opts.Api
	// A fresh run supersedes any network pause. Its journal replaces the
	// paused run's journal, whose pending items are dropped.
	m.paused = false
	if opts.HashCache == nil {
		if m.hashCache == nil {
			m.hashCache = NewHashCache(HashCachePath())
		}
		opts.HashCache = m.hashCache
	}
	m.mu.Unlock()

	// Make preflight visible immediately so a long directory or metadata scan can
	// be cancelled from the UI.
	m.reporter.UploadStart(UploadBatchStart{})

	targetPaths, err := filterGooglePhotosFilesWithCancel(paths, opts, m.isCancelled)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			m.finish()
			return
		}
		m.reporter.FileResult(FileUploadResult{
			IsError:      true,
			Error:        err,
			ErrorMessage: err.Error(),
		})
		m.finish()
		return
	}
	workItems, preflightWarnings := ClassifyUploadWork(targetPaths, LivePhotoClassificationOptions{
		Enabled:             opts.PairLivePhotos,
		SkipIncomplete:      opts.SkipIncompleteLivePhotos,
		IgnoreAppleMetadata: opts.IgnoreAppleMetadata,
		Cancelled:           m.isCancelled,
	}, nil)
	if m.isCancelled() {
		m.finish()
		return
	}
	m.reportPreflight(len(workItems), preflightWarnings)

	if len(workItems) == 0 {
		m.finish()
		return
	}

	journal := newUploadJournal(paths, opts, workItems, preflightWarnings)
	journal.Save()

	// Preflight-skipped items are counted in Total but not in workItems, so
	// their bytes belong in the reported total too.
	var extraTotalBytes int64
	for _, warning := range preflightWarnings {
		if isSkippedPreflightWarning(warning.Code) {
			extraTotalBytes += sumPathSizes(warning.Paths)
		}
	}

	go m.runWorkItems(workItems, opts, journal, extraTotalBytes)
}

// journalSaveInterval throttles journal writes during a run.
const journalSaveInterval = 2 * time.Second

// Resume continues an interrupted upload run from the journal file. Pending
// and failed items are retried; done and skipped ones are not. It reports
// whether a resume run was started.
func (m *UploadManager) Resume() bool {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return false
	}
	journal, err := loadUploadJournal()
	if err != nil || journal == nil {
		m.mu.Unlock()
		return false
	}
	m.running = true
	m.cancel = make(chan struct{})
	m.canceled = false
	if m.hashCache == nil {
		m.hashCache = NewHashCache(HashCachePath())
	}
	hashCache := m.hashCache
	m.mu.Unlock()

	opts := journal.Options.normalized()
	opts.HashCache = hashCache

	remaining := journal.Remaining()
	workItems := make([]UploadWorkItem, 0, len(remaining))
	for _, item := range remaining {
		if len(item.Paths) == 0 {
			continue
		}
		if _, err := os.Stat(item.Paths[0]); err != nil {
			journal.markJournalItemFailed(item.Paths[0], "file no longer exists on disk")
			continue
		}
		switch item.Kind {
		case string(UploadWorkSingle):
			workItems = append(workItems, UploadWorkItem{
				Kind:   UploadWorkSingle,
				Single: &SingleMedia{Path: item.Paths[0]},
			})
		case string(UploadWorkLivePhoto):
			if len(item.Paths) < 2 {
				journal.markJournalItemFailed(item.Paths[0], "incomplete Live Photo pair in journal")
				continue
			}
			workItems = append(workItems, UploadWorkItem{
				Kind:      UploadWorkLivePhoto,
				LivePhoto: &LivePhotoPair{PhotoPath: item.Paths[0], VideoPath: item.Paths[1]},
			})
		default:
			journal.markJournalItemFailed(item.Paths[0], "unknown work kind in journal")
		}
	}

	// Make the resumed batch visible immediately so the UI picks it up.
	m.reporter.UploadStart(UploadBatchStart{Total: len(workItems)})

	if len(workItems) == 0 {
		// Everything already reached a terminal state; the journal is stale.
		journal.Remove()
		m.finish()
		return true
	}

	go m.runWorkItems(workItems, opts, journal, 0)
	return true
}

// runWorkItems runs the worker pipeline for an already-classified work list
// and owns the journal for it. It is called from Upload and Resume and must
// run in its own goroutine.
func (m *UploadManager) runWorkItems(workItems []UploadWorkItem, opts UploadOptions, journal *UploadJournal, extraTotalBytes int64) {
	if _, err := NewApi(opts.Api); err != nil {
		for _, item := range workItems {
			paths := uploadWorkPaths(item)
			result := FileUploadResult{
				IsError:      true,
				Error:        err,
				ErrorMessage: err.Error(),
				Path:         uploadWorkPrimaryPath(item),
				Paths:        paths,
				Bytes:        sumPathSizes(paths),
			}
			m.reporter.FileResult(result)
			journal.RecordResult(result)
		}
		journal.Save()
		m.finish()
		return
	}

	// Calculate total bytes asynchronously and emit update when complete
	go func() {
		var totalBytes int64
		for _, item := range workItems {
			for _, path := range uploadWorkPaths(item) {
				if info, err := os.Stat(path); err == nil {
					totalBytes += info.Size()
				}
			}
		}
		m.reporter.TotalBytes(totalBytes + extraTotalBytes)
	}()

	// Don't start more threads than files to process
	numWorkers := min(opts.Threads, len(workItems))

	// Create a worker pool for concurrent uploads
	workChan := make(chan UploadWorkItem, len(workItems))
	results := make(chan FileUploadResult, len(workItems))

	// Sample disk-read throughput once per second for the GUI graph.
	runStart := time.Now()
	diskStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		lastRead := DiskReadTotal()
		for {
			select {
			case <-diskStop:
				return
			case <-ticker.C:
				total := DiskReadTotal()
				m.reporter.DiskRead(total - lastRead)
				lastRead = total
			}
		}
	}()

	// Start workers
	for i := range numWorkers {
		m.wg.Add(1)
		go m.runWorker(i, workChan, results, opts)
	}

	// Send work to workers
	go func() {
	LOOP:
		for _, item := range workItems {
			select {
			case <-m.cancel:
				break LOOP
			case workChan <- item:
			}
		}
		close(workChan)
	}()

	// Handle results, wait for completion, and create album if configured
	go func() {
		// Collect successful uploads with path -> mediaKey mapping for AUTO mode
		successfulUploads := make(map[string]string) // path -> mediaKey

		// Wait for all workers to finish in a separate goroutine, then close results
		go func() {
			m.wg.Wait()
			close(results)
		}()

			// Process all results (this blocks until results channel is closed)
		lastJournalSave := time.Now()
		consecutiveNetFails := 0
		var runBytes, runFiles, runFails, runSkips int64
		for result := range results {
			m.reporter.FileResult(result)
			journal.RecordResult(result)
			if time.Since(lastJournalSave) >= journalSaveInterval {
				journal.Save()
				lastJournalSave = time.Now()
			}
			switch {
			case result.IsError:
				runFails++
				m.logger.Error("upload error", "path", result.Path, "error", result.Error)
				// Repeated network failures mean the connection is gone:
				// pause the run instead of failing everything.
				if isNetworkError(result.Error) {
					consecutiveNetFails++
					if m.autoResume && consecutiveNetFails >= networkPauseThreshold {
						m.pauseRun("Internet connection lost. The upload paused and will resume automatically.")
					}
				} else {
					consecutiveNetFails = 0
				}
			case result.Skipped:
				runSkips++
			default:
				runFiles++
				if result.Transferred {
					runBytes += result.Bytes
				}
				consecutiveNetFails = 0
				m.logger.Info("upload success", "path", result.Path)
				if result.MediaKey != "" {
					successfulUploads[result.Path] = result.MediaKey
				}
			}
		}
		close(diskStop)

		m.logger.Info("upload complete", "successful", len(successfulUploads),
			"albumName", opts.AlbumName, "albumAutoMode", opts.AlbumAutoMode)

		if len(successfulUploads) > 0 {
			m.handleAlbumCreation(successfulUploads, opts.AlbumName, opts.AlbumAutoMode)
		}

		m.mu.Lock()
		if m.hashCache != nil {
			m.hashCache.Save()
		}
		m.mu.Unlock()

		if journal.Complete() {
			journal.Remove()
		} else {
			journal.Save()
		}

		RecordUploadRun(runBytes, runFiles, runFails, runSkips, int64(time.Since(runStart).Seconds()))

		m.finish()
	}()
}

// finish signals the end of a run and releases the manager for the next one.
func (m *UploadManager) finish() {
	m.reporter.UploadStop()
	m.mu.Lock()
	m.running = false
	m.mu.Unlock()
}

// networkPauseThreshold is how many consecutive network-classified failures
// trigger a pause.
const networkPauseThreshold = 3

// pauseRun stops the in-flight run, keeps the journal, and schedules an
// automatic resume once connectivity is back. Called from the results
// goroutine; safe to call again after a pause already started.
func (m *UploadManager) pauseRun(reason string) {
	m.mu.Lock()
	if m.paused || m.canceled {
		m.mu.Unlock()
		return
	}
	m.paused = true
	if m.cancel != nil && !m.canceled {
		close(m.cancel)
		m.canceled = true
	}
	m.mu.Unlock()

	m.reporter.UploadPaused(reason)
	m.logger.Warn("upload paused", "reason", reason)

	go m.waitAndResume()
}

// waitAndResume polls connectivity until the network is back, then resumes
// the paused run from the journal.
func (m *UploadManager) waitAndResume() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		paused := m.paused
		running := m.running
		autoResume := m.autoResume
		m.mu.Unlock()
		// The pause was resolved another way (user cancel or a new upload).
		if !paused || !autoResume {
			return
		}
		// Let the stopped run wind down so the journal is fully saved.
		if running {
			continue
		}
		if !probeConnectivity() {
			continue
		}
		m.mu.Lock()
		if !m.paused {
			m.mu.Unlock()
			return
		}
		m.paused = false
		m.mu.Unlock()
		m.logger.Info("network restored, resuming upload")
		if !m.Resume() {
			m.logger.Warn("resume after network pause failed")
		}
		return
	}
}

func (m *UploadManager) reportPreflight(uploadItemCount int, warnings []PreflightWarning) {
	skippedCount := 0
	for _, warning := range warnings {
		if isSkippedPreflightWarning(warning.Code) {
			skippedCount++
		}
	}

	// Start before emitting warnings/results so the frontend resets its previous
	// batch while retaining every event from this preflight.
	m.reporter.UploadStart(UploadBatchStart{
		Total:      uploadItemCount + skippedCount,
		TotalBytes: 0,
	})
	for _, warning := range warnings {
		m.reporter.Warning(warning)
		if !isSkippedPreflightWarning(warning.Code) {
			continue
		}
		primaryPath := ""
		if len(warning.Paths) > 0 {
			primaryPath = warning.Paths[0]
		}
		m.reporter.FileResult(FileUploadResult{
			IsLivePhoto: true,
			Skipped:     true,
			SkipCode:    warning.Code,
			SkipReason:  warning.Message,
			Path:        primaryPath,
			Paths:       warning.Paths,
			Bytes:       sumPathSizes(warning.Paths),
		})
	}
}

func isSkippedPreflightWarning(code string) bool {
	return code == "incomplete-live-photo-skipped" || code == "ambiguous-filename-stem"
}

// handleAlbumCreation handles album creation based on config (manual name/key or AUTO mode)
func (m *UploadManager) handleAlbumCreation(uploads map[string]string, albumName string, albumAutoMode bool) {
	// Check if cancelled before starting album creation
	if m.isCancelled() {
		m.logger.Info("Upload cancelled, skipping album creation")
		return
	}

	m.logger.Info(fmt.Sprintf("handleAlbumCreation called with %d uploads", len(uploads)))

	// Create API once for all album operations
	api, err := NewApi(m.apiOptions)
	if err != nil {
		m.logger.Error(fmt.Sprintf("failed to create API for album creation: %v", err))
		m.reporter.AlbumError(AlbumError{
			AlbumName: albumName,
			Error:     fmt.Sprintf("failed to initialize API: %v", err),
		})
		return
	}

	albumManager := NewAlbumManager(api, m.reporter, m.logger, m.getCancelChan())

	// Check if AUTO mode is enabled
	if albumAutoMode {
		m.logger.Info("AUTO mode enabled, creating albums from directories")
		m.createAlbumsFromDirectories(albumManager, uploads)
		return
	}

	// Manual mode: use AlbumName if set
	if albumName == "" {
		m.logger.Info("No album name set and AUTO mode disabled, skipping album creation")
		return
	}

	m.logger.Info(fmt.Sprintf("Creating album with name/key: '%s'", albumName))

	mediaKeys := make([]string, 0, len(uploads))
	for _, mediaKey := range uploads {
		mediaKeys = append(mediaKeys, mediaKey)
	}

	m.logger.Info(fmt.Sprintf("Adding %d media keys to album '%s'", len(mediaKeys), albumName))

	albumKeys, err := albumManager.AddToAlbum(mediaKeys, albumName)
	if err != nil {
		m.logger.Error(fmt.Sprintf("failed to create album '%s': %v", albumName, err))
		m.reporter.AlbumError(AlbumError{
			AlbumName: albumName,
			Error:     err.Error(),
		})
		return
	}
	m.logger.Info(fmt.Sprintf("created album '%s' with %d items, album keys: %v", albumName, len(mediaKeys), albumKeys))
}

// createAlbumsFromDirectories creates albums based on parent directory names (AUTO mode)
func (m *UploadManager) createAlbumsFromDirectories(albumManager *AlbumManager, uploads map[string]string) {
	// Group media keys by parent directory
	mediaKeysByDir := make(map[string][]string)

	for filePath, mediaKey := range uploads {
		parentDir := filepath.Dir(filePath)
		mediaKeysByDir[parentDir] = append(mediaKeysByDir[parentDir], mediaKey)
	}

	// Create an album for each directory
	for dirPath, mediaKeys := range mediaKeysByDir {
		albumName := filepath.Base(dirPath)
		if albumName == "" || albumName == "." {
			albumName = "Uploads"
		}

		albumKeys, err := albumManager.AddToAlbum(mediaKeys, albumName)
		if err != nil {
			m.logger.Error(fmt.Sprintf("failed to create album '%s': %v", albumName, err))
			m.reporter.AlbumError(AlbumError{
				AlbumName: albumName,
				Error:     err.Error(),
			})
			continue
		}
		m.logger.Info(fmt.Sprintf("created album '%s' with %d items, album keys: %v", albumName, len(mediaKeys), albumKeys))
	}
}

// supportedFormats is a map of file extensions supported by Google Photos (O(1) lookup)
var supportedFormats = map[string]bool{
	// Photo formats
	"avif": true, "bmp": true, "gif": true, "heic": true, "heif": true, "ico": true,
	"jpg": true, "jpeg": true, "png": true, "tif": true, "tiff": true, "webp": true,
	"cr2": true, "cr3": true, "nef": true, "arw": true, "orf": true,
	"raf": true, "rw2": true, "pef": true, "sr2": true, "dng": true,
	// Video formats
	"3gp": true, "3g2": true, "asf": true, "avi": true, "divx": true,
	"m2t": true, "m2ts": true, "m4v": true, "mkv": true, "mmv": true,
	"mod": true, "mov": true, "mp4": true, "mpg": true, "mpeg": true,
	"mts": true, "tod": true, "wmv": true, "ts": true, "webm": true,
}

// isSupportedByGooglePhotos checks if a file extension is supported by Google Photos
func isSupportedByGooglePhotos(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return false
	}
	// Remove the dot and check map
	return supportedFormats[ext[1:]]
}

func scanDirectoryForFiles(path string, recursive bool, excludePattern string, cancelled func() bool) ([]string, error) {
	var files []string

	if cancelled != nil && cancelled() {
		return nil, context.Canceled
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if cancelled != nil && cancelled() {
			return nil, context.Canceled
		}
		fullPath := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			if excludePattern != "" && entry.Name() == excludePattern {
				continue
			}
			if recursive {
				subFiles, err := scanDirectoryForFiles(fullPath, recursive, excludePattern, cancelled)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						return nil, err
					}
					continue
				}
				files = append(files, subFiles...)
			}
		} else {
			files = append(files, fullPath)
		}
	}

	return files, nil
}

// FilterGooglePhotosFiles expands paths into the files that would be uploaded
// under opts: directories are scanned, unsupported files dropped, duplicates removed.
func FilterGooglePhotosFiles(paths []string, opts UploadOptions) ([]string, error) {
	return filterGooglePhotosFilesWithCancel(paths, opts, nil)
}

func filterGooglePhotosFilesWithCancel(paths []string, opts UploadOptions, cancelled func() bool) ([]string, error) {
	var supportedFiles []string
	type seenUploadFile struct {
		canonicalPath string
		info          os.FileInfo
	}
	seenFiles := make(map[string][]seenUploadFile)
	appendFile := func(path string, info os.FileInfo) {
		if !opts.DisableUnsupportedFilesFilter && !isSupportedByGooglePhotos(path) {
			return
		}
		canonicalPath := canonicalUploadPath(path)
		bucketKey := canonicalPath
		if runtime.GOOS == "windows" {
			bucketKey = strings.ToLower(bucketKey)
		}
		if info == nil {
			info, _ = os.Stat(path)
		}
		for _, seen := range seenFiles[bucketKey] {
			if canonicalPath == seen.canonicalPath ||
				(info != nil && seen.info != nil && os.SameFile(info, seen.info)) {
				return
			}
		}
		seenFiles[bucketKey] = append(seenFiles[bucketKey], seenUploadFile{
			canonicalPath: canonicalPath,
			info:          info,
		})
		supportedFiles = append(supportedFiles, path)
	}

	for _, path := range paths {
		if cancelled != nil && cancelled() {
			return nil, context.Canceled
		}
		fileInfo, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("error accessing path %s: %w", path, err)
		}

		if fileInfo.IsDir() {
			files, err := scanDirectoryForFiles(path, opts.Recursive, opts.ExcludePattern, cancelled)
			if err != nil {
				return nil, fmt.Errorf("error scanning directory %s: %w", path, err)
			}

			for _, file := range files {
				if cancelled != nil && cancelled() {
					return nil, context.Canceled
				}
				appendFile(file, nil)
			}
		} else {
			appendFile(path, fileInfo)
		}
	}

	return supportedFiles, nil
}

func canonicalUploadPath(path string) string {
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		canonicalPath = filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(canonicalPath); err == nil {
		canonicalPath = resolved
	}
	return filepath.Clean(canonicalPath)
}

// UploadFile uploads a single file and returns its media key.
func UploadFile(ctx context.Context, api *Api, filePath string, opts UploadOptions, workerID int, reporter UploadReporter) (string, error) {
	if reporter == nil {
		reporter = NopReporter{}
	}
	mediaKey, _, err := uploadSingleFile(ctx, api, filePath, opts, workerID, reporter)
	return mediaKey, err
}

// uploadSingleFile returns the media key, whether the bytes were transferred
// (false when the file was already in the library), and an error.
func uploadSingleFile(ctx context.Context, api *Api, filePath string, opts UploadOptions, workerID int, reporter UploadReporter) (string, bool, error) {
	fileName := filepath.Base(filePath)
	mediakey := ""

	// Determine the timestamp to use for the upload.
	// Default to file mtime, but allow filename-based timestamp to take precedence if enabled.
	var uploadTimestamp int64
	if info, err := os.Stat(filePath); err == nil {
		uploadTimestamp = info.ModTime().Unix()
	}
	if opts.SetDateFromFilename {
		if t, ok := parseTimestampFromFilename(filePath); ok {
			uploadTimestamp = t.Unix()
		}
	}

	// Stage 1: Hashing
	reporter.ThreadStatus(ThreadStatus{
		WorkerID: workerID,
		Status:   "hashing",
		FilePath: filePath,
		FileName: fileName,
		Message:  "Hashing...",
	})

	sha1_hash_bytes, err := calculateSHA1Cached(ctx, filePath, opts.HashCache)
	if err != nil {
		return "", false, fmt.Errorf("error calculating hash file: %w", err)
	}

	sha1_hash_b64 := base64.StdEncoding.EncodeToString([]byte(sha1_hash_bytes))

	// Stage 2: Checking if exists in library
	if !opts.ForceUpload {
		reporter.ThreadStatus(ThreadStatus{
			WorkerID: workerID,
			Status:   "checking",
			FilePath: filePath,
			FileName: fileName,
			Message:  "Checking if file exists in library...",
		})

		mediakey, err = api.FindRemoteMediaByHash(sha1_hash_bytes)
		if err != nil {
			// Non-fatal: log via callback and continue with upload
			reporter.ThreadStatus(ThreadStatus{
				WorkerID: workerID,
				Status:   "checking",
				FilePath: filePath,
				FileName: fileName,
				Message:  fmt.Sprintf("Hash check warning: %v, proceeding with upload", err),
			})
		}
		if len(mediakey) > 0 {
			reporter.ThreadStatus(ThreadStatus{
				WorkerID: workerID,
				Status:   "completed",
				FilePath: filePath,
				FileName: fileName,
				Message:  "Already in library",
			})
			if opts.DeleteFromHost {
				if err := os.Remove(filePath); err != nil {
					return mediakey, false, fmt.Errorf("file exists in library but failed to delete local copy: %w", err)
				}
			}
			return mediakey, false, nil
		}
	}

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", false, fmt.Errorf("error getting file info: %w", err)
	}

	// Stage 3: Uploading
	fileSize := fileInfo.Size()
	reporter.ThreadStatus(ThreadStatus{
		WorkerID:      workerID,
		Status:        "uploading",
		FilePath:      filePath,
		FileName:      fileName,
		Message:       "Uploading...",
		BytesUploaded: 0,
		BytesTotal:    fileSize,
	})

	token, err := api.GetUploadToken(sha1_hash_b64, fileSize)
	if err != nil {
		return "", false, fmt.Errorf("error uploading file: %w", err)
	}

	// Create progress callback for upload
	progressCallback := func(bytesUploaded, bytesTotal int64, attempt int) {
		message := "Uploading..."
		if attempt > 1 {
			message = fmt.Sprintf("Retrying... (attempt %d)", attempt)
		}
		reporter.ThreadStatus(ThreadStatus{
			WorkerID:      workerID,
			Status:        "uploading",
			FilePath:      filePath,
			FileName:      fileName,
			Message:       message,
			BytesUploaded: bytesUploaded,
			BytesTotal:    bytesTotal,
			Attempt:       attempt,
		})
	}

	finalizeToken, err := api.UploadFileWithProgress(ctx, filePath, token, progressCallback)
	if err != nil {
		return "", false, fmt.Errorf("error uploading file: %w", err)
	}
	commitToken, err := finalizeToken.legacyCommitToken()
	if err != nil {
		return "", false, fmt.Errorf("error decoding upload finalize token: %w", err)
	}

	// Stage 4: Finalizing
	reporter.ThreadStatus(ThreadStatus{
		WorkerID: workerID,
		Status:   "finalizing",
		FilePath: filePath,
		FileName: fileName,
		Message:  "Committing upload...",
	})

	mediaKey, err := api.CommitUpload(commitToken, fileInfo.Name(), sha1_hash_bytes, uploadTimestamp)
	if err != nil {
		return "", false, fmt.Errorf("error committing file: %w", err)
	}

	if len(mediaKey) == 0 {
		return "", false, fmt.Errorf("media key not received")
	}

	if opts.DeleteFromHost {
		if err := os.Remove(filePath); err != nil {
			return mediaKey, true, fmt.Errorf("uploaded successfully but failed to delete file: %w", err)
		}
	}

	return mediaKey, true, nil
}

func (m *UploadManager) runWorker(workerID int, workChan <-chan UploadWorkItem, results chan<- FileUploadResult, opts UploadOptions) {
	defer m.wg.Done()
	cancel := m.getCancelChan()

	// Emit idle status initially
	m.reporter.ThreadStatus(ThreadStatus{
		WorkerID: workerID,
		Status:   "idle",
		Message:  "Waiting for files...",
	})

	// Create API client once per worker for connection reuse
	api, err := NewApi(opts.Api)
	if err != nil {
		m.reporter.ThreadStatus(ThreadStatus{
			WorkerID: workerID,
			Status:   "error",
			Message:  fmt.Sprintf("Failed to initialize API: %v", err),
		})
		return
	}

	for item := range workChan {
		select {
		case <-cancel:
			m.reporter.ThreadStatus(ThreadStatus{
				WorkerID: workerID,
				Status:   "idle",
				Message:  "Cancelled",
			})
			return // Stop if cancellation is requested
		default:
			ctx, cancelUpload := context.WithCancel(context.Background())
			go func() {
				select {
				case <-cancel:
					cancelUpload()
				case <-ctx.Done():
					// Upload completed or context cancelled
				}
			}()

			path := uploadWorkPrimaryPath(item)
			paths := uploadWorkPaths(item)
			isLivePhoto := item.Kind == UploadWorkLivePhoto
			mediaKey, transferred, skipped, err := uploadWorkItem(ctx, api, item, opts, workerID, m.reporter)
			if err != nil && mediaKey != "" {
				results <- FileUploadResult{IsLivePhoto: isLivePhoto, Path: path, Paths: paths, MediaKey: mediaKey, Bytes: sumPathSizes(paths), Transferred: true}
				m.reporter.Warning(PreflightWarning{
					Paths:   paths,
					Code:    "local-cleanup-failed",
					Message: err.Error(),
				})
				m.reporter.ThreadStatus(ThreadStatus{
					WorkerID: workerID,
					Status:   "completed",
					FilePath: path,
					FileName: filepath.Base(path),
					Message:  fmt.Sprintf("Uploaded, but local cleanup failed: %v", err),
				})
			} else if err != nil {
				results <- FileUploadResult{IsError: true, IsLivePhoto: isLivePhoto, Error: err, ErrorMessage: err.Error(), Path: path, Paths: paths, Bytes: sumPathSizes(paths)}
				m.reporter.ThreadStatus(ThreadStatus{
					WorkerID: workerID,
					Status:   "error",
					FilePath: path,
					FileName: filepath.Base(path),
					Message:  fmt.Sprintf("Error: %v", err),
				})
			} else if skipped {
				skipCode := "remote-duplicate"
				skipReason := "Skipped because this file already exists remotely"
				if isLivePhoto {
					skipCode = "remote-live-photo-component-exists"
					skipReason = "Skipped because a Live Photo component already exists remotely"
				}
				// No Bytes here: uploadLivePhotoWithCallback already shrank the
				// reported total with a TotalBytesDelta for these skips.
				results <- FileUploadResult{
					IsLivePhoto: isLivePhoto,
					Skipped:     true,
					SkipCode:    skipCode,
					SkipReason:  skipReason,
					Path:        path,
					Paths:       paths,
				}
			} else {
				results <- FileUploadResult{IsLivePhoto: isLivePhoto, Path: path, Paths: paths, MediaKey: mediaKey, Bytes: sumPathSizes(paths), Transferred: transferred}
				m.reporter.ThreadStatus(ThreadStatus{
					WorkerID: workerID,
					Status:   "completed",
					FilePath: path,
					FileName: filepath.Base(path),
					Message:  "Completed",
				})
			}
			cancelUpload()

			// Mark as idle after completing file
			m.reporter.ThreadStatus(ThreadStatus{
				WorkerID: workerID,
				Status:   "idle",
				Message:  "Waiting for next file...",
			})
		}
	}

	// Final idle status when no more work
	m.reporter.ThreadStatus(ThreadStatus{
		WorkerID: workerID,
		Status:   "idle",
		Message:  "Finished",
	})
}

func uploadWorkItem(ctx context.Context, api *Api, item UploadWorkItem, opts UploadOptions, workerID int, reporter UploadReporter) (string, bool, bool, error) {
	switch item.Kind {
	case UploadWorkSingle:
		if item.Single == nil || item.LivePhoto != nil {
			return "", false, false, fmt.Errorf("invalid single-media work item")
		}
		mediaKey, transferred, err := uploadSingleFile(ctx, api, item.Single.Path, opts, workerID, reporter)
		return mediaKey, transferred, false, err
	case UploadWorkLivePhoto:
		if item.LivePhoto == nil || item.Single != nil {
			return "", false, false, fmt.Errorf("invalid Live Photo work item")
		}
		mediaKey, skipped, err := uploadLivePhotoWithCallback(ctx, api, *item.LivePhoto, LivePhotoUploadOptions{
			Policy:                     buildLivePhotoCommitPolicy(api),
			DeleteFromHost:             opts.DeleteFromHost,
			SetDateFromFilename:        opts.SetDateFromFilename,
			UpdateExistingPhotosToLive: opts.UpdateExistingPhotosToLive,
			HashCache:                  opts.HashCache,
		}, workerID, reporter)
		// Transferred is false for skipped pairs; their bytes left the total
		// through a TotalBytesDelta inside the Live Photo upload.
		transferred := err == nil && !skipped && mediaKey != ""
		return mediaKey, transferred, skipped, err
	default:
		return "", false, false, fmt.Errorf("unsupported upload work kind %q", item.Kind)
	}
}

func uploadWorkPaths(item UploadWorkItem) []string {
	if item.Kind == UploadWorkLivePhoto && item.LivePhoto != nil {
		return []string{item.LivePhoto.PhotoPath, item.LivePhoto.VideoPath}
	}
	if item.Single != nil {
		return []string{item.Single.Path}
	}
	return nil
}

func uploadWorkPrimaryPath(item UploadWorkItem) string {
	paths := uploadWorkPaths(item)
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// sumPathSizes stats every path and returns the total size, ignoring failures.
func sumPathSizes(paths []string) int64 {
	var total int64
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil {
			total += info.Size()
		}
	}
	return total
}
