package render

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/lestrrat-go/makislicer/internal/mesh"
	"github.com/lestrrat-go/makislicer/internal/slice"
)

// RoleColor maps a path role to a stable preview colour. The palette
// follows OrcaSlicer's defaults closely so the visual distinction
// between outer walls (red), inner walls (green), sparse infill
// (amber) and solid infill (cyan) reads immediately to anyone who has
// used OrcaSlicer / Bambu Studio.
func RoleColor(r slice.PathRole) color.NRGBA {
	switch r {
	case slice.RoleExternalPerimeter:
		return color.NRGBA{0xe6, 0x2b, 0x2b, 0xff} // outer wall — red
	case slice.RolePerimeter:
		return color.NRGBA{0x2e, 0xa4, 0x4f, 0xff} // inner walls — green
	case slice.RoleInfill:
		return color.NRGBA{0xe0, 0xa8, 0x1f, 0xff} // sparse infill — amber
	case slice.RoleSolidInfill:
		return color.NRGBA{0x1f, 0x8a, 0xc9, 0xff} // solid infill — cyan
	}
	return color.NRGBA{0x60, 0x60, 0x60, 0x40}
}

// LegendEntries returns the role/colour pairs the preview uses, in the
// order they should appear in a UI legend (outer wall first, then
// inner, then fill kinds). The slice is freshly allocated so callers
// can mutate it freely.
func LegendEntries() []LegendEntry {
	return []LegendEntry{
		{Role: slice.RoleExternalPerimeter, Color: RoleColor(slice.RoleExternalPerimeter), Label: "Outer wall"},
		{Role: slice.RolePerimeter, Color: RoleColor(slice.RolePerimeter), Label: "Inner wall"},
		{Role: slice.RoleInfill, Color: RoleColor(slice.RoleInfill), Label: "Sparse infill"},
		{Role: slice.RoleSolidInfill, Color: RoleColor(slice.RoleSolidInfill), Label: "Solid infill"},
	}
}

// LegendEntry is one row in the toolpath-preview legend: a role, the
// colour the previewer draws it in, and a human-readable label.
type LegendEntry struct {
	Role  slice.PathRole
	Color color.NRGBA
	Label string
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

// roleStrokeWidth returns the on-screen stroke width for a given role.
// Outer walls render thicker than the inner walls so the user can read
// the wall structure even when the colours are hard to tell apart on a
// busy preview (e.g. zoomed all the way out, or printed to a screenshot
// at a low colour depth).
func roleStrokeWidth(base float32, r slice.PathRole) float32 {
	switch r {
	case slice.RoleExternalPerimeter:
		return base * 1.6
	case slice.RolePerimeter:
		return base * 1.1
	case slice.RoleSolidInfill:
		return base
	}
	return base * 0.9
}

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
			c := RoleColor(p.Role)
			sw := roleStrokeWidth(d.LineWidthPx, p.Role)
			prev := project(cam, layers[li].Z, p.Points[0], aspect, x0, y0, fw, fh)
			for i := 1; i < len(p.Points); i++ {
				cur := project(cam, layers[li].Z, p.Points[i], aspect, x0, y0, fw, fh)
				if prev.ok && cur.ok {
					vector.StrokeLine(dst, prev.sx, prev.sy, cur.sx, cur.sy, sw, c, false)
				}
				prev = cur
			}
			if p.Closed && len(p.Points) >= 2 {
				cur := project(cam, layers[li].Z, p.Points[0], aspect, x0, y0, fw, fh)
				if prev.ok && cur.ok {
					vector.StrokeLine(dst, prev.sx, prev.sy, cur.sx, cur.sy, sw, c, false)
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
