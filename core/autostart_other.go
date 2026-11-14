//go:build !windows

package core

// SetAutostart is only implemented on Windows; other platforms are a no-op.
func SetAutostart(bool) error { return nil }

// IsAutostartEnabled reports whether the autostart entry exists.
func IsAutostartEnabled() bool { return false }
