package render

import (
	"image"
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/lestrrat-go/osafune/internal/mesh"
)

// GizmoElement identifies a grabbable part of the transform gizmo.
type GizmoElement int

const (
	GizmoNone GizmoElement = iota
	GizmoRotateX
	GizmoRotateY
	GizmoRotateZ
	GizmoScale
)

const (
	gizmoRingSegments = 64
	gizmoHitTol       = 8.0 // screen px within which a ring/handle is grabbed
)

// Gizmo draws and hit-tests the transform handles for the selected object:
// three rotation rings (one per world axis) and a uniform-scale handle. It is
// stateless — the viewport owns the selection and the in-progress drag — so a
// single instance is shared.
type Gizmo struct{}

// NewGizmo returns a gizmo drawer.
func NewGizmo() *Gizmo { return &Gizmo{} }

// GizmoRadius is the ring radius for an object with the given bounds: just
// outside its bounding sphere (half-diagonal) so the rings hug the object
// without swamping the view.
func GizmoRadius(b mesh.AABB) float64 { return float64(b.Diagonal()) * 0.55 }

type screenPt struct{ x, y float32 }

// ringAxisColor is the per-axis ring colour (X red, Y green, Z blue), matching
// the convention used everywhere else in the viewport.
func ringAxisColor(axis mesh.Axis) color.NRGBA {
	switch axis {
	case mesh.AxisX:
		return color.NRGBA{0xd9, 0x4f, 0x4f, 0xff}
	case mesh.AxisY:
		return color.NRGBA{0x57, 0xc0, 0x6b, 0xff}
	default:
		return color.NRGBA{0x4f, 0x8f, 0xd9, 0xff}
	}
}

var gizmoScaleColor = color.NRGBA{0xe8, 0xd0, 0x55, 0xff} // scale handle — amber

// Draw renders the gizmo for center/radius into bounds via cam.
func (g *Gizmo) Draw(dst *ebiten.Image, bounds image.Rectangle, cam *Camera, center mesh.Vec3, radius float64, active GizmoElement) {
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 || radius <= 0 {
		return
	}
	vp := cam.ViewProj(float32(w) / float32(h))
	ox, oy := float32(bounds.Min.X), float32(bounds.Min.Y)
	fw, fh := float32(w), float32(h)

	for _, axis := range []mesh.Axis{mesh.AxisX, mesh.AxisY, mesh.AxisZ} {
		pts := ringPoints(&vp, center, radius, axis, ox, oy, fw, fh)
		col := ringAxisColor(axis)
		width := float32(1.6)
		if active == rotateElement(axis) {
			width = 3 // thicken the ring being dragged
		}
		drawPolyline(dst, pts, col, width)
	}

	// Scale handle: a small filled square at the gizmo's projected centre,
	// nudged toward the camera-up so it doesn't sit under the rings.
	if sx, sy, ok := scaleHandleScreen(&vp, center, radius, ox, oy, fw, fh); ok {
		s := float32(5)
		if active == GizmoScale {
			s = 7
		}
		vector.DrawFilledRect(dst, sx-s, sy-s, 2*s, 2*s, gizmoScaleColor, true)
	}
}

// Hit returns the gizmo element under (sx, sy), or GizmoNone. Rotation rings
// are tested by distance to their projected polyline; the scale handle by
// distance to its marker.
func (g *Gizmo) Hit(bounds image.Rectangle, cam *Camera, center mesh.Vec3, radius float64, sx, sy float32) GizmoElement {
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 || radius <= 0 {
		return GizmoNone
	}
	vp := cam.ViewProj(float32(w) / float32(h))
	ox, oy := float32(bounds.Min.X), float32(bounds.Min.Y)
	fw, fh := float32(w), float32(h)

	// Scale handle takes priority (it sits on top of the rings).
	if hx, hy, ok := scaleHandleScreen(&vp, center, radius, ox, oy, fw, fh); ok {
		if math.Hypot(float64(sx-hx), float64(sy-hy)) <= gizmoHitTol+2 {
			return GizmoScale
		}
	}
	best := GizmoNone
	bestD := float64(gizmoHitTol)
	for _, axis := range []mesh.Axis{mesh.AxisX, mesh.AxisY, mesh.AxisZ} {
		pts := ringPoints(&vp, center, radius, axis, ox, oy, fw, fh)
		if d := polylineDist(pts, sx, sy); d < bestD {
			bestD = d
			best = rotateElement(axis)
		}
	}
	return best
}

// ScreenCenter projects the gizmo centre to viewport pixels (for the
// viewport's drag-angle and drag-distance math). ok is false behind the eye.
func (g *Gizmo) ScreenCenter(bounds image.Rectangle, cam *Camera, center mesh.Vec3) (float32, float32, bool) {
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	vp := cam.ViewProj(float32(w) / float32(h))
	pr := vp.Project(center)
	if !pr.InFront {
		return 0, 0, false
	}
	return float32(bounds.Min.X) + (pr.X+1)*0.5*float32(w),
		float32(bounds.Min.Y) + (1-(pr.Y+1)*0.5)*float32(h), true
}

func rotateElement(axis mesh.Axis) GizmoElement {
	switch axis {
	case mesh.AxisX:
		return GizmoRotateX
	case mesh.AxisY:
		return GizmoRotateY
	default:
		return GizmoRotateZ
	}
}

// ringPoints projects a circle of radius about center, in the plane normal to
// axis, to viewport pixels. Points behind the near plane are dropped (the
// polyline simply breaks there).
func ringPoints(vp *ViewProj, center mesh.Vec3, radius float64, axis mesh.Axis, ox, oy, fw, fh float32) []screenPt {
	pts := make([]screenPt, 0, gizmoRingSegments+1)
	for i := 0; i <= gizmoRingSegments; i++ {
		t := float64(i) / gizmoRingSegments * 2 * math.Pi
		p := ringWorldPoint(center, radius, axis, t)
		pr := vp.Project(p)
		if !pr.InFront {
			pts = append(pts, screenPt{x: float32(math.Inf(1))}) // break marker
			continue
		}
		pts = append(pts, screenPt{ox + (pr.X+1)*0.5*fw, oy + (1-(pr.Y+1)*0.5)*fh})
	}
	return pts
}

func ringWorldPoint(center mesh.Vec3, radius float64, axis mesh.Axis, t float64) mesh.Vec3 {
	c := float32(radius * math.Cos(t))
	s := float32(radius * math.Sin(t))
	switch axis {
	case mesh.AxisX:
		return mesh.Vec3{center[0], center[1] + c, center[2] + s}
	case mesh.AxisY:
		return mesh.Vec3{center[0] + c, center[1], center[2] + s}
	default: // AxisZ
		return mesh.Vec3{center[0] + c, center[1] + s, center[2]}
	}
}

func scaleHandleScreen(vp *ViewProj, center mesh.Vec3, radius float64, ox, oy, fw, fh float32) (float32, float32, bool) {
	// Just above the rings along +Z so it reads as a separate grab point.
	p := mesh.Vec3{center[0], center[1], center[2] + float32(radius*1.15)}
	pr := vp.Project(p)
	if !pr.InFront {
		return 0, 0, false
	}
	return ox + (pr.X+1)*0.5*fw, oy + (1-(pr.Y+1)*0.5)*fh, true
}

func drawPolyline(dst *ebiten.Image, pts []screenPt, col color.NRGBA, width float32) {
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		if math.IsInf(float64(a.x), 1) || math.IsInf(float64(b.x), 1) {
			continue // segment touches a behind-camera break
		}
		vector.StrokeLine(dst, a.x, a.y, b.x, b.y, width, col, true)
	}
}

// polylineDist is the smallest distance from (sx,sy) to any segment of pts.
func polylineDist(pts []screenPt, sx, sy float32) float64 {
	best := math.Inf(1)
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		if math.IsInf(float64(a.x), 1) || math.IsInf(float64(b.x), 1) {
			continue
		}
		if d := segDist(sx, sy, a, b); d < best {
			best = d
		}
	}
	return best
}

func segDist(px, py float32, a, b screenPt) float64 {
	dx, dy := b.x-a.x, b.y-a.y
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(float64(px-a.x), float64(py-a.y))
	}
	t := ((px-a.x)*dx + (py-a.y)*dy) / l2
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	cx, cy := a.x+t*dx, a.y+t*dy
	return math.Hypot(float64(px-cx), float64(py-cy))
}
