package slice_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-go/makislicer/internal/slice"
)

// square returns a CCW axis-aligned square of side s with its lower-left
// corner at (x, y).
func square(x, y, s float64) slice.ExPolygon {
	return slice.ExPolygon{Outer: slice.Polygon{
		{X: x, Y: y},
		{X: x + s, Y: y},
		{X: x + s, Y: y + s},
		{X: x, Y: y + s},
	}}
}

// totalArea sums the net area (outer minus holes) of every region in a
// layer.
func totalArea(regions []slice.ExPolygon) float64 {
	var a float64
	for _, r := range regions {
		a += r.Outer.Area()
		for _, h := range r.Holes {
			a -= h.Area()
		}
	}
	return a
}

func TestClassifySkin_ConstantCrossSection(t *testing.T) {
	t.Parallel()
	// Five identical layers — a column. With a 2-layer top and bottom
	// shell only the first two and last two layers should be solid; the
	// middle layer is fully enclosed and stays sparse.
	areas := make([][]slice.ExPolygon, 5)
	for i := range areas {
		areas[i] = []slice.ExPolygon{square(0, 0, 10)}
	}

	solid, sparse := slice.ClassifySkin(areas, 2, 2, 0)

	for i := range areas {
		wantSolid := i == 0 || i == 1 || i == 3 || i == 4
		if wantSolid {
			require.InDelta(t, 100.0, totalArea(solid[i]), 1e-6, "layer %d should be fully solid", i)
			require.Empty(t, sparse[i], "layer %d should have no sparse area", i)
			continue
		}
		require.Empty(t, solid[i], "layer %d (enclosed) should have no solid skin", i)
		require.InDelta(t, 100.0, totalArea(sparse[i]), 1e-6, "layer %d should be fully sparse", i)
	}
}

func TestClassifySkin_FiltersThinSlivers(t *testing.T) {
	t.Parallel()
	// A nominally vertical column whose middle layer is inset by a tiny
	// 0.03 mm — the kind of difference contour quantization produces. The
	// exposed diff is a sub-bead sliver frame (mean width ≈ 0.03 mm) that
	// must NOT become solid: a 0.08 mm min-width filter drops it so the
	// enclosed middle layer stays fully sparse.
	full := []slice.ExPolygon{square(0, 0, 10)}
	inset := []slice.ExPolygon{square(0.03, 0.03, 10-0.06)} // 9.94 mm, ~99.88 % of area
	areas := [][]slice.ExPolygon{full, inset, full, inset, full}

	// With no filter the sliver leaks through as solid skin.
	rawSolid, _ := slice.ClassifySkin(areas, 1, 1, 0)
	require.Greater(t, totalArea(rawSolid[2]), 0.0, "no filter: sliver leaks as solid")

	// With the min-width filter the enclosed layer is clean.
	solid, sparse := slice.ClassifySkin(areas, 1, 1, 0.08)
	require.Empty(t, solid[2], "filtered: enclosed layer has no spurious solid")
	require.InDelta(t, totalArea(areas[2]), totalArea(sparse[2]), 1e-6,
		"filtered: enclosed layer is fully sparse")
}

func TestClassifySkin_NarrowingPartGetsRoof(t *testing.T) {
	t.Parallel()
	// A 10x10 base for two layers, then a 4x4 column on top. Where the
	// part narrows (layer 1), the shoulder that loses material above it
	// must become solid skin — the geometric top detection the naive
	// index rule could never do.
	base := []slice.ExPolygon{square(0, 0, 10)}  // area 100
	column := []slice.ExPolygon{square(3, 3, 4)} // area 16, centred on the base
	areas := [][]slice.ExPolygon{base, base, column}

	solid, sparse := slice.ClassifySkin(areas, 1, 1, 0)

	// Layer 1 shoulder: 100 - 16 = 84 of solid roof, 16 of sparse interior.
	require.InDelta(t, 84.0, totalArea(solid[1]), 1e-3, "shoulder roof area")
	require.InDelta(t, 16.0, totalArea(sparse[1]), 1e-3, "interior under the column")

	// The column layer itself is the very top, so it is fully solid.
	require.InDelta(t, 16.0, totalArea(solid[2]), 1e-3, "top of column is solid")
	require.Empty(t, sparse[2])
}
