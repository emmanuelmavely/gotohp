//go:build windows

package core

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

// Autostart registers the GUI in the user's Run registry key so Windows
// starts gotohp at login with the --hidden flag (starts to the tray).
const (
	autostartKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	autostartValue   = "gotohp"
	autostartFlag    = "--hidden"
)

// SetAutostart adds or removes the autostart registry entry.
func SetAutostart(enabled bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, autostartKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open autostart registry key: %w", err)
	}
	defer func() { _ = key.Close() }()

	if !enabled {
		if err := key.DeleteValue(autostartValue); err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("remove autostart entry: %w", err)
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable for autostart: %w", err)
	}
	command := fmt.Sprintf(`"%s" %s`, exe, autostartFlag)
	if err := key.SetStringValue(autostartValue, command); err != nil {
		return fmt.Errorf("write autostart entry: %w", err)
	}
	return nil
}

// IsAutostartEnabled reports whether the autostart entry exists.
func IsAutostartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, autostartKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer func() { _ = key.Close() }()
	value, _, err := key.GetStringValue(autostartValue)
	return err == nil && value != ""
}
