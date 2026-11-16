//go:build !cli

package gui

import "github.com/wailsapp/wails/v3/pkg/application"

// DialogService exposes native dialogs to the frontend. GUI-only: the CLI
// never needs it. The app handle is set right after application.New because
// services are constructed inside its options.
type DialogService struct {
	app *application.App
}

func NewDialogService() *DialogService {
	return &DialogService{}
}

func (s *DialogService) SetApp(app *application.App) {
	s.app = app
}

// PickFolder opens a native directory picker and returns the chosen folder.
func (s *DialogService) PickFolder() (string, error) {
	dialog := s.app.Dialog.OpenFile()
	dialog.CanChooseDirectories(true)
	dialog.CanChooseFiles(false)
	dialog.CanCreateDirectories(false)
	dialog.SetTitle("Choose a folder to watch")
	return dialog.PromptForSingleSelection()
}

// PickDownloadFolder opens a directory picker for where downloaded originals
// are saved.
func (s *DialogService) PickDownloadFolder() (string, error) {
	dialog := s.app.Dialog.OpenFile()
	dialog.CanChooseDirectories(true)
	dialog.CanChooseFiles(false)
	dialog.CanCreateDirectories(true)
	dialog.SetTitle("Choose where to save the downloaded originals")
	return dialog.PromptForSingleSelection()
}

// PickPhotosFolder opens a directory picker for a folder that holds original
// photos and videos (used to find local copies of counted files).
func (s *DialogService) PickPhotosFolder() (string, error) {
	dialog := s.app.Dialog.OpenFile()
	dialog.CanChooseDirectories(true)
	dialog.CanChooseFiles(false)
	dialog.CanCreateDirectories(false)
	dialog.SetTitle("Choose a folder with your original photos and videos")
	return dialog.PromptForSingleSelection()
}
