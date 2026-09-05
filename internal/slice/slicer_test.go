package slice_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/osafune/internal/mesh"
	"github.com/lestrrat-3d/osafune/internal/slice"
)

// cube returns an axis-aligned unit-cube mesh of side `size`, with its
// lower-front-left corner at (0,0,0). Twelve triangles, outward normals.
// We use this as the canonical synthetic input throughout the slicer
// tests — its slices should be exactly a `size × size` square at every
// Z in (0, size).
func cube(size float32) mesh.Mesh {
	v := [8]mesh.Vec3{
		{0, 0, 0}, {size, 0, 0}, {size, size, 0}, {0, size, 0},
		{0, 0, size}, {size, 0, size}, {size, size, size}, {0, size, size},
	}
	tri := func(a, b, c mesh.Vec3, n mesh.Vec3) mesh.Triangle {
		return mesh.Triangle{Vertices: [3]mesh.Vec3{a, b, c}, Normal: n}
	}
	tris := []mesh.Triangle{
		// bottom (Z-)
		tri(v[0], v[2], v[1], mesh.Vec3{0, 0, -1}),
		tri(v[0], v[3], v[2], mesh.Vec3{0, 0, -1}),
		// top (Z+)
		tri(v[4], v[5], v[6], mesh.Vec3{0, 0, 1}),
		tri(v[4], v[6], v[7], mesh.Vec3{0, 0, 1}),
		// front (Y-)
		tri(v[0], v[1], v[5], mesh.Vec3{0, -1, 0}),
		tri(v[0], v[5], v[4], mesh.Vec3{0, -1, 0}),
		// right (X+)
		tri(v[1], v[2], v[6], mesh.Vec3{1, 0, 0}),
		tri(v[1], v[6], v[5], mesh.Vec3{1, 0, 0}),
		// back (Y+)
		tri(v[2], v[3], v[7], mesh.Vec3{0, 1, 0}),
		tri(v[2], v[7], v[6], mesh.Vec3{0, 1, 0}),
		// left (X-)
		tri(v[3], v[0], v[4], mesh.Vec3{-1, 0, 0}),
		tri(v[3], v[4], v[7], mesh.Vec3{-1, 0, 0}),
	}
	var m mesh.Mesh
	m.Triangles = tris
	for _, t := range tris {
		for _, vv := range t.Vertices {
			m.Bounds.Extend(vv)
		}
	}
	return m
}

func TestSliceMesh_CubeProducesOneSquarePerLayer(t *testing.T) {
	t.Parallel()
	m := cube(10)

	layers := slice.SliceMesh(&m, 0.2, 0.2)
	// 10mm tall / 0.2mm per layer = ~50 layers; the top layer may be
	// included or not depending on how the half-layer offset lands.
	require.GreaterOrEqual(t, len(layers), 49)
	require.LessOrEqual(t, len(layers), 51)

	for i, l := range layers {
		// The bottom and top layers can be empty depending on whether
		// the slicing plane lands above/below the cube's bottom/top
		// face (Z-sweep doesn't intersect coplanar triangles). Skip
		// those degenerate edge layers and require the interior ones
		// to produce exactly one 10×10 square.
		if i == 0 || i == len(layers)-1 {
			continue
		}
		require.Len(t, l.Contours, 1, "layer %d should have exactly one contour", i)
		c := l.Contours[0]
		require.Empty(t, c.Holes, "cube has no holes")
		bb := c.Outer.BoundingBox()
		lo, hi := bb.Min, bb.Max
		require.InDelta(t, 0.0, lo.X, 1e-6, "layer %d minX", i)
		require.InDelta(t, 0.0, lo.Y, 1e-6, "layer %d minY", i)
		require.InDelta(t, 10.0, hi.X, 1e-6, "layer %d maxX", i)
		require.InDelta(t, 10.0, hi.Y, 1e-6, "layer %d maxY", i)
		require.True(t, c.Outer.IsCCW(), "outer should be CCW")
	}
}

func TestSlice_CubeFullPipeline(t *testing.T) {
	t.Parallel()
	m := cube(10)

	printer := defaultPrinter(t)
	process := defaultProcess(t)
	process.Perimeters = 2
	process.InfillDensity = 0.2
	process.TopLayers = 3
	process.BottomLayers = 3

	layers := slice.Slice(&m, &printer, &process)
	require.NotEmpty(t, layers)

	// Every layer should have at least one perimeter path and (for
	// non-edge layers) some infill or solid-infill paths.
	for i, l := range layers {
		if i == 0 || i == len(layers)-1 {
			continue
		}
		var perim, fill int
		for _, p := range l.Paths {
			switch p.Role {
			case slice.RoleExternalPerimeter, slice.RolePerimeter:
				perim++
			case slice.RoleInfill, slice.RoleSolidInfill:
				fill++
			}
		}
		require.Greater(t, perim, 0, "layer %d should have at least one perimeter path", i)
		require.Greater(t, fill, 0, "layer %d should have at least one infill path", i)
	}
}
