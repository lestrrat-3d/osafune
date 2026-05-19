package ui

import (
	"errors"

	"github.com/sqweek/dialog"
)

// NativeOpener uses the platform-native open-file dialog via sqweek/dialog.
// On WSLg it shells out to zenity, on Windows to GetOpenFileName, on macOS
// to NSOpenPanel — all the things that already work without an extra
// runtime install.
type NativeOpener struct{}

// Pick implements [FileOpener]. It returns ok=false when the user cancels.
func (NativeOpener) Pick() (string, bool) {
	path, err := dialog.File().
		Title("Open mesh").
		Filter("Mesh files", "stl", "3mf").
		Filter("All files", "*").
		Load()
	if err != nil {
		// sqweek returns ErrCancelled for cancel — that's not actually an
		// error from the user's perspective.
		if errors.Is(err, dialog.ErrCancelled) {
			return "", false
		}
		return "", false
	}
	return path, true
}

// NativeSaver opens the platform-native save-file dialog for the
// gcode-out flow.
type NativeSaver struct{}

// PickSave implements [FileSaver]. It returns ok=false when the user
// cancels.
func (NativeSaver) PickSave(defaultName string) (string, bool) {
	path, err := dialog.File().
		Title("Save gcode").
		Filter("Gcode", "gcode").
		Filter("All files", "*").
		SetStartFile(defaultName).
		Save()
	if err != nil {
		if errors.Is(err, dialog.ErrCancelled) {
			return "", false
		}
		return "", false
	}
	return path, true
}
