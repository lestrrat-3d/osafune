package slice_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/osafune/internal/slice"
)

// square10 is a 10×10 CCW contour at the origin.
func square10() slice.ExPolygon {
	return slice.ExPolygon{Outer: slice.Polygon{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}, {X: 0, Y: 10}}}
}

// maxExtent returns the largest coordinate magnitude over a path's points —
// a quick proxy for "how far out" a loop sits relative to the 10×10 contour.
func maxExtent(p slice.Path) float64 {
	m := 0.0
	for _, pt := range p.Points {
		if pt.X > m {
			m = pt.X
		}
		if pt.Y > m {
			m = pt.Y
		}
	}
	return m
}

func countRole(paths []slice.Path, role slice.PathRole) int {
	n := 0
	for _, p := range paths {
		if p.Role == role {
			n++
		}
	}
	return n
}

func TestGenerateSkirtLoopsOutsideAndFirst(t *testing.T) {
	t.Parallel()
	proc := defaultProcess(t)
	proc.SkirtLoops = 2
	proc.SkirtDistance = 2.0
	proc.BrimWidth = 0

	layer := slice.Layer{
		Index:    0,
		Contours: []slice.ExPolygon{square10()},
		Paths:    []slice.Path{{Points: []slice.Point2{{X: 1, Y: 1}, {X: 9, Y: 9}}, Role: slice.RolePerimeter}},
	}
	slice.GenerateSkirtBrim(&layer, &proc, 0.4, 25)

	require.Equal(t, 2, countRole(layer.Paths, slice.RoleSkirtBrim), "two skirt loops")
	require.Equal(t, slice.RoleSkirtBrim, layer.Paths[0].Role, "skirt prepended ahead of the model")
	require.Equal(t, slice.RolePerimeter, layer.Paths[len(layer.Paths)-1].Role, "model path stays last")
	for _, p := range layer.Paths {
		if p.Role != slice.RoleSkirtBrim {
			continue
		}
		require.True(t, p.Closed, "skirt loops are closed")
		// Innermost skirt sits SkirtDistance (2) + half a line beyond the
		// contour's max of 10, so every loop extends past ~12.
		require.Greater(t, maxExtent(p), 11.9, "skirt loop lies outside the object")
	}
}

func TestGenerateBrimLoopCount(t *testing.T) {
	t.Parallel()
	proc := defaultProcess(t)
	proc.SkirtLoops = 0
	proc.BrimWidth = 2.0 // / 0.4 line width = 5 loops

	layer := slice.Layer{Index: 0, Contours: []slice.ExPolygon{square10()}}
	slice.GenerateSkirtBrim(&layer, &proc, 0.4, 25)

	require.Equal(t, 5, countRole(layer.Paths, slice.RoleSkirtBrim), "BrimWidth/lineWidth loops")
	for _, p := range layer.Paths {
		require.Greater(t, maxExtent(p), 10.0, "brim loops lie outside the wall")
	}
}

func TestSkirtBrimDisabled(t *testing.T) {
	t.Parallel()
	proc := defaultProcess(t)
	proc.SkirtLoops = 0
	proc.BrimWidth = 0
	layer := slice.Layer{Index: 0, Contours: []slice.ExPolygon{square10()}}
	slice.GenerateSkirtBrim(&layer, &proc, 0.4, 25)
	require.Empty(t, layer.Paths, "no adhesion loops when both disabled")
}
