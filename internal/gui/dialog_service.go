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
