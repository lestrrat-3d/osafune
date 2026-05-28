package render_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-go/osafune/internal/mesh"
	"github.com/lestrrat-go/osafune/internal/render"
)

// TestFitNearPlaneTiny guards against the near plane clipping the front of
// the model when zoomed in. The toolpath previewer drops a whole segment if
// any vertex falls behind the near plane (no near-plane clipping yet), which
// rendered as a see-through horizontal gap. Fit must therefore place the
// near plane very close — a point just 1mm in front of the eye must still
// project as in-front.
func TestFitNearPlaneTiny(t *testing.T) {
	t.Parallel()
	var cam render.Camera = render.Defaults()
	cam.Fit(mesh.AABB{Min: mesh.Vec3{0, 0, 0}, Max: mesh.Vec3{100, 100, 100}})

	require.Less(t, float64(cam.Near), float64(cam.Distance)*0.001,
		"near plane should be a tiny fraction of the view distance")

	eye, _, _, fwd := cam.Basis()
	vp := cam.ViewProj(1.5)
	front := mesh.Vec3{eye[0] + fwd[0], eye[1] + fwd[1], eye[2] + fwd[2]} // 1mm ahead of the eye
	require.True(t, vp.Project(front).InFront, "a point 1mm in front of the eye must not be clipped")
}

// TestUnprojectRoundTrip checks that Unproject inverts Project composed with
// the NDC→screen map the rasterizer uses, so the SSAO pass reconstructs the
// same world point the depth buffer was written from.
func TestUnprojectRoundTrip(t *testing.T) {
	t.Parallel()
	cam := render.Defaults()
	cam.Fit(mesh.AABB{Min: mesh.Vec3{0, 0, 0}, Max: mesh.Vec3{100, 100, 100}})
	const fw, fh float32 = 800, 600
	vp := cam.ViewProj(fw / fh)

	for _, p := range []mesh.Vec3{
		{50, 50, 50}, {10, 90, 5}, {90, 20, 80}, {50, 50, 0},
	} {
		pr := vp.Project(p)
		require.True(t, pr.InFront, "test point must be in front: %v", p)
		// Same NDC→screen mapping as projectSlabRange / rasterBand.
		sx := (pr.X + 1) * 0.5 * fw
		sy := (1 - (pr.Y+1)*0.5) * fh
		got := vp.Unproject(sx, sy, fw, fh, pr.ViewZ)
		for i := 0; i < 3; i++ {
			require.InDelta(t, float64(p[i]), float64(got[i]), 1e-2,
				"axis %d of unprojected point %v", i, p)
		}
	}
}
