package mesh_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/units"

	"github.com/lestrrat-3d/osafune/internal/mesh"
)

// tri builds a single-triangle mesh with bounds, for transform tests.
func tri(a, b, c mesh.Vec3) *mesh.Mesh {
	m := &mesh.Mesh{Triangles: []mesh.Triangle{{Vertices: [3]mesh.Vec3{a, b, c}, Normal: mesh.Vec3{0, 0, 1}}}}
	for _, v := range []mesh.Vec3{a, b, c} {
		m.Bounds.Extend(v)
	}
	return m
}

// vecTol is the tolerance every vector comparison in this file uses: the
// transforms run in float32, so a quarter turn lands within a few ulps rather
// than on the exact value.
const vecTol = 1e-5

func vecInDelta(t *testing.T, want, got mesh.Vec3) {
	t.Helper()
	for i := range 3 {
		require.InDelta(t, float64(want[i]), float64(got[i]), vecTol, "component %d", i)
	}
}

func TestRotateZQuarterTurn(t *testing.T) {
	t.Parallel()
	m := tri(mesh.Vec3{1, 0, 0}, mesh.Vec3{2, 0, 0}, mesh.Vec3{1, 1, 0})
	// Degrees and radians name the same turn, and the typed angle is what makes
	// the call site say which it meant.
	require.NoError(t, m.Rotate(mesh.Vec3{0, 0, 0}, mesh.AxisZ, units.Degrees(90)))
	// (1,0)→(0,1), (2,0)→(0,2), (1,1)→(-1,1).
	vecInDelta(t, mesh.Vec3{0, 1, 0}, m.Triangles[0].Vertices[0])
	vecInDelta(t, mesh.Vec3{0, 2, 0}, m.Triangles[0].Vertices[1])
	vecInDelta(t, mesh.Vec3{-1, 1, 0}, m.Triangles[0].Vertices[2])
	// Bounds refreshed to the rotated extent.
	vecInDelta(t, mesh.Vec3{-1, 1, 0}, m.Bounds.Min)
	vecInDelta(t, mesh.Vec3{0, 2, 0}, m.Bounds.Max)
}

func TestRotateXTipsNormal(t *testing.T) {
	t.Parallel()
	m := tri(mesh.Vec3{0, 0, 0}, mesh.Vec3{1, 0, 0}, mesh.Vec3{0, 1, 0})
	require.NoError(t, m.Rotate(mesh.Vec3{0, 0, 0}, mesh.AxisX, units.Radians(math.Pi/2)))
	// Normal (0,0,1) about X by +90° → (0,-1,0).
	vecInDelta(t, mesh.Vec3{0, -1, 0}, m.Triangles[0].Normal)
}

func TestScaleUniformAboutCenter(t *testing.T) {
	t.Parallel()
	m := tri(mesh.Vec3{0, 0, 0}, mesh.Vec3{2, 0, 0}, mesh.Vec3{0, 2, 0})
	m.ScaleUniform(mesh.Vec3{1, 1, 0}, 2) // scale ×2 about (1,1)
	vecInDelta(t, mesh.Vec3{-1, -1, 0}, m.Triangles[0].Vertices[0])
	vecInDelta(t, mesh.Vec3{3, -1, 0}, m.Triangles[0].Vertices[1])
	vecInDelta(t, mesh.Vec3{-1, 3, 0}, m.Triangles[0].Vertices[2])
}

func TestDropToBed(t *testing.T) {
	t.Parallel()
	m := tri(mesh.Vec3{0, 0, 5}, mesh.Vec3{1, 0, 8}, mesh.Vec3{0, 1, 5})
	m.DropToBed()
	require.InDelta(t, 0, float64(m.Bounds.Min[2]), 1e-6, "lowest point sits on the bed")
	require.InDelta(t, 3, float64(m.Bounds.Max[2]), 1e-6, "height preserved")
}

func TestRotateRejectsNonAngle(t *testing.T) {
	t.Parallel()

	// The one thing a bare float64 radians parameter could not tell a caller:
	// that the number handed to it was never an angle. The mesh is left alone.
	m := tri(mesh.Vec3{1, 0, 0}, mesh.Vec3{2, 0, 0}, mesh.Vec3{1, 1, 0})
	before := m.Triangles[0].Vertices

	require.ErrorIs(t, m.Rotate(mesh.Vec3{}, mesh.AxisZ, units.Millimeters(90)), units.ErrIncompatible)
	require.Equal(t, before, m.Triangles[0].Vertices)

	// A bare number is not an angle either, which is the trap units exists for.
	require.ErrorIs(t, m.Rotate(mesh.Vec3{}, mesh.AxisZ, units.Scalar(90)), units.ErrIncompatible)
	require.Equal(t, before, m.Triangles[0].Vertices)
}

func TestRotateZeroAngleIsANoOp(t *testing.T) {
	t.Parallel()

	m := tri(mesh.Vec3{1, 0, 0}, mesh.Vec3{2, 0, 0}, mesh.Vec3{1, 1, 0})
	before := m.Triangles[0].Vertices
	require.NoError(t, m.Rotate(mesh.Vec3{}, mesh.AxisZ, units.Degrees(0)))
	require.Equal(t, before, m.Triangles[0].Vertices)
}
