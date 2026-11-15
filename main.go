//go:build !cli

package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"app/core"
	"app/internal/cli"
	"app/internal/gui"
	"app/internal/preview"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var trayIcon []byte

var title = "gotohp v" + getAppVersion()

func main() {
	// Run the CLI when the arguments name one of its commands; otherwise start the GUI.
	if cli.IsCLIInvocation(os.Args[1:]) {
		build := getBuildInfo()
		os.Exit(cli.Run(os.Args[1:], cli.Info{
			ExecutableName: "gotohp",
			HasGUI:         true,
			Version:        getAppVersion(),
			Commit:         build.Commit,
			BuildDate:      build.Date,
		}))
	}

	runGUI()
}

func runGUI() {
	normalizeFrontendDevServerURL()

	// --hidden starts minimized to the tray (used by the autostart entry).
	hidden := isHiddenStart(os.Args)

	configManager := &core.ConfigManager{}
	dialogService := gui.NewDialogService()
	libraryService := gui.NewLibraryService()
	build := getBuildInfo()
	appService := gui.NewAppService(gui.AppInfo{
		Version:   getAppVersion(),
		Commit:    build.Commit,
		BuildDate: build.Date,
	}, getChangelog())
	previews := preview.New()
	wailsApp := application.New(application.Options{
		Name:        "com.xob0t.gotohp",
		Description: "Google Photos unofficial client",
		Services: []application.Service{
			application.NewService(configManager),
			application.NewService(dialogService),
			application.NewService(libraryService),
			application.NewService(appService),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
			// Serves the files of the running upload so the UI can draw thumbnails.
			Middleware: previews.Middleware,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	dialogService.SetApp(wailsApp)
	// Library scans report their running totals so the window can show them live.
	gui.SetLibrarySyncProgress(libraryService, func(stats core.LibrarySyncStats) {
		wailsApp.Event.Emit("librarySyncProgress", stats)
	})
	// Live Photo repairs report each pair's stages and can be cancelled.
	gui.SetLivePhotoRepairProgress(libraryService, func(progress core.LivePhotoRepairProgress) {
		wailsApp.Event.Emit("livePhotoRepairProgress", progress)
	})
	wailsApp.Event.On("livePhotoRepairCancel", func(*application.CustomEvent) {
		gui.CancelLivePhotoRepair(libraryService)
	})

	window := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:          title,
		Frameless:      false,
		Width:          480,
		Height:         720,
		MinWidth:       400,
		MinHeight:      560,
		EnableFileDrop: true,
		Hidden:         hidden,
		BackgroundType: application.BackgroundTypeTranslucent,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 0,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		URL: "/",
	})

	reporter := previewReporter{UploadReporter: gui.NewWailsReporter(wailsApp), previews: previews}
	uploadManager := core.NewUploadManager(reporter, wailsApp.Logger)
	// The GUI pauses uploads on network loss and resumes them automatically.
	uploadManager.SetAutoResume(true)

	// Offer to resume an interrupted upload run from the previous session.
	if summary := core.PendingUploadJournal(); summary.Exists {
		wailsApp.Event.Emit("resumePending", summary)
	}

	// Watch folders: new media files upload automatically (Dropbox-style).
	settings := configManager.GetSettings()
	watchManager := core.NewWatchManager(uploadManager.HashCache(), watchUpload(uploadManager, configManager, wailsApp), wailsApp.Logger)
	if settings.WatchEnabled && len(settings.WatchFolders) > 0 {
		if err := watchManager.Start(settings.WatchFolders); err != nil {
			wailsApp.Logger.Error("watch folder setup failed", "error", err)
		}
	}

	// Closing the window does not end the app on its own: the window asks
	// whether to keep running in the tray or to quit (see closeChoice below).
	// Watch folders and uploads keep going while the window is hidden.
	var quitting, closePending atomic.Bool
	showWindow := func() {
		closePending.Store(false)
		window.Show()
		window.Focus()
	}
	quitApp := func() {
		quitting.Store(true)
		wailsApp.Quit()
	}
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting.Load() {
			return
		}
		e.Cancel()
		// A second close while the question is still open (or the page is not
		// responding) hides to the tray, so the window can always be dismissed.
		if closePending.Swap(true) {
			closePending.Store(false)
			window.Hide()
			return
		}
		wailsApp.Event.Emit("closeRequested", nil)
	})
	wailsApp.Event.On("closeChoice", func(e *application.CustomEvent) {
		closePending.Store(false)
		switch choice, _ := e.Data.(string); choice {
		case "quit":
			quitApp()
		case "tray":
			window.Hide()
		}
		// Anything else (the question was dismissed) leaves the window open.
	})

	// Tray icon: reopens the window while the app runs hidden, and quits it.
	setupTray(wailsApp, title, showWindow, quitApp)

	// Listen for upload cancel event
	wailsApp.Event.On("uploadCancel", func(e *application.CustomEvent) {
		uploadManager.Cancel()
	})

	// Resume / discard a pending upload journal from the previous session
	wailsApp.Event.On("resumeUpload", func(e *application.CustomEvent) {
		if !uploadManager.Resume() {
			wailsApp.Logger.Warn("resumeUpload: nothing to resume")
		}
	})
	wailsApp.Event.On("discardUpload", func(e *application.CustomEvent) {
		core.DiscardUploadJournal()
	})

	// Restart or stop watching when the watch settings change
	wailsApp.Event.On("watchSettingsChanged", func(e *application.CustomEvent) {
		s := configManager.GetSettings()
		if s.WatchEnabled && len(s.WatchFolders) > 0 {
			if err := watchManager.Start(s.WatchFolders); err != nil {
				wailsApp.Logger.Error("watch folder setup failed", "error", err)
			}
		} else {
			watchManager.Stop()
		}
	})

	window.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		paths := event.Context().DroppedFiles()
		dropTarget := event.Context().DropTargetDetails()

		var dropZone string
		if dropTarget != nil {
			dropZone = dropTarget.Attributes["data-drop-zone"]
			wailsApp.Logger.Info("Drop target detected",
				"dropZone", dropZone,
				"elementID", dropTarget.ElementID)
		}

		// Emit event to frontend with drop details
		wailsApp.Event.Emit("files-dropped", core.FilesDroppedEvent{
			Files:    paths,
			DropZone: dropZone,
		})
	})

	// Listen for upload request from frontend (after drop zone is determined)
	wailsApp.Event.On("startUpload", func(e *application.CustomEvent) {
		if data, ok := e.Data.(core.StartUploadEvent); ok {
			wailsApp.Logger.Info("Starting upload", "fileCount", len(data.Files))
			uploadManager.Upload(data.Files, configManager.SessionUploadOptions())
		} else {
			wailsApp.Logger.Error("startUpload: unexpected data type", "type", fmt.Sprintf("%T", e.Data))
		}
	})

	err := wailsApp.Run()
	if err != nil {
		log.Fatal(err)
	}
}

// isHiddenStart reports whether the process was started with --hidden,
// which makes the GUI start minimized to the tray (autostart mode).
func isHiddenStart(args []string) bool {
	for _, arg := range args {
		if arg == "--hidden" || arg == "-hidden" {
			return true
		}
	}
	return false
}

// watchUpload uploads a batch of watched files once no upload is running.
// It runs on a watcher goroutine and blocks until the batch is started.
func watchUpload(uploadManager *core.UploadManager, configManager *core.ConfigManager, wailsApp *application.App) func(paths []string) {
	return func(paths []string) {
		// Let a running upload finish so the watched batch starts cleanly.
		for uploadManager.IsRunning() {
			time.Sleep(2 * time.Second)
		}
		wailsApp.Event.Emit("watchFilesFound", core.WatchFilesFoundEvent{
			Count: len(paths),
		})
		uploadManager.Upload(paths, configManager.SessionUploadOptions())
	}
}

// setupTray shows a tray icon so a hidden session (closed to the tray, or
// started with --hidden by autostart) can be reached again. Clicking the icon
// opens the window; the menu offers open and quit.
func setupTray(app *application.App, tooltip string, show func(), quit func()) {
	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip(tooltip)
	tray.OnClick(show)

	menu := app.NewMenu()
	menu.Add("Open gotohp").OnClick(func(*application.Context) {
		show()
	})
	menu.Add("Quit").OnClick(func(*application.Context) {
		quit()
	})
	tray.SetMenu(menu)
}

// previewReporter lets the UI preview each file an upload worker picks up.
type previewReporter struct {
	core.UploadReporter
	previews *preview.Server
}

func (r previewReporter) ThreadStatus(s core.ThreadStatus) {
	r.previews.Allow(s.FilePath)
	r.UploadReporter.ThreadStatus(s)
}

func normalizeFrontendDevServerURL() {
	const envName = "FRONTEND_DEVSERVER_URL"

	value := os.Getenv(envName)
	value = strings.Replace(value, "http://localhost:", "http://127.0.0.1:", 1)
	value = strings.Replace(value, "https://localhost:", "https://127.0.0.1:", 1)
	if value != os.Getenv(envName) {
		_ = os.Setenv(envName, value)
	}
}
