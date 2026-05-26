package render

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-go/makislicer/internal/mesh"
	"github.com/lestrrat-go/makislicer/internal/slice"
)

// squareBounds is the AABB of the 10×10 test geometry, used to frame the
// camera so its segments project in front of the near plane.
func squareBounds() mesh.AABB {
	return mesh.AABB{Min: mesh.Vec3{0, 0, 0}, Max: mesh.Vec3{10, 10, 1}}
}

// squareLayer returns a layer with one closed external-perimeter loop, so
// the drawer has real segments to project and pack.
func squareLayer(z float64) slice.Layer {
	return slice.Layer{
		Z:      z,
		Height: 0.2,
		Paths: []slice.Path{{
			Role:   slice.RoleExternalPerimeter,
			Width:  0.4,
			Closed: true,
			Points: []slice.Point2{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}, {X: 0, Y: 10}},
		}},
	}
}

func TestToolpathDrawerCache(t *testing.T) {
	t.Parallel()
	d := NewToolpathDrawer()
	cam := Defaults()
	cam.Fit(squareBounds())
	bounds := image.Rect(0, 0, 640, 480)
	layers := []slice.Layer{squareLayer(0.2), squareLayer(0.4)}

	// First prepare is a cache miss (nothing built yet).
	require.True(t, d.prepare(bounds, layers, &cam, 1, false), "first call must rebuild")
	require.NotEmpty(t, d.batches, "rebuild should produce batches")

	// Identical inputs: cache hit, no rebuild.
	require.False(t, d.prepare(bounds, layers, &cam, 1, false), "unchanged inputs must reuse cache")

	// Camera change invalidates.
	moved := cam
	moved.Yaw += 0.5
	require.True(t, d.prepare(bounds, layers, &moved, 1, false), "camera change must rebuild")
	require.False(t, d.prepare(bounds, layers, &moved, 1, false), "second identical call must hit cache")

	// geomGen bump (new slice / layer range) invalidates.
	require.True(t, d.prepare(bounds, layers, &moved, 2, false), "geomGen change must rebuild")

	// Viewport resize invalidates.
	require.True(t, d.prepare(image.Rect(0, 0, 800, 600), layers, &moved, 2, false), "bounds change must rebuild")
}

func TestToolpathDrawerAdaptiveDecimation(t *testing.T) {
	t.Parallel()
	// A layer with one perimeter loop and one infill run.
	cam := Defaults()
	cam.Fit(squareBounds())
	bounds := image.Rect(0, 0, 640, 480)
	layers := []slice.Layer{{
		Z: 0.2, Height: 0.2,
		Paths: []slice.Path{
			{Role: slice.RoleExternalPerimeter, Width: 0.4, Closed: true,
				Points: []slice.Point2{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}, {X: 0, Y: 10}}},
			{Role: slice.RoleInfill, Width: 0.4,
				Points: []slice.Point2{{X: 1, Y: 1}, {X: 9, Y: 1}, {X: 9, Y: 9}, {X: 1, Y: 9}}},
		},
	}}

	// Light view (under budget): dragging keeps full detail, infill included.
	light := NewToolpathDrawer()
	light.prepare(bounds, layers, &cam, 1, false) // establishes full count
	full := len(light.segs)
	moved := cam
	moved.Yaw += 0.3
	light.prepare(bounds, layers, &moved, 1, true) // interacting
	require.Equal(t, full, len(light.segs), "a light view should keep full detail while dragging")

	// Dense view (budget forced to 0): dragging drops to walls only.
	dense := NewToolpathDrawer()
	dense.decimationBudget = 0
	dense.prepare(bounds, layers, &cam, 1, false) // sets lastFullSegCount > 0
	dense.prepare(bounds, layers, &moved, 1, true)
	require.Equal(t, 4, len(dense.segs), "over budget, a drag keeps only the 4 perimeter segments")
}

func TestToolpathDrawerBatchIndexCap(t *testing.T) {
	t.Parallel()
	// Enough segments to span multiple batches, verifying the uint16 cap
	// split: each batch's indices must stay within range of its vertices.
	d := NewToolpathDrawer()
	cam := Defaults()
	cam.Fit(squareBounds())

	// ~8000 points, all inside the fitted [0,10] view so the off-screen
	// cull keeps them; 7999 segments span several uint16-capped batches.
	pts := make([]slice.Point2, 0, 8000)
	for i := range cap(pts) {
		pts = append(pts, slice.Point2{X: float64(i%100) * 0.1, Y: float64(i/100) * 0.125})
	}
	layers := []slice.Layer{{
		Z: 0.2, Height: 0.2,
		Paths: []slice.Path{{Role: slice.RoleInfill, Width: 0.4, Points: pts}},
	}}

	d.prepare(image.Rect(0, 0, 640, 480), layers, &cam, 1, false)
	require.Greater(t, len(d.batches), 1, "should span multiple batches")
	for bi := range d.batches {
		b := d.batches[bi]
		require.LessOrEqual(t, len(b.verts), 65535, "batch vertices within uint16 range")
		for _, idx := range b.indices {
			require.Less(t, int(idx), len(b.verts), "index in batch %d out of range", bi)
		}
	}
}
