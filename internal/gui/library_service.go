//go:build !cli

package gui

import (
	"context"
	"fmt"
	"sync"

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

// AccountStorage refreshes the library index and summarizes how much storage
// the account uses.
func (s *LibraryService) AccountStorage() (core.AccountStorage, error) {
	api, err := apiFor()
	if err != nil {
		return core.AccountStorage{}, err
	}
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
	cached, _ := core.LoadLibraryIndex()
	index, err := api.SyncLibraryIncremental(cached, s.reportSyncProgress)
	if err != nil {
		return core.LivePhotoRepairReport{}, err
	}
	if err := index.Save(); err != nil {
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
	cached, _ := core.LoadLibraryIndex()
	index, err := api.SyncLibraryIncremental(cached, s.reportSyncProgress)
	if err != nil {
		return core.BinReport{}, err
	}
	if err := index.Save(); err != nil {
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
	cached, _ := core.LoadLibraryIndex()
	index, err := api.SyncLibraryIncremental(cached, s.reportSyncProgress)
	if err != nil {
		return nil, err
	}
	if err := index.Save(); err != nil {
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
