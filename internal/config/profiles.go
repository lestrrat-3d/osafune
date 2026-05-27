package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Profile kinds — the subdirectories under the user profile directory that
// hold the three families of settings as individual JSON files.
const (
	KindPrinter  = "printers"
	KindFilament = "filaments"
	KindProcess  = "processes"
)

// ProfileDir returns the per-user directory where on-disk profiles live
// (e.g. ~/.config/makislicer on Linux), creating nothing. It mirrors
// OrcaSlicer's notion of a user config store, scaled down to plain JSON.
func ProfileDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "makislicer"), nil
}

// SaveProfile writes v as <ProfileDir>/<kind>/<name>.json, creating the
// directory as needed. name is sanitised to a bare file stem.
func SaveProfile(kind, name string, v any) error {
	dir, err := ProfileDir()
	if err != nil {
		return err
	}
	kindDir := filepath.Join(dir, kind)
	if err := os.MkdirAll(kindDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(kindDir, profileStem(name)+".json"), data, 0o644)
}

// LoadProfile reads <ProfileDir>/<kind>/<name>.json into a T.
func LoadProfile[T any](kind, name string) (T, error) {
	var v T
	dir, err := ProfileDir()
	if err != nil {
		return v, err
	}
	data, err := os.ReadFile(filepath.Join(dir, kind, profileStem(name)+".json"))
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(data, &v)
	return v, err
}

// ListProfiles returns the available profile names (file stems) for a kind,
// sorted. A missing directory yields an empty list, not an error.
func ListProfiles(kind string) ([]string, error) {
	dir, err := ProfileDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, kind))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(names)
	return names, nil
}

// EnsureDefaultProfiles writes the built-in Default{Printer,Filament,Process}
// to disk the first time the app runs, so the profile directory is never
// empty and users have a starting point to copy and edit. Existing files are
// left untouched.
func EnsureDefaultProfiles() error {
	seed := []struct {
		kind string
		name string
		v    any
	}{
		{KindPrinter, DefaultPrinter().Name, DefaultPrinter()},
		{KindFilament, DefaultFilament().Name, DefaultFilament()},
		{KindProcess, DefaultProcess().Name, DefaultProcess()},
	}
	for _, s := range seed {
		dir, err := ProfileDir()
		if err != nil {
			return err
		}
		path := filepath.Join(dir, s.kind, profileStem(s.name)+".json")
		if _, err := os.Stat(path); err == nil {
			continue // already present — don't clobber user edits
		}
		if err := SaveProfile(s.kind, s.name, s.v); err != nil {
			return fmt.Errorf("seed %s/%s: %w", s.kind, s.name, err)
		}
	}
	return nil
}

// profileStem reduces a profile name to a safe file stem (path separators and
// surrounding whitespace removed) so a name can double as a filename.
func profileStem(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, string(filepath.Separator), "_")
	name = strings.ReplaceAll(name, "/", "_")
	if name == "" {
		return "profile"
	}
	return name
}
