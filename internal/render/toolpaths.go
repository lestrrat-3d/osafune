package render

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/lestrrat-go/makislicer/internal/mesh"
	"github.com/lestrrat-go/makislicer/internal/slice"
)

// roleColor maps a path role to a stable preview colour. The palette
// matches PrusaSlicer's / OrcaSlicer's default toolpath legend closely
// enough that anyone used to those tools can read the preview without
// thinking — external perimeters orange, infill yellow, solid infill
// red-ish, travels a faint dotted grey (drawn last so they don't drown
// out the extrusions).
func roleColor(r slice.PathRole) color.NRGBA {
	switch r {
	case slice.RoleExternalPerimeter:
		return color.NRGBA{0xff, 0x80, 0x00, 0xff}
	case slice.RolePerimeter:
		return color.NRGBA{0xff, 0xb0, 0x40, 0xff}
	case slice.RoleInfill:
		return color.NRGBA{0xc0, 0xc0, 0x40, 0xff}
	case slice.RoleSolidInfill:
		return color.NRGBA{0xc0, 0x40, 0x40, 0xff}
	}
	return color.NRGBA{0x60, 0x60, 0x60, 0x40}
}

// ToolpathDrawer renders sliced layers as projected line segments
// through the same [Camera] the mesh viewer uses. The two views share a
// camera so the user can swap between mesh and toolpaths without losing
// orbit / pan state.
//
// Drawing is straight ebiten.vector.StrokeLine per segment; for an MVP
// with O(10k) paths this is well within frame budget, and switching to
// a batched line-mesh later is a contained change.
type ToolpathDrawer struct {
	// LineWidthPx is the preview stroke thickness in pixels. Real
	// extrusion width is layer-dependent and the path knows its true
	// mm width; for the preview we use a constant pixel width because
	// it reads better at any zoom level.
	LineWidthPx float32
}

// NewToolpathDrawer returns a drawer with a 1.5px stroke. Callers can
// adjust [ToolpathDrawer.LineWidthPx] before [Draw] if they want a
// chunkier or thinner preview.
func NewToolpathDrawer() *ToolpathDrawer { return &ToolpathDrawer{LineWidthPx: 1.5} }

// Draw projects every path in layers through cam into dstBounds and
// strokes each segment. Layers are drawn in Z order (bottom up) so
// upper layers paint over lower ones — a cheap approximation of depth
// that works because adjacent layers' paths almost never overlap.
func (d *ToolpathDrawer) Draw(dst *ebiten.Image, dstBounds image.Rectangle, layers []slice.Layer, cam *Camera) {
	w := dstBounds.Dx()
	h := dstBounds.Dy()
	if w <= 0 || h <= 0 || len(layers) == 0 {
		return
	}
	aspect := float32(w) / float32(h)
	x0 := float32(dstBounds.Min.X)
	y0 := float32(dstBounds.Min.Y)
	fw := float32(w)
	fh := float32(h)

	for li := range layers {
		z := float32(layers[li].Z)
		for _, p := range layers[li].Paths {
			if !p.Role.IsExtrusion() || len(p.Points) < 2 {
				continue
			}
			c := roleColor(p.Role)
			prev := project(cam, layers[li].Z, p.Points[0], aspect, x0, y0, fw, fh)
			for i := 1; i < len(p.Points); i++ {
				cur := project(cam, layers[li].Z, p.Points[i], aspect, x0, y0, fw, fh)
				if prev.ok && cur.ok {
					vector.StrokeLine(dst, prev.sx, prev.sy, cur.sx, cur.sy, d.LineWidthPx, c, false)
				}
				prev = cur
			}
			if p.Closed && len(p.Points) >= 2 {
				cur := project(cam, layers[li].Z, p.Points[0], aspect, x0, y0, fw, fh)
				if prev.ok && cur.ok {
					vector.StrokeLine(dst, prev.sx, prev.sy, cur.sx, cur.sy, d.LineWidthPx, c, false)
				}
			}
		}
		_ = z
	}
}

type projected2 struct {
	sx, sy float32
	ok     bool
}

func project(cam *Camera, z float64, p slice.Point2, aspect, x0, y0, fw, fh float32) projected2 {
	w := mesh.Vec3{float32(p.X), float32(p.Y), float32(z)}
	pr := cam.Project(w, aspect)
	if !pr.InFront {
		return projected2{}
	}
	return projected2{
		sx: x0 + (pr.X+1)*0.5*fw,
		sy: y0 + (1-(pr.Y+1)*0.5)*fh,
		ok: true,
	}
}
