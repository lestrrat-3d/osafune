package config_test

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/units"

	"github.com/lestrrat-3d/osafune/internal/config"
)

// isolateConfigDir points os.UserConfigDir at a temp directory, on every
// platform this is tested on. Go reads a different variable on each —
// XDG_CONFIG_HOME on Linux and the BSDs, HOME on macOS, AppData on Windows —
// so setting only the Linux one leaves the other two reading, and
// EnsureDefaultProfiles writing, the developer's real config directory. That is
// a test that fails for the wrong reason and a side effect besides.
func isolateConfigDir(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", dir)
	case "darwin":
		t.Setenv("HOME", dir)
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
}

func TestProfileRoundTrip(t *testing.T) {
	isolateConfigDir(t)

	p := config.DefaultProcess()
	p.Name = "My Fast"
	p.LayerHeight = units.Millimeters(0.28)
	p.InfillDensity = 0.42
	require.NoError(t, config.SaveProfile(config.KindProcess, p.Name, p))

	got, err := config.LoadProfile[config.Process](config.KindProcess, p.Name)
	require.NoError(t, err)
	require.Equal(t, "My Fast", got.Name)
	require.Equal(t, units.Millimeters(0.28), got.LayerHeight)
	require.Equal(t, 0.42, got.InfillDensity)

	names, err := config.ListProfiles(config.KindProcess)
	require.NoError(t, err)
	require.Contains(t, names, "My Fast")
}

func TestEnsureDefaultProfiles(t *testing.T) {
	isolateConfigDir(t)

	require.NoError(t, config.EnsureDefaultProfiles())
	for _, kind := range []string{config.KindPrinter, config.KindFilament, config.KindProcess} {
		names, err := config.ListProfiles(kind)
		require.NoError(t, err)
		require.NotEmpty(t, names, "kind %s should be seeded", kind)
	}
	// Idempotent — a second run neither errors nor clobbers.
	require.NoError(t, config.EnsureDefaultProfiles())
}

func TestListProfilesMissingDirIsEmpty(t *testing.T) {
	isolateConfigDir(t)
	names, err := config.ListProfiles(config.KindPrinter)
	require.NoError(t, err, "a missing profile dir is empty, not an error")
	require.Empty(t, names)
}
