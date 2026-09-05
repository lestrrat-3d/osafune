package slice_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/osafune/internal/slice"
)

func TestOffsetExPolygon_ShrinksSquare(t *testing.T) {
	t.Parallel()
	// A 10x10 square shrunk inward by 1 on every side becomes an 8x8
	// square (area 64). The naive edge offsetter got this right too; this
	// guards the sign convention through the polyclip bridge.
	out := slice.OffsetExPolygon(square(0, 0, 10), 1)
	require.Len(t, out, 1)
	require.InDelta(t, 64.0, out[0].Outer.Area(), 1e-3)
	bb := out[0].Outer.BoundingBox()
	lo, hi := bb.Min, bb.Max
	require.InDelta(t, 1.0, lo.X, 1e-6)
	require.InDelta(t, 1.0, lo.Y, 1e-6)
	require.InDelta(t, 9.0, hi.X, 1e-6)
	require.InDelta(t, 9.0, hi.Y, 1e-6)
}

func TestOffsetExPolygon_OvershrinkCollapses(t *testing.T) {
	t.Parallel()
	// Shrinking a 10x10 square by 6 (more than its half-width) erodes it
	// to nothing. The naive offsetter produced an inside-out bow-tie here;
	// polyclip correctly drops the collapsed piece.
	out := slice.OffsetExPolygon(square(0, 0, 10), 6)
	require.Empty(t, out)
}

func TestOffsetExPolygon_NeckSplitsInTwo(t *testing.T) {
	t.Parallel()
	// A dumbbell: two 10-wide lobes joined by a 2-wide neck. Shrinking
	// inward by 2 pinches the neck shut, splitting the region into two
	// disjoint pieces — a topology change the naive offsetter could not
	// represent (it returned a single self-intersecting polygon).
	dumbbell := slice.ExPolygon{Outer: slice.Polygon{
		{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 4}, // left lobe bottom
		{X: 16, Y: 4}, {X: 16, Y: 0}, {X: 26, Y: 0}, // right lobe bottom
		{X: 26, Y: 10}, {X: 16, Y: 10}, {X: 16, Y: 6}, // right lobe top
		{X: 10, Y: 6}, {X: 10, Y: 10}, {X: 0, Y: 10}, // left lobe top
	}}
	out := slice.OffsetExPolygon(dumbbell, 2)
	require.Len(t, out, 2, "pinched dumbbell should split into two pieces")
}
