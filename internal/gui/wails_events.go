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
	application.RegisterEvent[application.Void]("uploadPause")
	application.RegisterEvent[application.Void]("uploadResumePaused")
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
	application.RegisterEvent[core.QuotaScanProgress]("quotaScanProgress")
	application.RegisterEvent[core.QuotaConvertProgress]("quotaConvertProgress")
	application.RegisterEvent[core.QuotaDownloadProgress]("quotaDownloadProgress")
	application.RegisterEvent[core.TakeawayProgress]("takeawayProgress")
	application.RegisterEvent[application.Void]("takeawayCancel")
	application.RegisterEvent[application.Void]("quotaConvertCancel")
}
