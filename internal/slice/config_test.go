package slice_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/osafune/internal/config"
)

// defaultProcess and defaultPrinter are the built-in profiles converted to the
// units the slicer works in. Tests tune the resolved form, so a knob is set as
// the plain millimetres or mm/s the pipeline reads rather than as a quantity
// that would only be converted straight back.
func defaultProcess(t *testing.T) config.ResolvedProcess {
	t.Helper()
	p, err := config.DefaultProcess().Resolve()
	require.NoError(t, err)
	return p
}

func defaultPrinter(t *testing.T) config.ResolvedPrinter {
	t.Helper()
	p, err := config.DefaultPrinter().Resolve()
	require.NoError(t, err)
	return p
}
