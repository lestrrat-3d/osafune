package gcode_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/osafune/internal/config"
)

// defaultPrinter, defaultFilament and defaultProcess are the built-in profiles
// converted to the units the emitter works in. Tests tune the resolved form, so
// a knob is set as the plain millimetres, mm/s or whole degrees the emitter
// reads rather than as a quantity that would only be converted straight back.
func defaultPrinter(t *testing.T) config.ResolvedPrinter {
	t.Helper()
	p, err := config.DefaultPrinter().Resolve()
	require.NoError(t, err)
	return p
}

func defaultFilament(t *testing.T) config.ResolvedFilament {
	t.Helper()
	f, err := config.DefaultFilament().Resolve()
	require.NoError(t, err)
	return f
}

func defaultProcess(t *testing.T) config.ResolvedProcess {
	t.Helper()
	p, err := config.DefaultProcess().Resolve()
	require.NoError(t, err)
	return p
}
