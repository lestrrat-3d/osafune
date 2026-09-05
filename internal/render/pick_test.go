package render_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/osafune/internal/mesh"
	"github.com/lestrrat-3d/osafune/internal/render"
)

func TestRayAABBHitAndMiss(t *testing.T) {
	t.Parallel()
	box := mesh.AABB{Min: mesh.Vec3{-1, -1, -1}, Max: mesh.Vec3{1, 1, 1}}
	// Ray from -Z toward +Z straight at the box centre hits at distance 9.
	tHit, ok := render.RayAABB(mesh.Vec3{0, 0, -10}, mesh.Vec3{0, 0, 1}, box)
	require.True(t, ok)
	require.InDelta(t, 9, float64(tHit), 1e-5)
	// Parallel ray off to the side misses.
	_, ok = render.RayAABB(mesh.Vec3{5, 5, -10}, mesh.Vec3{0, 0, 1}, box)
	require.False(t, ok)
	// Ray pointing away from the box (behind the origin) misses.
	_, ok = render.RayAABB(mesh.Vec3{0, 0, -10}, mesh.Vec3{0, 0, -1}, box)
	require.False(t, ok)
}

func TestPickObjectSelectsUnderCursor(t *testing.T) {
	t.Parallel()
	// One object centred on the bed; camera framed on it. A ray through the
	// viewport centre must hit it; a ray through a far corner must not.
	box := mesh.AABB{Min: mesh.Vec3{90, 90, 0}, Max: mesh.Vec3{110, 110, 20}}
	scene := &mesh.Scene{Objects: []mesh.Object{{
		Name: "part",
		Mesh: mesh.Mesh{Bounds: box, Triangles: []mesh.Triangle{{Vertices: [3]mesh.Vec3{{90, 90, 0}, {110, 90, 0}, {90, 110, 20}}}}},
	}}}
	cam := render.Defaults()
	cam.Fit(box)
	const w, h = 800, 600

	require.Equal(t, 0, render.PickObject(scene, &cam, w/2, h/2, w, h), "centre ray hits the part")
	require.Equal(t, -1, render.PickObject(scene, &cam, 2, 2, w, h), "corner ray misses")
}
