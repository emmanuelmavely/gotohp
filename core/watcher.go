package core

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch folder behavior: new and changed media files under the watched roots
// are collected with a debounce, then handed to onFiles as one batch. On
// start, a full rescan catches everything that changed while the app was
// off: a file whose size and mtime are still in the hash cache is unchanged
// and therefore skipped.
const (
	// watchQuietPeriod is how long a path must stay silent before it is
	// considered finished copying.
	watchQuietPeriod = 30 * time.Second
	// watchSweepInterval is how often pending paths are checked.
	watchSweepInterval = 5 * time.Second
)

// watcherPaths filters out files that are still being written or are not
// media at all.
func watcherPathWanted(path string, info os.FileInfo) bool {
	if info != nil && info.IsDir() {
		return false
	}
	name := filepath.Base(path)
	lower := strings.ToLower(name)
	switch lower {
	case "", "desktop.ini", "thumbs.db":
		return false
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~$") {
		return false
	}
	for _, suffix := range []string{".tmp", ".temp", ".partial", ".crdownload", ".download", ".part", ".swp", ".bak"} {
		if strings.HasSuffix(lower, suffix) {
			return false
		}
	}
	return isSupportedByGooglePhotos(name)
}

// WatchManager watches configured folders for new media files.
type WatchManager struct {
	mu      sync.Mutex
	folders []string
	cancel  chan struct{}
	stopped bool

	// onFiles receives one batch of finished new files. It is called from a
	// watcher goroutine and must not block for long.
	onFiles func(paths []string)
	// hashCache decides whether a rescan sees a file as new: a cache hit
	// with matching size and mtime means the file is unchanged.
	hashCache *HashCache
	logger    *slog.Logger
}

func NewWatchManager(hashCache *HashCache, onFiles func(paths []string), logger *slog.Logger) *WatchManager {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if onFiles == nil {
		onFiles = func([]string) {}
	}
	return &WatchManager{
		hashCache: hashCache,
		onFiles:   onFiles,
		logger:    logger,
	}
}

// Folders returns the currently watched roots.
func (w *WatchManager) Folders() []string {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.folders...)
}

// Start begins watching the given roots. A previous watch is stopped first.
// The full startup rescan runs in the background.
func (w *WatchManager) Start(folders []string) error {
	if w == nil {
		return nil
	}
	w.Stop()

	cleaned := make([]string, 0, len(folders))
	for _, folder := range folders {
		folder = filepath.Clean(folder)
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			continue
		}
		cleaned = append(cleaned, folder)
	}
	if len(cleaned) == 0 {
		return nil
	}

	w.mu.Lock()
	if w.stopped {
		// Stop marks the manager permanently unusable only after Close; a
		// restart simply resets the flag.
		w.stopped = false
	}
	w.folders = cleaned
	cancel := make(chan struct{})
	w.cancel = cancel
	w.mu.Unlock()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	for _, folder := range cleaned {
		if err := addRecursiveWatch(watcher, folder); err != nil {
			w.logger.Warn("watch: partial watch setup", "folder", folder, "error", err)
		}
	}

	go w.run(watcher, cleaned, cancel)
	go w.rescan(cleaned, cancel)
	return nil
}

// Stop ends watching. Safe to call multiple times.
func (w *WatchManager) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.cancel == nil || w.stopped {
		w.mu.Unlock()
		return
	}
	close(w.cancel)
	w.stopped = true
	w.cancel = nil
	w.mu.Unlock()
}

func (w *WatchManager) isCancelled(cancel chan struct{}) bool {
	select {
	case <-cancel:
		return true
	default:
		return false
	}
}

// addRecursiveWatch adds the directory and all subdirectories to the watcher.
func addRecursiveWatch(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if !entry.IsDir() {
			return nil
		}
		return watcher.Add(path)
	})
}

// run consumes fsnotify events until cancel fires.
func (w *WatchManager) run(watcher *fsnotify.Watcher, roots []string, cancel chan struct{}) {
	defer func() { _ = watcher.Close() }()

	pending := make(map[string]time.Time)
	var pendingMu sync.Mutex
	ticker := time.NewTicker(watchSweepInterval)
	defer ticker.Stop()

	addPending := func(path string) {
		pendingMu.Lock()
		pending[path] = time.Now()
		pendingMu.Unlock()
	}

	for {
		select {
		case <-cancel:
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Rename) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					// New directory: watch it so its files show up too.
					if err := addRecursiveWatch(watcher, event.Name); err != nil {
						w.logger.Debug("watch: could not watch new directory", "path", event.Name, "error", err)
					}
					continue
				}
				if !watcherPathWanted(event.Name, nil) {
					continue
				}
				addPending(event.Name)
			} else if event.Has(fsnotify.Remove) {
				// Best effort: drop watches under removed directories.
				_ = watcher.Remove(event.Name)
				pendingMu.Lock()
				delete(pending, event.Name)
				pendingMu.Unlock()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			w.logger.Warn("watch error", "error", err)
		case now := <-ticker.C:
			var ready []string
			pendingMu.Lock()
			for path, lastEvent := range pending {
				if now.Sub(lastEvent) >= watchQuietPeriod {
					ready = append(ready, path)
					delete(pending, path)
				}
			}
			pendingMu.Unlock()
			if len(ready) > 0 {
				w.dispatch(ready)
			}
		}
	}
}

// WatchFilesFoundEvent tells the UI that the watch folder found new files.
type WatchFilesFoundEvent struct {
	Count int `json:"Count"`
}

// dispatch filters the batch down to files that are still wanted and hands
// them to onFiles.
func (w *WatchManager) dispatch(paths []string) {
	ready := make([]string, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !watcherPathWanted(path, info) {
			continue
		}
		ready = append(ready, path)
	}
	if len(ready) == 0 {
		return
	}
	w.logger.Info("watch: new files", "count", len(ready))
	w.onFiles(ready)
}

// rescan performs the startup sweep: every supported file whose size and
// mtime are not already in the hash cache is new or changed and gets
// uploaded.
func (w *WatchManager) rescan(roots []string, cancel chan struct{}) {
	var found []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || w.isCancelled(cancel) {
				return filepath.SkipAll
			}
			if entry.IsDir() {
				return nil
			}
			if !watcherPathWanted(path, nil) {
				return nil
			}
			if w.hashCache != nil {
				if _, cached := w.hashCache.Get(path); cached {
					return nil
				}
			}
			found = append(found, path)
			return nil
		})
		if err != nil {
			w.logger.Warn("watch rescan failed", "root", root, "error", err)
		}
	}
	if w.isCancelled(cancel) || len(found) == 0 {
		return
	}
	w.logger.Info("watch rescan: new files", "count", len(found))
	w.dispatch(found)
}
