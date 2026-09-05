package gcode_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/osafune/internal/config"
	"github.com/lestrrat-3d/osafune/internal/slice"
)

// twoFarPaths is a single layer at Z=0.2 with two far-apart paths, forcing a
// long retracted travel between them (where z-hop applies).
func twoFarPaths() []slice.Layer {
	return []slice.Layer{{
		Index: 0, Z: 0.2, Height: 0.2,
		Paths: []slice.Path{
			wall(slice.RoleExternalPerimeter, slice.Point2{X: 0, Y: 0}, slice.Point2{X: 10, Y: 0}),
			wall(slice.RoleExternalPerimeter, slice.Point2{X: 50, Y: 50}, slice.Point2{X: 60, Y: 50}),
		},
	}}
}

func TestZHopOnLongTravel(t *testing.T) {
	t.Parallel()
	fil := config.DefaultFilament() // ZHop 0.4
	out := emit(t, config.DefaultPrinter(), fil, config.DefaultProcess(), twoFarPaths())
	body := printBody(out)

	require.Contains(t, body, "G1 Z0.600 ; z-hop", "travel must lift to layerZ+ZHop (0.2+0.4)")
	// The lift must sit between the retract and the unretract, and drop back
	// to the layer height before priming.
	hop := strings.Index(body, "; z-hop")
	down := strings.Index(body, "G1 Z0.200\n")
	unretract := strings.Index(body, " ; unretract")
	require.GreaterOrEqual(t, hop, 0)
	require.Greater(t, down, hop, "z-hop drops back down after the travel")
	require.Greater(t, unretract, down, "unretract happens after lowering")
}

func TestZHopDisabled(t *testing.T) {
	t.Parallel()
	fil := config.DefaultFilament()
	fil.ZHop = 0
	out := emit(t, config.DefaultPrinter(), fil, config.DefaultProcess(), twoFarPaths())
	require.NotContains(t, printBody(out), "; z-hop", "z-hop disabled when ZHop<=0")
}
