package slice_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-go/makislicer/internal/config"
	"github.com/lestrrat-go/makislicer/internal/slice"
)

func closedLoop(pts ...slice.Point2) slice.Path {
	// Copy so each Path owns its slice (as the generator's do); PlaceSeams
	// rotates Points in place, and tests reuse the same literal vertices.
	cp := append([]slice.Point2(nil), pts...)
	return slice.Path{Points: cp, Role: slice.RoleExternalPerimeter, Closed: true, Width: 0.4, Speed: 40}
}

// isRotationOf reports whether got is a cyclic rotation of want (same loop,
// possibly different start vertex).
func isRotationOf(got, want []slice.Point2) bool {
	if len(got) != len(want) {
		return false
	}
	n := len(want)
	for s := 0; s < n; s++ {
		ok := true
		for i := 0; i < n; i++ {
			if got[i] != want[(s+i)%n] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func TestPlaceSeamsAlignedPicksRearVertex(t *testing.T) {
	t.Parallel()
	orig := []slice.Point2{{X: 0, Y: 0}, {X: 2, Y: 0}, {X: 2, Y: 2}, {X: 0, Y: 2}}
	layer := slice.Layer{Index: 3, Paths: []slice.Path{closedLoop(orig...)}}
	slice.PlaceSeams(&layer, config.SeamAligned)

	// Rear-most vertex = max Y, ties broken by max X → (2,2).
	require.Equal(t, slice.Point2{X: 2, Y: 2}, layer.Paths[0].Points[0], "seam at rear-most vertex")
	require.True(t, isRotationOf(layer.Paths[0].Points, orig), "rotation must preserve the loop geometry")
}

func TestPlaceSeamsLeavesOpenPaths(t *testing.T) {
	t.Parallel()
	open := slice.Path{Points: []slice.Point2{{X: 0, Y: 0}, {X: 5, Y: 9}}, Role: slice.RoleInfill}
	layer := slice.Layer{Paths: []slice.Path{open}}
	slice.PlaceSeams(&layer, config.SeamAligned)
	require.Equal(t, slice.Point2{X: 0, Y: 0}, layer.Paths[0].Points[0], "open infill path untouched")
}

func TestPlaceSeamsRandomDeterministicButVaries(t *testing.T) {
	t.Parallel()
	orig := []slice.Point2{{X: 0, Y: 0}, {X: 2, Y: 0}, {X: 2, Y: 2}, {X: 0, Y: 2}}
	seamAt := func(layerIdx int) slice.Point2 {
		l := slice.Layer{Index: layerIdx, Paths: []slice.Path{closedLoop(orig...)}}
		slice.PlaceSeams(&l, config.SeamRandom)
		return l.Paths[0].Points[0]
	}
	// Deterministic: same layer index → same seam.
	require.Equal(t, seamAt(5), seamAt(5))
	// Varies across layers (scattering the seam). Sample several; expect at
	// least two distinct start vertices.
	seen := map[slice.Point2]struct{}{}
	for i := 0; i < 8; i++ {
		seen[seamAt(i)] = struct{}{}
	}
	require.Greater(t, len(seen), 1, "random seam should land on more than one vertex")
}
