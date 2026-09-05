package slice_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/osafune/internal/slice"
)

// travelLength sums the non-extruding travel between consecutive paths: the
// gap from where one path ends to where the next begins. Mirrors the order
// the gcode writer travels in (to Points[0], reaching Points[0] again for a
// closed loop or the last point for an open path).
func travelLength(paths []slice.Path) float64 {
	end := func(p slice.Path) slice.Point2 {
		if p.Closed {
			return p.Points[0]
		}
		return p.Points[len(p.Points)-1]
	}
	var total float64
	for i := 1; i < len(paths); i++ {
		prev := end(paths[i-1])
		cur := paths[i].Points[0]
		total += math.Hypot(prev.X-cur.X, prev.Y-cur.Y)
	}
	return total
}

func line(role slice.PathRole, x0, y0, x1, y1 float64) slice.Path {
	return slice.Path{Points: []slice.Point2{{X: x0, Y: y0}, {X: x1, Y: y1}}, Role: role, Width: 0.4, Speed: 60}
}

func TestOptimizeTravelBoustrophedon(t *testing.T) {
	t.Parallel()
	// Four parallel scanlines, all left-to-right — the worst case the
	// generator produces. Each return hop is the full line width (~10).
	layer := slice.Layer{Paths: []slice.Path{
		line(slice.RoleInfill, 0, 0, 10, 0),
		line(slice.RoleInfill, 0, 1, 10, 1),
		line(slice.RoleInfill, 0, 2, 10, 2),
		line(slice.RoleInfill, 0, 3, 10, 3),
	}}
	before := travelLength(layer.Paths)
	slice.OptimizeTravel(&layer)
	after := travelLength(layer.Paths)

	require.Less(t, after, before, "reordering must cut travel")
	require.Less(t, after, 4.0, "boustrophedon hops should each be ~1mm (the line spacing)")
	require.Greater(t, before, 25.0, "sanity: original left-returns are long")
}

func TestOptimizeTravelPreservesPhaseOrder(t *testing.T) {
	t.Parallel()
	// Walls then infill — reordering must never float infill ahead of walls.
	layer := slice.Layer{Paths: []slice.Path{
		line(slice.RoleExternalPerimeter, 0, 0, 5, 0),
		line(slice.RolePerimeter, 0, 1, 5, 1),
		line(slice.RolePerimeter, 9, 9, 5, 9),
		line(slice.RoleInfill, 0, 2, 10, 2),
		line(slice.RoleInfill, 0, 3, 10, 3),
	}}
	slice.OptimizeTravel(&layer)

	// Collapse consecutive duplicate roles; the phase sequence must be
	// unchanged (External, Perimeter, Infill).
	var phases []slice.PathRole
	for _, p := range layer.Paths {
		if len(phases) == 0 || phases[len(phases)-1] != p.Role {
			phases = append(phases, p.Role)
		}
	}
	require.Equal(t,
		[]slice.PathRole{slice.RoleExternalPerimeter, slice.RolePerimeter, slice.RoleInfill},
		phases)
}

func TestOptimizeTravelFlipsOpenButNotClosed(t *testing.T) {
	t.Parallel()
	// A closed loop followed by an open line whose far end is nearer to the
	// loop's end than its near end.
	loop := slice.Path{
		Points: []slice.Point2{{X: 0, Y: 0}, {X: 2, Y: 0}, {X: 2, Y: 2}, {X: 0, Y: 2}},
		Role:   slice.RolePerimeter, Closed: true, Width: 0.4, Speed: 60,
	}
	open := line(slice.RolePerimeter, 20, 20, 2, 0) // far end (20,20), near end (2,0)
	layer := slice.Layer{Paths: []slice.Path{loop, open}}
	slice.OptimizeTravel(&layer)

	// Closed loop keeps its start point (closed paths are never reversed).
	require.Equal(t, slice.Point2{X: 0, Y: 0}, layer.Paths[0].Points[0], "closed loop start unchanged")
	// The open line is flipped to begin at its (2,0) end, nearest the loop.
	require.Equal(t, slice.Point2{X: 2, Y: 0}, layer.Paths[1].Points[0], "open path flipped to nearer end")
}
