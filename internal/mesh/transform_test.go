package mesh_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-go/osafune/internal/mesh"
)

// tri builds a single-triangle mesh with bounds, for transform tests.
func tri(a, b, c mesh.Vec3) *mesh.Mesh {
	m := &mesh.Mesh{Triangles: []mesh.Triangle{{Vertices: [3]mesh.Vec3{a, b, c}, Normal: mesh.Vec3{0, 0, 1}}}}
	for _, v := range []mesh.Vec3{a, b, c} {
		m.Bounds.Extend(v)
	}
	return m
}

func vecInDelta(t *testing.T, want, got mesh.Vec3, d float64) {
	t.Helper()
	for i := 0; i < 3; i++ {
		require.InDelta(t, float64(want[i]), float64(got[i]), d, "component %d", i)
	}
}

func TestRotateZQuarterTurn(t *testing.T) {
	t.Parallel()
	m := tri(mesh.Vec3{1, 0, 0}, mesh.Vec3{2, 0, 0}, mesh.Vec3{1, 1, 0})
	m.Rotate(mesh.Vec3{0, 0, 0}, mesh.AxisZ, math.Pi/2) // +90° about origin Z
	// (1,0)→(0,1), (2,0)→(0,2), (1,1)→(-1,1).
	vecInDelta(t, mesh.Vec3{0, 1, 0}, m.Triangles[0].Vertices[0], 1e-5)
	vecInDelta(t, mesh.Vec3{0, 2, 0}, m.Triangles[0].Vertices[1], 1e-5)
	vecInDelta(t, mesh.Vec3{-1, 1, 0}, m.Triangles[0].Vertices[2], 1e-5)
	// Bounds refreshed to the rotated extent.
	vecInDelta(t, mesh.Vec3{-1, 1, 0}, m.Bounds.Min, 1e-5)
	vecInDelta(t, mesh.Vec3{0, 2, 0}, m.Bounds.Max, 1e-5)
}

func TestRotateXTipsNormal(t *testing.T) {
	t.Parallel()
	m := tri(mesh.Vec3{0, 0, 0}, mesh.Vec3{1, 0, 0}, mesh.Vec3{0, 1, 0})
	m.Rotate(mesh.Vec3{0, 0, 0}, mesh.AxisX, math.Pi/2) // +90° about X
	// Normal (0,0,1) about X by +90° → (0,-1,0).
	vecInDelta(t, mesh.Vec3{0, -1, 0}, m.Triangles[0].Normal, 1e-5)
}

func TestScaleUniformAboutCenter(t *testing.T) {
	t.Parallel()
	m := tri(mesh.Vec3{0, 0, 0}, mesh.Vec3{2, 0, 0}, mesh.Vec3{0, 2, 0})
	m.ScaleUniform(mesh.Vec3{1, 1, 0}, 2) // scale ×2 about (1,1)
	vecInDelta(t, mesh.Vec3{-1, -1, 0}, m.Triangles[0].Vertices[0], 1e-5)
	vecInDelta(t, mesh.Vec3{3, -1, 0}, m.Triangles[0].Vertices[1], 1e-5)
	vecInDelta(t, mesh.Vec3{-1, 3, 0}, m.Triangles[0].Vertices[2], 1e-5)
}

func TestDropToBed(t *testing.T) {
	t.Parallel()
	m := tri(mesh.Vec3{0, 0, 5}, mesh.Vec3{1, 0, 8}, mesh.Vec3{0, 1, 5})
	m.DropToBed()
	require.InDelta(t, 0, float64(m.Bounds.Min[2]), 1e-6, "lowest point sits on the bed")
	require.InDelta(t, 3, float64(m.Bounds.Max[2]), 1e-6, "height preserved")
}
