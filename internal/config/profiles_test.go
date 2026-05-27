package config_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-go/makislicer/internal/config"
)

func TestProfileRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate from the real config dir

	p := config.DefaultProcess()
	p.Name = "My Fast"
	p.LayerHeight = 0.28
	p.InfillDensity = 0.42
	require.NoError(t, config.SaveProfile(config.KindProcess, p.Name, p))

	got, err := config.LoadProfile[config.Process](config.KindProcess, p.Name)
	require.NoError(t, err)
	require.Equal(t, "My Fast", got.Name)
	require.Equal(t, 0.28, got.LayerHeight)
	require.Equal(t, 0.42, got.InfillDensity)

	names, err := config.ListProfiles(config.KindProcess)
	require.NoError(t, err)
	require.Contains(t, names, "My Fast")
}

func TestEnsureDefaultProfiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	names, err := config.ListProfiles(config.KindPrinter)
	require.NoError(t, err, "a missing profile dir is empty, not an error")
	require.Empty(t, names)
}
