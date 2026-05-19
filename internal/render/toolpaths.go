package render

import (
	"image"
	"image/color"
	"math"
	"sort"

	"github.com/hajimehoshi/ebiten/v2"

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

// ToolpathDrawer renders sliced layers as ribbon-shaped extrusion
// segments depth-sorted in view space, so upper layers occlude lower
// ones the way they would in a real print preview. Each segment is
// projected to two screen-space endpoints, expanded into a rectangle
// of width = roleStrokeWidth perpendicular to the segment direction,
// and shipped through Ebitengine's DrawTriangles after a back-to-front
// painter's sort.
//
// The previous (per-segment vector.StrokeLine, then per-(layer, role)
// vector.StrokePath) renderers drew flat 2D strokes and so let the
// viewer "see through" to layers on the far side of the model. This
// implementation hands every segment its own view-Z so depth ordering
// is global, not just per-layer — which is what makes the preview feel
// 3D.
type ToolpathDrawer struct {
	// LineWidthPx is the preview stroke thickness in pixels. Real
	// extrusion width is layer-dependent and the path knows its true
	// mm width; for the preview we use a constant pixel width because
	// it reads better at any zoom level.
	LineWidthPx float32

	// Scratch buffers reused across frames.
	quads   []segmentQuad
	verts   []ebiten.Vertex
	indices []uint16
}

// segmentQuad is one extrusion segment ready to draw: the four screen-
// space corners (already coloured) plus a depth key for the painter
// sort. avgZ is in camera view space, where more-negative is further
// from the camera (see [Camera.Project]).
type segmentQuad struct {
	v0, v1, v2, v3 ebiten.Vertex
	avgZ           float32
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

// Draw walks every extrusion segment in layers, projects it to screen
// space, builds a ribbon-shaped quad for it, depth-sorts the whole
// pile in view space, and ships them all through one (or, when the
// uint16 index cap is exceeded, a small handful of) DrawTriangles
// calls. The global depth sort is what gives the preview a tube-like
// feel: closer-to-camera segments paint over further ones regardless
// of which layer they belong to.
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

	d.quads = d.quads[:0]

	for li := range layers {
		layerZ := float32(layers[li].Z)
		for _, p := range layers[li].Paths {
			if !p.Role.IsExtrusion() || len(p.Points) < 2 {
				continue
			}
			sw := roleStrokeWidth(d.LineWidthPx, p.Role)
			halfW := sw * 0.5
			rc := RoleColor(p.Role)
			cr := float32(rc.R) / 255
			cg := float32(rc.G) / 255
			cb := float32(rc.B) / 255

			n := len(p.Points)
			segs := n - 1
			if p.Closed {
				segs = n
			}
			for i := 0; i < segs; i++ {
				a := p.Points[i]
				b := p.Points[(i+1)%n]
				pa := cam.Project(mesh.Vec3{float32(a.X), float32(a.Y), layerZ}, aspect)
				pb := cam.Project(mesh.Vec3{float32(b.X), float32(b.Y), layerZ}, aspect)
				if !pa.InFront || !pb.InFront {
					continue
				}
				ax := x0 + (pa.X+1)*0.5*fw
				ay := y0 + (1-(pa.Y+1)*0.5)*fh
				bx := x0 + (pb.X+1)*0.5*fw
				by := y0 + (1-(pb.Y+1)*0.5)*fh
				dx := bx - ax
				dy := by - ay
				len2 := dx*dx + dy*dy
				if len2 < 1e-8 {
					// Sub-pixel segment — skip rather than emit a
					// degenerate quad whose perpendicular direction
					// is undefined.
					continue
				}
				inv := float32(1.0 / math.Sqrt(float64(len2)))
				// Perpendicular in screen space, scaled to half the
				// stroke width. Rotating (dx, dy) by +90° gives
				// (-dy, dx); multiply by halfW/|d|.
				px := -dy * inv * halfW
				py := dx * inv * halfW
				d.quads = append(d.quads, segmentQuad{
					v0:   makeQuadVertex(ax+px, ay+py, cr, cg, cb),
					v1:   makeQuadVertex(ax-px, ay-py, cr, cg, cb),
					v2:   makeQuadVertex(bx-px, by-py, cr, cg, cb),
					v3:   makeQuadVertex(bx+px, by+py, cr, cg, cb),
					avgZ: (pa.ViewZ + pb.ViewZ) * 0.5,
				})
			}
		}
	}

	if len(d.quads) == 0 {
		return
	}

	// Painter's algorithm: most-negative ViewZ first (furthest from
	// camera) so nearer segments paint over them. Identical scheme to
	// the mesh rasterizer.
	sort.Slice(d.quads, func(i, j int) bool {
		return d.quads[i].avgZ < d.quads[j].avgZ
	})

	// uint16 indices cap at 65535: 6 indices per quad → 10922 quads
	// per draw call. For a Wind-Turbine-class slice (~90k segments)
	// that means ~9 DrawTriangles calls per frame, still trivial.
	const quadsPerBatch = 65535 / 6
	for start := 0; start < len(d.quads); start += quadsPerBatch {
		end := start + quadsPerBatch
		if end > len(d.quads) {
			end = len(d.quads)
		}
		d.emitBatch(dst, d.quads[start:end])
	}
}

// emitBatch packs the given quads into Vertex / index buffers and
// issues a single DrawTriangles call. Buffers are stored on the
// receiver so consecutive frames reuse the same backing array.
func (d *ToolpathDrawer) emitBatch(dst *ebiten.Image, quads []segmentQuad) {
	n := len(quads)
	vNeed := n * 4
	iNeed := n * 6
	if cap(d.verts) < vNeed {
		d.verts = make([]ebiten.Vertex, vNeed)
	} else {
		d.verts = d.verts[:vNeed]
	}
	if cap(d.indices) < iNeed {
		d.indices = make([]uint16, iNeed)
	} else {
		d.indices = d.indices[:iNeed]
	}
	for i, q := range quads {
		vb := i * 4
		ib := i * 6
		d.verts[vb] = q.v0
		d.verts[vb+1] = q.v1
		d.verts[vb+2] = q.v2
		d.verts[vb+3] = q.v3
		// Two triangles: 0-1-2 and 0-2-3.
		d.indices[ib] = uint16(vb)
		d.indices[ib+1] = uint16(vb + 1)
		d.indices[ib+2] = uint16(vb + 2)
		d.indices[ib+3] = uint16(vb)
		d.indices[ib+4] = uint16(vb + 2)
		d.indices[ib+5] = uint16(vb + 3)
	}
	dst.DrawTriangles(d.verts, d.indices, getWhite(), nil)
}

// makeQuadVertex constructs an Ebitengine vertex at the given screen
// coordinate carrying the role's solid colour. The texture sample is
// fixed at (0, 0) of the 1×1 white image so the per-vertex ColorR/G/B
// channels show through unmodulated.
func makeQuadVertex(sx, sy, cr, cg, cb float32) ebiten.Vertex {
	return ebiten.Vertex{
		DstX:   sx,
		DstY:   sy,
		SrcX:   0,
		SrcY:   0,
		ColorR: cr,
		ColorG: cg,
		ColorB: cb,
		ColorA: 1,
	}
}
