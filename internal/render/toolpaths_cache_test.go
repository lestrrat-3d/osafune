package render

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-go/osafune/internal/mesh"
	"github.com/lestrrat-go/osafune/internal/slice"
)

// squareBounds is the AABB of the 10×10 test geometry, used to frame the
// camera so its segments project in front of the near plane.
func squareBounds() mesh.AABB {
	return mesh.AABB{Min: mesh.Vec3{0, 0, 0}, Max: mesh.Vec3{10, 10, 1}}
}

// squareLayer returns a layer with a filled cross-section and a closed
// perimeter path, so both the wall shell (from Contours) and the cut-face
// beads (from Paths) have real geometry.
func squareLayer(z float64) slice.Layer {
	sq := slice.Polygon{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}, {X: 0, Y: 10}}
	return slice.Layer{
		Z:        z,
		Height:   0.2,
		Contours: []slice.ExPolygon{{Outer: sq}},
		Paths: []slice.Path{{
			Role: slice.RoleExternalPerimeter, Width: 0.4, Closed: true, Points: sq,
		}},
	}
}

// coveredPixels counts opaque (alpha != 0) pixels in the rendered buffer.
func (d *ToolpathDrawer) coveredPixels() int {
	n := 0
	for i := 3; i < len(d.rgba); i += 4 {
		if d.rgba[i] != 0 {
			n++
		}
	}
	return n
}

func TestToolpathDrawerCache(t *testing.T) {
	t.Parallel()
	d := NewToolpathDrawer()
	cam := Defaults()
	cam.Fit(squareBounds())
	const W, H = 640, 480
	layers := []slice.Layer{squareLayer(0.2), squareLayer(0.4)}

	require.True(t, d.prepare(W, H, 1, layers, &cam, 1, false, false, false), "first call must render")
	require.Greater(t, d.coveredPixels(), 0, "render should cover pixels")
	require.False(t, d.prepare(W, H, 1, layers, &cam, 1, false, false, false), "unchanged inputs must reuse cache")

	moved := cam
	moved.Yaw += 0.5
	require.True(t, d.prepare(W, H, 1, layers, &moved, 1, false, false, false), "camera change must re-render")
	require.False(t, d.prepare(W, H, 1, layers, &moved, 1, false, false, false), "second identical call must hit cache")

	require.True(t, d.prepare(W, H, 1, layers, &moved, 2, false, false, false), "geomGen change must re-render")
	require.True(t, d.prepare(W/2, H/2, 1, layers, &moved, 2, false, false, false), "resolution change must re-render")
	require.True(t, d.prepare(W/2, H/2, 1, layers, &moved, 2, true, false, false), "cut-flag change must re-render")
	require.True(t, d.prepare(W/2, H/2, 2, layers, &moved, 2, true, false, false), "supersample change must re-render")
}

func TestToolpathDrawerShellAndCut(t *testing.T) {
	t.Parallel()
	cam := Defaults()
	cam.Fit(squareBounds())
	layers := []slice.Layer{squareLayer(0.2), squareLayer(0.4), squareLayer(0.6)}

	// Whole model: solid wall shell only.
	full := NewToolpathDrawer()
	full.prepare(640, 480, 1, layers, &cam, 1, false, false, false)
	require.NotEmpty(t, full.worldSlab, "shell geometry should be built")
	require.Greater(t, full.coveredPixels(), 0)
	shellTris := len(full.tris)

	// Top cutaway: shell body + the top layer's beads overlaid → more
	// projected triangles than the shell alone.
	cut := NewToolpathDrawer()
	cut.prepare(640, 480, 1, layers, &cam, 1, true, false, false)
	require.Greater(t, cut.coveredPixels(), 0)
	require.Greater(t, len(cut.tris), shellTris, "cut face should add bead triangles")
}

func TestToolpathDrawerBackfaceCulled(t *testing.T) {
	t.Parallel()
	// The projected shell must be back-face culled: far faces dropped, so
	// fewer triangles are drawn than the closed shell contains.
	d := NewToolpathDrawer()
	cam := Defaults()
	cam.Fit(squareBounds())
	layers := []slice.Layer{squareLayer(0.2), squareLayer(0.4)}
	d.prepare(640, 480, 1, layers, &cam, 1, false, false, false)
	require.NotEmpty(t, d.worldSlab)
	require.Less(t, len(d.tris), len(d.worldSlab), "back faces should be culled")
}

func TestToolpathDrawerReducedResolution(t *testing.T) {
	t.Parallel()
	d := NewToolpathDrawer()
	cam := Defaults()
	cam.Fit(squareBounds())
	layers := []slice.Layer{squareLayer(0.2), squareLayer(0.4)}

	d.prepare(640, 480, 1, layers, &cam, 1, false, false, false)
	full := len(d.rgba)
	d.prepare(320, 240, 1, layers, &cam, 1, false, false, false)
	half := len(d.rgba)
	require.Equal(t, 640*480*4, full)
	require.Equal(t, 320*240*4, half)
	require.Less(t, half, full)
}

// TestToolpathDrawerSupersampleAndSSAO exercises the settle path: a 2×
// supersampled render with the SSAO pass, then box-downsample. The internal
// buffers are ss² the output, while d.out matches the output resolution and
// the rendered frame still covers pixels.
func TestToolpathDrawerSupersampleAndSSAO(t *testing.T) {
	t.Parallel()
	d := NewToolpathDrawer()
	cam := Defaults()
	cam.Fit(squareBounds())
	layers := []slice.Layer{squareLayer(0.2), squareLayer(0.4)}
	const W, H, SS = 320, 240, 2

	require.True(t, d.prepare(W, H, SS, layers, &cam, 1, false, false, true), "first settle render")
	require.Equal(t, W*SS*H*SS*4, len(d.rgba), "raster buffer is supersampled")
	require.Equal(t, W*SS*H*SS*3, len(d.nbuf), "normal buffer is supersampled")
	require.Equal(t, W*H*4, len(d.out), "output buffer is screen resolution")
	require.Greater(t, d.coveredPixels(), 0, "render should cover pixels")
	require.False(t, d.prepare(W, H, SS, layers, &cam, 1, false, false, true), "unchanged inputs reuse cache")
}
