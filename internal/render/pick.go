package render

import (
	"math"

	"github.com/lestrrat-3d/osafune/internal/mesh"
)

// PickRay returns the world-space picking ray for viewport pixel (sx, sy) in
// an fw×fh viewport: the origin is the camera eye and the direction is a unit
// vector through that pixel. Used to select the object under the cursor.
func (c *Camera) PickRay(sx, sy, fw, fh float32) (mesh.Vec3, mesh.Vec3) {
	eye, right, up, forward := c.Basis()
	fy := float32(1.0 / math.Tan(float64(c.FOV)*0.5))
	fx := fy / (fw / fh)
	ndcX := sx/fw*2 - 1
	ndcY := 1 - sy/fh*2
	dir := mesh.Vec3{
		forward[0] + right[0]*(ndcX/fx) + up[0]*(ndcY/fy),
		forward[1] + right[1]*(ndcX/fx) + up[1]*(ndcY/fy),
		forward[2] + right[2]*(ndcX/fx) + up[2]*(ndcY/fy),
	}
	return eye, normalize(dir)
}

// RayAABB intersects a ray with an axis-aligned box (slab method) and returns
// the entry distance along dir and whether they meet in front of the origin.
func RayAABB(origin, dir mesh.Vec3, b mesh.AABB) (float32, bool) {
	tmin := float32(math.Inf(-1))
	tmax := float32(math.Inf(1))
	for a := range 3 {
		if math.Abs(float64(dir[a])) < 1e-9 {
			if origin[a] < b.Min[a] || origin[a] > b.Max[a] {
				return 0, false // parallel and outside this slab
			}
			continue
		}
		inv := 1 / dir[a]
		t1 := (b.Min[a] - origin[a]) * inv
		t2 := (b.Max[a] - origin[a]) * inv
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		if t1 > tmin {
			tmin = t1
		}
		if t2 < tmax {
			tmax = t2
		}
		if tmin > tmax {
			return 0, false
		}
	}
	if tmax < 0 {
		return 0, false // box is entirely behind the origin
	}
	if tmin < 0 {
		return 0, true // origin is inside the box
	}
	return tmin, true
}

// PickObject returns the index of the nearest visible object whose bounding
// box the cursor ray hits, or -1 if none. Selection is by bounding box, which
// is precise enough to grab a part and cheap on the CPU.
func PickObject(scene *mesh.Scene, cam *Camera, sx, sy, fw, fh float32) int {
	if scene == nil {
		return -1
	}
	origin, dir := cam.PickRay(sx, sy, fw, fh)
	best := -1
	var bestT float32
	for i := range scene.Objects {
		if scene.Objects[i].Hidden {
			continue
		}
		t, ok := RayAABB(origin, dir, scene.Objects[i].Mesh.Bounds)
		if !ok {
			continue
		}
		if best == -1 || t < bestT {
			best, bestT = i, t
		}
	}
	return best
}
