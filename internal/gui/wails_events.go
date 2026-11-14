//go:build !cli

package gui

import (
	"app/core"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func init() {
	application.RegisterEvent[core.AlbumStatus]("albumProgress")
	application.RegisterEvent[core.AlbumStatus]("albumComplete")
	application.RegisterEvent[core.AlbumError]("albumError")
	application.RegisterEvent[core.UploadBatchStart]("uploadStart")
	application.RegisterEvent[application.Void]("uploadStop")
	application.RegisterEvent[core.FileUploadResult]("FileStatus")
	application.RegisterEvent[core.ThreadStatus]("ThreadStatus")
	application.RegisterEvent[core.PreflightWarning]("uploadWarning")
	application.RegisterEvent[application.Void]("uploadPaused")
	application.RegisterEvent[int64]("uploadDiskRead")
	application.RegisterEvent[application.Void]("uploadCancel")
	application.RegisterEvent[int64]("uploadTotalBytes")
	application.RegisterEvent[int64]("uploadTotalBytesDelta")
	application.RegisterEvent[core.FilesDroppedEvent]("files-dropped")
	application.RegisterEvent[core.StartUploadEvent]("startUpload")
	application.RegisterEvent[core.UploadJournalSummary]("resumePending")
	application.RegisterEvent[application.Void]("resumeUpload")
	application.RegisterEvent[application.Void]("discardUpload")
	application.RegisterEvent[core.WatchFilesFoundEvent]("watchFilesFound")
	application.RegisterEvent[application.Void]("watchSettingsChanged")
	application.RegisterEvent[core.LibrarySyncStats]("librarySyncProgress")
	application.RegisterEvent[application.Void]("closeRequested")
	application.RegisterEvent[string]("closeChoice")
	application.RegisterEvent[core.LivePhotoRepairProgress]("livePhotoRepairProgress")
	application.RegisterEvent[application.Void]("livePhotoRepairCancel")
}
