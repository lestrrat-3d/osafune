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

// ToolpathDrawer renders sliced layers as volumetric extrusion segments
// — each segment is a world-space oriented box of (Path.Width × Layer.Height
// × segment length) — projected through the camera and painter-sorted
// in view space. That gives the preview the same chunky tube-like feel
// real slicers ship: upper layers occlude lower ones, segments behind
// segments are properly hidden, and the cross-section actually has
// visual mass instead of being a hairline screen-space stroke.
//
// Each segment emits its 4 long-axis faces (top, bottom, two sides);
// end caps are intentionally omitted because adjacent segments along a
// path share the same endpoint, and the cap geometry would just
// interpenetrate with the next segment without adding visual value.
// Painter's algorithm handles inter-face ordering globally — back
// faces of a box have more-negative view Z than front faces, so they
// sort earlier and get overdrawn by the visible front faces in the
// same pass.
type ToolpathDrawer struct {
	// LineWidthPx is retained for backwards compatibility but no
	// longer has any effect: extrusion width now comes from each
	// [slice.Path]'s Width field (the real mm value the slicer
	// computed), so zooming in genuinely shows fatter strokes.
	LineWidthPx float32

	// Scratch buffers reused across frames.
	faces   []faceQuad
	verts   []ebiten.Vertex
	indices []uint16
}

// faceQuad is one rectangular face of a segment's bounding box, ready
// to draw: four screen-space corners (already coloured + shaded) and a
// painter-sort key. avgZ is in camera view space — more-negative is
// further from the camera (see [Camera.Project]).
type faceQuad struct {
	v0, v1, v2, v3 ebiten.Vertex
	avgZ           float32
}

// NewToolpathDrawer returns a drawer with a default stroke baseline.
// LineWidthPx is no longer used by the volumetric renderer; the field
// is kept on the struct so callers that set it before [Draw] continue
// to compile.
func NewToolpathDrawer() *ToolpathDrawer { return &ToolpathDrawer{LineWidthPx: 1.5} }

// Per-face shade factors. The top face is the brightest because the
// virtual key light points down-and-toward-the-camera; sides take a
// mid-tone so the eye can read each segment as a 3D body; the bottom
// is darkest so an upside-down camera doesn't look identical to a
// right-side-up one. The values are calibrated by eye against
// OrcaSlicer's preview at a default viewing angle.
const (
	shadeTop    = 1.00
	shadeSide   = 0.70
	shadeBottom = 0.40
)

// minExtrusionWidth and minLayerHeight clamp degenerate path metadata
// (e.g. travel moves or zero-width init paths) so we never produce a
// flat box that disappears at certain angles.
const (
	minExtrusionWidth = 0.05 // mm
	minLayerHeight    = 0.05 // mm
)

// Draw walks every extrusion segment in layers, builds the 4-face
// bounding box for each segment in world space, projects all 8 unique
// corners through cam, and ships the resulting screen-space faces
// through ebiten.Image.DrawTriangles after a global painter's sort.
//
// The per-frame work is bounded by O(segments × 8 projections + faces ×
// log faces). For a Wind-Turbine-class slice that's ~90k segments →
// ~720k projections and ~360k face sorts; comfortably real-time on a
// modern desktop.
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

	d.faces = d.faces[:0]

	for li := range layers {
		layer := &layers[li]
		layerZ := float32(layer.Z)
		layerH := float32(layer.Height)
		if layerH < minLayerHeight {
			layerH = minLayerHeight
		}
		halfH := layerH * 0.5
		zTop := layerZ
		zBot := layerZ - layerH // Layer.Z is top-of-layer; the layer
		// actually occupies [Z-Height, Z]. Drawing the box between
		// those planes makes adjacent layers' boxes meet at exactly
		// the same Z, so there's no vertical gap to see through.
		_ = halfH

		for _, p := range layer.Paths {
			if !p.Role.IsExtrusion() || len(p.Points) < 2 {
				continue
			}
			extrW := float32(p.Width)
			if extrW < minExtrusionWidth {
				extrW = minExtrusionWidth
			}
			halfW := extrW * 0.5

			rc := RoleColor(p.Role)
			baseR := float32(rc.R) / 255
			baseG := float32(rc.G) / 255
			baseB := float32(rc.B) / 255

			n := len(p.Points)
			segs := n - 1
			if p.Closed {
				segs = n
			}
			for i := 0; i < segs; i++ {
				a := p.Points[i]
				b := p.Points[(i+1)%n]
				ax := float32(a.X)
				ay := float32(a.Y)
				bx := float32(b.X)
				by := float32(b.Y)
				dx := bx - ax
				dy := by - ay
				len2 := dx*dx + dy*dy
				if len2 < 1e-12 {
					continue
				}
				inv := float32(1.0 / math.Sqrt(float64(len2)))
				// Perpendicular in the XY plane, length = halfW.
				perpX := -dy * inv * halfW
				perpY := dx * inv * halfW

				// Eight world-space corners. Naming: A/B = which path
				// endpoint, m/p = minus/plus perpendicular side,
				// t/b = top/bottom in Z.
				amt := cam.Project(mesh.Vec3{ax - perpX, ay - perpY, zTop}, aspect)
				amb := cam.Project(mesh.Vec3{ax - perpX, ay - perpY, zBot}, aspect)
				apt := cam.Project(mesh.Vec3{ax + perpX, ay + perpY, zTop}, aspect)
				apb := cam.Project(mesh.Vec3{ax + perpX, ay + perpY, zBot}, aspect)
				bmt := cam.Project(mesh.Vec3{bx - perpX, by - perpY, zTop}, aspect)
				bmb := cam.Project(mesh.Vec3{bx - perpX, by - perpY, zBot}, aspect)
				bpt := cam.Project(mesh.Vec3{bx + perpX, by + perpY, zTop}, aspect)
				bpb := cam.Project(mesh.Vec3{bx + perpX, by + perpY, zBot}, aspect)
				if !amt.InFront || !amb.InFront || !apt.InFront || !apb.InFront ||
					!bmt.InFront || !bmb.InFront || !bpt.InFront || !bpb.InFront {
					// Any vertex behind the near plane → drop the
					// whole box. Robust clipping is a follow-up; the
					// bounding-box fit keeps the print in front in
					// normal use.
					continue
				}

				// Project to screen-space vertex helpers.
				vAmt := projToVertex(amt, x0, y0, fw, fh, baseR, baseG, baseB)
				vAmb := projToVertex(amb, x0, y0, fw, fh, baseR, baseG, baseB)
				vApt := projToVertex(apt, x0, y0, fw, fh, baseR, baseG, baseB)
				vApb := projToVertex(apb, x0, y0, fw, fh, baseR, baseG, baseB)
				vBmt := projToVertex(bmt, x0, y0, fw, fh, baseR, baseG, baseB)
				vBmb := projToVertex(bmb, x0, y0, fw, fh, baseR, baseG, baseB)
				vBpt := projToVertex(bpt, x0, y0, fw, fh, baseR, baseG, baseB)
				vBpb := projToVertex(bpb, x0, y0, fw, fh, baseR, baseG, baseB)

				// Top face (+Z): brightest.
				d.faces = append(d.faces, faceQuad{
					v0:   shade(vAmt, shadeTop),
					v1:   shade(vBmt, shadeTop),
					v2:   shade(vBpt, shadeTop),
					v3:   shade(vApt, shadeTop),
					avgZ: (amt.ViewZ + bmt.ViewZ + bpt.ViewZ + apt.ViewZ) * 0.25,
				})
				// Bottom face (-Z): darkest.
				d.faces = append(d.faces, faceQuad{
					v0:   shade(vAmb, shadeBottom),
					v1:   shade(vApb, shadeBottom),
					v2:   shade(vBpb, shadeBottom),
					v3:   shade(vBmb, shadeBottom),
					avgZ: (amb.ViewZ + apb.ViewZ + bpb.ViewZ + bmb.ViewZ) * 0.25,
				})
				// −perp side face.
				d.faces = append(d.faces, faceQuad{
					v0:   shade(vAmb, shadeSide),
					v1:   shade(vAmt, shadeSide),
					v2:   shade(vBmt, shadeSide),
					v3:   shade(vBmb, shadeSide),
					avgZ: (amb.ViewZ + amt.ViewZ + bmt.ViewZ + bmb.ViewZ) * 0.25,
				})
				// +perp side face.
				d.faces = append(d.faces, faceQuad{
					v0:   shade(vApb, shadeSide),
					v1:   shade(vBpb, shadeSide),
					v2:   shade(vBpt, shadeSide),
					v3:   shade(vApt, shadeSide),
					avgZ: (apb.ViewZ + bpb.ViewZ + bpt.ViewZ + apt.ViewZ) * 0.25,
				})
			}
		}
	}

	if len(d.faces) == 0 {
		return
	}

	// Painter's algorithm: most-negative view Z (furthest from
	// camera) first, so nearer faces overdraw them. Matches the mesh
	// rasterizer's scheme.
	sort.Slice(d.faces, func(i, j int) bool {
		return d.faces[i].avgZ < d.faces[j].avgZ
	})

	// uint16 index cap: 6 indices per quad → 10922 quads per draw.
	const quadsPerBatch = 65535 / 6
	for start := 0; start < len(d.faces); start += quadsPerBatch {
		end := start + quadsPerBatch
		if end > len(d.faces) {
			end = len(d.faces)
		}
		d.emitBatch(dst, d.faces[start:end])
	}
}

// emitBatch packs the given face quads into Vertex / index buffers and
// issues a single DrawTriangles call. Buffers are stored on the
// receiver so consecutive frames reuse the same backing array.
func (d *ToolpathDrawer) emitBatch(dst *ebiten.Image, quads []faceQuad) {
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

// projToVertex maps a camera projection result to an Ebitengine
// screen-space vertex carrying a base colour. Flips Y because NDC is
// OpenGL-style (Y up) and screen Y points down.
func projToVertex(p Projected, x0, y0, fw, fh, cr, cg, cb float32) ebiten.Vertex {
	return ebiten.Vertex{
		DstX:   x0 + (p.X+1)*0.5*fw,
		DstY:   y0 + (1-(p.Y+1)*0.5)*fh,
		SrcX:   0,
		SrcY:   0,
		ColorR: cr,
		ColorG: cg,
		ColorB: cb,
		ColorA: 1,
	}
}

// shade multiplies the vertex's RGB channels by s in place. Used to
// brighten the top face / darken sides + bottom so a segment reads as
// a 3D box rather than a flat ribbon.
func shade(v ebiten.Vertex, s float32) ebiten.Vertex {
	v.ColorR *= s
	v.ColorG *= s
	v.ColorB *= s
	return v
}
