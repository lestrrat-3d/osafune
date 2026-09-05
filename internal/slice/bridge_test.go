package slice_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/osafune/internal/slice"
)

// rect is a CCW axis-aligned rectangle region.
func rect(x0, y0, x1, y1 float64) slice.ExPolygon {
	return slice.ExPolygon{Outer: slice.Polygon{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}}}
}

func bbox(regions []slice.ExPolygon) (minX, maxX float64) {
	minX, maxX = 1e18, -1e18
	for _, r := range regions {
		for _, p := range r.Outer {
			if p.X < minX {
				minX = p.X
			}
			if p.X > maxX {
				maxX = p.X
			}
		}
	}
	return minX, maxX
}

func TestSplitBridgesSeparatesOverhang(t *testing.T) {
	t.Parallel()
	// Layer 0 supports x∈[0,10]; layer 1's solid extends to x=20, so x∈[10,20]
	// overhangs air and must be classified as bridge.
	layers := []slice.Layer{
		{Index: 0, Contours: []slice.ExPolygon{rect(0, 0, 10, 10)}},
		{Index: 1},
	}
	solid := []slice.ExPolygon{rect(0, 0, 20, 10)}

	bridge, supported := slice.SplitBridges(solid, 1, layers)
	require.NotEmpty(t, bridge, "overhang must be detected as bridge")
	require.NotEmpty(t, supported, "the part over the layer below stays solid")

	bMin, bMax := bbox(bridge)
	sMin, sMax := bbox(supported)
	require.InDelta(t, 10, bMin, 0.5, "bridge starts at the support edge")
	require.InDelta(t, 20, bMax, 0.5, "bridge reaches the overhang edge")
	require.InDelta(t, 0, sMin, 0.5, "supported solid starts at the object edge")
	require.InDelta(t, 10, sMax, 0.5, "supported solid ends at the support edge")
}

func TestSplitBridgesFirstLayerNeverBridges(t *testing.T) {
	t.Parallel()
	layers := []slice.Layer{{Index: 0}}
	solid := []slice.ExPolygon{rect(0, 0, 20, 10)}
	bridge, supported := slice.SplitBridges(solid, 0, layers)
	require.Empty(t, bridge, "first layer rests on the bed, never bridges")
	require.Equal(t, solid, supported)
}

func TestGenerateBridgesTagsAndSpeeds(t *testing.T) {
	t.Parallel()
	proc := defaultProcess(t)
	proc.BridgeSpeed = 22
	proc.BridgeFlow = 0.9
	layer := slice.Layer{Index: 5}
	slice.GenerateBridges(&layer, []slice.ExPolygon{rect(0, 0, 20, 4)}, &proc)

	require.NotEmpty(t, layer.Paths, "bridge region must produce fill strands")
	for _, p := range layer.Paths {
		require.Equal(t, slice.RoleBridge, p.Role)
		require.Equal(t, 22.0, p.Speed, "bridge strands print at bridge speed")
		require.InDelta(t, proc.LineWidth*0.9, p.Width, 1e-9, "width scaled by bridge flow")
	}
}
