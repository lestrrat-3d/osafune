// Package render projects [mesh.Mesh] geometry into screen space and hands
// it to Ebitengine's textured-triangle pipeline. The whole pipeline is
// software so it works the same on WSLg and on bare Linux/macOS/Windows;
// the slicer stages can lift it onto the GPU later if it becomes the
// bottleneck.
package render

import (
	"math"

	"github.com/lestrrat-go/makislicer/internal/mesh"
)

// Camera is an orbit camera around Target at spherical (Yaw, Pitch, Distance).
//
// Yaw rotates around the world Z axis; Pitch tilts above the X/Y plane. The
// orientation matches what a CAD user expects when dragging in 2D: dragging
// right rotates the model clockwise as seen from above. Z-up matches both
// STL convention and the build-plate convention used by every slicer.
type Camera struct {
	Target   mesh.Vec3
	Distance float32
	Yaw      float32 // radians; rotation around world Z.
	Pitch    float32 // radians; clamped to (-pi/2, pi/2) by the input handlers.
	FOV      float32 // radians; vertical field of view.
	Near     float32
	Far      float32
}

// Defaults returns a camera framing a unit cube at the origin from the
// front-right-and-above with a sensible FOV. Use [Camera.Fit] right after to
// reframe for a real mesh.
func Defaults() Camera {
	return Camera{
		Yaw:      -math.Pi / 4,
		Pitch:    math.Pi / 6,
		Distance: 4,
		FOV:      math.Pi / 4,
		Near:     0.1,
		Far:      10000,
	}
}

// Fit reframes the camera so that bbox sits comfortably inside the view
// regardless of the model's overall scale. It preserves the orientation
// (Yaw/Pitch) and recentres Target on the bbox.
func (c *Camera) Fit(bbox mesh.AABB) {
	if bbox.Empty() {
		return
	}
	c.Target = bbox.Center()
	// Half-diagonal of the bbox is an upper bound on the radius of the
	// circumscribing sphere; place the eye far enough away that the sphere
	// fits within the FOV cone with a margin.
	r := bbox.Diagonal() * 0.5
	half := float64(c.FOV) * 0.5
	if half <= 0 {
		half = math.Pi / 8
	}
	c.Distance = r / float32(math.Tan(half)) * 1.4
	// Keep Near/Far loose enough that the whole model is in range even
	// after the user zooms in or pans away.
	c.Near = c.Distance * 0.01
	c.Far = c.Distance * 100
}

// Basis returns the world-space orthonormal basis at the eye:
// right (camera X), up (camera Y), forward (toward Target, camera -Z).
// The eye position is also returned for convenience.
func (c *Camera) Basis() (eye, right, up, forward mesh.Vec3) {
	cosP := float32(math.Cos(float64(c.Pitch)))
	sinP := float32(math.Sin(float64(c.Pitch)))
	cosY := float32(math.Cos(float64(c.Yaw)))
	sinY := float32(math.Sin(float64(c.Yaw)))

	// Direction from Target to eye (the camera offsets away from the target
	// along this vector). Z-up; on the equator (Pitch=0) the camera sits in
	// the X/Y plane.
	dx := cosP * cosY
	dy := cosP * sinY
	dz := sinP
	eye = mesh.Vec3{
		c.Target[0] + dx*c.Distance,
		c.Target[1] + dy*c.Distance,
		c.Target[2] + dz*c.Distance,
	}
	forward = mesh.Vec3{-dx, -dy, -dz}

	// Right is forward × worldUp, normalised. Pitch is clamped before this
	// runs so worldUp and forward are never colinear.
	worldUp := mesh.Vec3{0, 0, 1}
	right = normalize(cross(forward, worldUp))
	up = cross(right, forward) // already unit length since right and forward are orthonormal.
	return
}

// Projected is the result of projecting a world point through the camera.
// X/Y are in normalized device coordinates (-1..1, with Y pointing up like
// in OpenGL); ViewZ is the signed view-space depth (negative in front of
// the camera). InFront reports whether the point is on the visible side of
// the near plane — points behind the near plane must not be drawn because
// their NDC values blow up on perspective divide.
type Projected struct {
	X, Y    float32
	ViewZ   float32
	InFront bool
}

// Project transforms a world point through the camera. aspect is the
// viewport's width/height ratio.
func (c *Camera) Project(p mesh.Vec3, aspect float32) Projected {
	eye, right, up, forward := c.Basis()
	dx := p[0] - eye[0]
	dy := p[1] - eye[1]
	dz := p[2] - eye[2]
	xv := dx*right[0] + dy*right[1] + dz*right[2]
	yv := dx*up[0] + dy*up[1] + dz*up[2]
	zv := -(dx*forward[0] + dy*forward[1] + dz*forward[2]) // negative so points in front have negative view-z.

	if zv > -c.Near {
		return Projected{ViewZ: zv}
	}
	fy := float32(1.0 / math.Tan(float64(c.FOV)*0.5))
	fx := fy / aspect
	nx := xv * fx / -zv
	ny := yv * fy / -zv
	return Projected{X: nx, Y: ny, ViewZ: zv, InFront: true}
}

// ViewProj bundles the per-frame camera constants (basis vectors and FOV
// factors) so a batch of points can be projected without recomputing the
// basis and its trig on every call. [Camera.Project] is convenient for one
// point but recomputes [Camera.Basis] each time; projecting millions of
// toolpath vertices per frame makes that redundant trig the hot path.
type ViewProj struct {
	eye, right, up, forward mesh.Vec3
	near, fx, fy            float32
}

// ViewProj returns the projection constants for the given aspect ratio.
func (c *Camera) ViewProj(aspect float32) ViewProj {
	eye, right, up, forward := c.Basis()
	fy := float32(1.0 / math.Tan(float64(c.FOV)*0.5))
	return ViewProj{eye: eye, right: right, up: up, forward: forward, near: c.Near, fx: fy / aspect, fy: fy}
}

// Eye returns the camera position baked into these constants.
func (v *ViewProj) Eye() mesh.Vec3 { return v.eye }

// Project transforms a world point using the precomputed constants. It
// matches [Camera.Project] exactly but does no per-call basis or trig work.
func (v *ViewProj) Project(p mesh.Vec3) Projected {
	dx := p[0] - v.eye[0]
	dy := p[1] - v.eye[1]
	dz := p[2] - v.eye[2]
	xv := dx*v.right[0] + dy*v.right[1] + dz*v.right[2]
	yv := dx*v.up[0] + dy*v.up[1] + dz*v.up[2]
	zv := -(dx*v.forward[0] + dy*v.forward[1] + dz*v.forward[2])
	if zv > -v.near {
		return Projected{ViewZ: zv}
	}
	nx := xv * v.fx / -zv
	ny := yv * v.fy / -zv
	return Projected{X: nx, Y: ny, ViewZ: zv, InFront: true}
}

// PanScreen moves Target by the given screen-space deltas (in pixels). The
// deltas are converted to world units at the depth of the current Target so
// the model tracks the cursor at a sensible rate regardless of zoom.
//
// viewportH is needed to convert pixels to NDC for the vertical axis.
func (c *Camera) PanScreen(dxPixels, dyPixels float32, viewportH int) {
	if viewportH <= 0 {
		return
	}
	_, right, up, _ := c.Basis()
	// One pixel along the screen corresponds to (2 * tan(fov/2) * dist) / vh
	// world units at the Target's depth.
	worldPerPixel := float32(2*math.Tan(float64(c.FOV)*0.5)) * c.Distance / float32(viewportH)
	dxW := dxPixels * worldPerPixel
	dyW := dyPixels * worldPerPixel
	// Dragging right (positive dx) moves the world to the right of the
	// camera, which means Target moves LEFT in world space.
	for i := 0; i < 3; i++ {
		c.Target[i] += -right[i]*dxW + up[i]*dyW
	}
}

// Zoom scales Distance by factor (use 1/factor for the opposite direction).
// Distance is clamped to keep the camera from collapsing onto the Target
// or shooting past the Far plane.
func (c *Camera) Zoom(factor float32) {
	if factor <= 0 {
		return
	}
	c.Distance *= factor
	if c.Distance < 0.001 {
		c.Distance = 0.001
	}
}

// Orbit applies yaw/pitch increments and clamps Pitch away from the poles.
func (c *Camera) Orbit(dYaw, dPitch float32) {
	c.Yaw += dYaw
	c.Pitch += dPitch
	const lim = math.Pi/2 - 0.01
	if c.Pitch > lim {
		c.Pitch = lim
	}
	if c.Pitch < -lim {
		c.Pitch = -lim
	}
}

func cross(a, b mesh.Vec3) mesh.Vec3 {
	return mesh.Vec3{
		a[1]*b[2] - a[2]*b[1],
		a[2]*b[0] - a[0]*b[2],
		a[0]*b[1] - a[1]*b[0],
	}
}

func normalize(v mesh.Vec3) mesh.Vec3 {
	l := float32(math.Sqrt(float64(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])))
	if l == 0 {
		return v
	}
	return mesh.Vec3{v[0] / l, v[1] / l, v[2] / l}
}
