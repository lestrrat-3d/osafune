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

// ToolpathDrawer renders sliced layers using the same billboard trick
// OrcaSlicer's GCode viewer uses (see libvgcode/SegmentTemplate.cpp +
// Shaders.hpp in the upstream OrcaSlicer source): each extrusion
// segment is an 8-vertex / 8-triangle camera-facing ribbon with a
// boat-shaped silhouette — three "ring" corners (top, near side,
// bottom) at each endpoint plus a pointed spike that extends along
// the line direction. The spike hides the join between consecutive
// segments along a path; the camera-facing flip keeps the ribbon
// presenting its broad face to the viewer from any angle.
//
// The 3D illusion comes from per-vertex Lambertian shading using a
// normal derived from (vertex - endpoint), interpolated across the
// triangle. The top corner is lit, the bottom is dark, sides fall
// in between — same trick the OrcaSlicer GPU shader uses, just done
// CPU-side because ebiten doesn't expose programmable vertex shaders.
type ToolpathDrawer struct {
	// LightDir is the unit direction the virtual key light shines
	// toward. Same convention as the mesh rasterizer's LightDir
	// (shade = ambient + (1-ambient) · max(0, n · -LightDir)).
	LightDir mesh.Vec3

	// AmbientFactor in [0, 1] keeps the shadow side of a segment
	// readable rather than going to pure black.
	AmbientFactor float32

	// LineWidthPx is retained on the struct so existing callers that
	// assign to it continue to compile; the volumetric billboard
	// renderer does not consult it any more (extrusion width comes
	// straight from each [slice.Path]'s mm-space Width field).
	LineWidthPx float32

	// Scratch buffers reused across frames.
	segs    []segmentBillboard
	verts   []ebiten.Vertex
	indices []uint16
}

// segmentBillboard caches one segment's 8 finished screen-space
// vertices plus the painter-sort depth key (mean view Z of the eight
// corners). Generated in the per-frame projection pass and consumed,
// in sorted order, by the emit pass.
type segmentBillboard struct {
	verts [segmentVertexCount]ebiten.Vertex
	avgZ  float32
}

// segmentVertexCount is the per-segment vertex count of OrcaSlicer's
// template. Eight vertices, eight triangles — see segmentTriangles.
const segmentVertexCount = 8

// segmentTriangles is the index pattern from
// libvgcode/SegmentTemplate.cpp (VERTEX_DATA). Eight triangles: two
// front-spike fans, four body, two back-spike fans. Indices here are
// the local 0–7 offsets; the emit loop adds the segment's base
// vertex index to each.
var segmentTriangles = [8][3]uint16{
	{0, 1, 2}, // front spike
	{0, 2, 3}, // front spike
	{0, 3, 4}, // right/bottom body
	{0, 4, 5}, // right/bottom body
	{0, 5, 6}, // left/top body
	{0, 6, 1}, // left/top body
	{5, 4, 7}, // back spike
	{5, 7, 6}, // back spike
}

// horizontalViewSigns / verticalViewSigns are the
// horizontal_vertical_view_signs_array constants from the OrcaSlicer
// vertex shader (libvgcode/Shaders.hpp). Each pair is (right_sign,
// up_sign): the multipliers applied to halfWidth·lineRight and
// halfHeight·lineUp when placing a vertex relative to its endpoint.
// Vertex ids 2 and 7 sit at the endpoint itself (signs 0,0) and get
// extended into a spike along ±lineDir by the geometry pass.
var (
	horizontalViewSigns = [segmentVertexCount][2]float32{
		{1, 0},
		{0, 1},
		{0, 0},
		{0, -1},
		{0, -1},
		{1, 0},
		{0, 1},
		{0, 0},
	}
	verticalViewSigns = [segmentVertexCount][2]float32{
		{0, 1},
		{-1, 0},
		{0, 0},
		{1, 0},
		{1, 0},
		{0, 1},
		{-1, 0},
		{0, 0},
	}
)

// NewToolpathDrawer returns a drawer with the same key-light / ambient
// values as the mesh rasterizer, so the toolpath preview and the mesh
// preview shade consistently when the user toggles between them.
func NewToolpathDrawer() *ToolpathDrawer {
	return &ToolpathDrawer{
		LightDir:      normalize(mesh.Vec3{-0.4, -0.5, -0.8}),
		AmbientFactor: 0.4,
		LineWidthPx:   1.5,
	}
}

// minExtrusionWidth and minLayerHeight clamp degenerate path metadata
// so a zero-dimensioned path can't produce a sliver billboard that
// disappears or projects to NaN at edge-on viewing angles.
const (
	minExtrusionWidth = 0.05 // mm
	minLayerHeight    = 0.05 // mm
)

// worldUp is the slicer's Z-up convention. line_up direction
// degenerates to this for any segment lying in an XY plane, which is
// the only kind of segment our planar slicer emits — but the cross-
// product fallback below also handles a hypothetical vertical move.
var worldUp = mesh.Vec3{0, 0, 1}

// Draw is the renderer entry point. For each extrusion segment in
// layers it computes 8 screen-space vertices (the OrcaSlicer ribbon
// template), painter-sorts segments by mean view Z, and ships them
// through ebiten.DrawTriangles batched at the uint16 index cap.
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

	eye, _, _, _ := cam.Basis()

	d.segs = d.segs[:0]

	for li := range layers {
		layer := &layers[li]
		layerH := float32(layer.Height)
		if layerH < minLayerHeight {
			layerH = minLayerHeight
		}
		halfH := layerH * 0.5
		// Layer.Z is the top of the layer; the bead occupies
		// [Z-Height, Z]. Centring the billboard on the midpoint
		// makes adjacent layers' geometry meet at the exact layer
		// boundary so they appear continuous in Z.
		centerZ := float32(layer.Z) - halfH

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
				posA := mesh.Vec3{float32(a.X), float32(a.Y), centerZ}
				posB := mesh.Vec3{float32(b.X), float32(b.Y), centerZ}
				if rec, ok := d.buildSegment(cam, eye, aspect, x0, y0, fw, fh,
					posA, posB, halfW, halfH, baseR, baseG, baseB); ok {
					d.segs = append(d.segs, rec)
				}
			}
		}
	}

	if len(d.segs) == 0 {
		return
	}

	// Painter sort: furthest segment first (most-negative view Z),
	// nearer segments overdraw them.
	sort.Slice(d.segs, func(i, j int) bool {
		return d.segs[i].avgZ < d.segs[j].avgZ
	})

	d.emit(dst)
}

// buildSegment computes the eight projected vertices for one segment
// and returns false when any of them lands behind the near plane.
// Walking through the OrcaSlicer vertex shader: pick the
// horizontal-or-vertical sign table based on the camera direction
// relative to the segment's cross-section diagonal, place each
// vertex at endpoint + signs·half-axis, extend ids 2 & 7 along the
// line direction to form the spike caps, then shade per-vertex
// using normalize(pos - endpoint) as a fake smooth normal.
func (d *ToolpathDrawer) buildSegment(
	cam *Camera, eye mesh.Vec3, aspect float32,
	x0, y0, fw, fh float32,
	posA, posB mesh.Vec3,
	halfW, halfH float32,
	baseR, baseG, baseB float32,
) (segmentBillboard, bool) {
	lineX := posB[0] - posA[0]
	lineY := posB[1] - posA[1]
	lineZ := posB[2] - posA[2]
	lineLen := float32(math.Sqrt(float64(lineX*lineX + lineY*lineY + lineZ*lineZ)))
	if lineLen < 1e-6 {
		return segmentBillboard{}, false
	}
	lineDir := mesh.Vec3{lineX / lineLen, lineY / lineLen, lineZ / lineLen}

	// line_right ⟂ line_dir in (roughly) the XY plane. For a
	// nearly-vertical line, fall back to a fixed reference axis the
	// way the OrcaSlicer shader does.
	var lineRight mesh.Vec3
	if absF(dot3(lineDir, worldUp)) > 0.9 {
		lineRight = norm3(cross3(mesh.Vec3{1, 0, 0}, lineDir))
	} else {
		lineRight = norm3(cross3(lineDir, worldUp))
	}
	lineUp := norm3(cross3(lineRight, lineDir))

	// diagonal_dir_border = unit vector at angle atan2(W, H) in the
	// (line_right, line_up) plane scaled by the cross-section's
	// half-extents. Used as the "tilt" reference for the
	// horizontal-vs-vertical view test.
	diagX := halfH*2*lineUp[0] + halfW*2*lineRight[0]
	diagY := halfH*2*lineUp[1] + halfW*2*lineRight[1]
	diagZ := halfH*2*lineUp[2] + halfW*2*lineRight[2]
	diagLen := float32(math.Sqrt(float64(diagX*diagX + diagY*diagY + diagZ*diagZ)))
	if diagLen < 1e-6 {
		return segmentBillboard{}, false
	}
	diagDir := mesh.Vec3{diagX / diagLen, diagY / diagLen, diagZ / diagLen}

	segCenter := mesh.Vec3{
		(posA[0] + posB[0]) * 0.5,
		(posA[1] + posB[1]) * 0.5,
		(posA[2] + posB[2]) * 0.5,
	}
	viewDir := norm3(mesh.Vec3{
		segCenter[0] - eye[0],
		segCenter[1] - eye[1],
		segCenter[2] - eye[2],
	})

	// Compare camera projection onto line_up vs line_right,
	// normalised by the diagonal's projection, to pick the
	// orientation that maximises the billboard's silhouette.
	denomUp := absF(dot3(diagDir, lineUp))
	denomRt := absF(dot3(diagDir, lineRight))
	isVertical := false
	if denomUp > 1e-6 && denomRt > 1e-6 {
		isVertical = absF(dot3(viewDir, lineUp))/denomUp >
			absF(dot3(viewDir, lineRight))/denomRt
	}
	signs := &horizontalViewSigns
	if isVertical {
		signs = &verticalViewSigns
	}

	negView := mesh.Vec3{-viewDir[0], -viewDir[1], -viewDir[2]}
	viewRightSign := signF(dot3(negView, lineRight))
	viewTopSign := signF(dot3(negView, lineUp))
	if viewRightSign == 0 {
		viewRightSign = 1
	}
	if viewTopSign == 0 {
		viewTopSign = 1
	}

	horizontalDir := mesh.Vec3{lineRight[0] * halfW, lineRight[1] * halfW, lineRight[2] * halfW}
	verticalDir := mesh.Vec3{lineUp[0] * halfH, lineUp[1] * halfH, lineUp[2] * halfH}

	var out segmentBillboard
	var sumViewZ float32
	for vid := 0; vid < segmentVertexCount; vid++ {
		endpoint := posA
		if vid >= 4 {
			endpoint = posB
		}
		s := signs[vid]
		hSign := s[0] * viewRightSign
		vSign := s[1] * viewTopSign

		pos := mesh.Vec3{
			endpoint[0] + hSign*horizontalDir[0] + vSign*verticalDir[0],
			endpoint[1] + hSign*horizontalDir[1] + vSign*verticalDir[1],
			endpoint[2] + hSign*horizontalDir[2] + vSign*verticalDir[2],
		}
		// Spike vertices (ids 2 and 7) are offset along the line
		// direction so the segment tapers to a point at each end.
		// Adjacent segments' spikes overlap, which hides the join
		// without needing the GPU shader's miter math.
		if vid == 2 || vid == 7 {
			sign := float32(-1)
			if vid == 7 {
				sign = 1
			}
			pos[0] += sign * halfW * lineDir[0]
			pos[1] += sign * halfW * lineDir[1]
			pos[2] += sign * halfW * lineDir[2]
		}

		// Fake smooth normal: from the segment axis (endpoint)
		// outward through the vertex. Matches the OrcaSlicer shader's
		// `normalize(pos - endpoint_pos)` so the Gouraud-style
		// gradient across each face reads as a curved tube.
		nx := pos[0] - endpoint[0]
		ny := pos[1] - endpoint[1]
		nz := pos[2] - endpoint[2]
		nLen := float32(math.Sqrt(float64(nx*nx + ny*ny + nz*nz)))
		if nLen < 1e-6 {
			// Spike at endpoint: use line_dir as a stand-in normal
			// so the spike tip is shaded with the segment-axis
			// orientation rather than going to zero.
			sign := float32(-1)
			if vid == 7 {
				sign = 1
			}
			nx = sign * lineDir[0]
			ny = sign * lineDir[1]
			nz = sign * lineDir[2]
		} else {
			nx /= nLen
			ny /= nLen
			nz /= nLen
		}

		ndotL := -(nx*d.LightDir[0] + ny*d.LightDir[1] + nz*d.LightDir[2])
		if ndotL < 0 {
			ndotL = 0
		}
		shade := d.AmbientFactor + (1-d.AmbientFactor)*ndotL

		pr := cam.Project(pos, aspect)
		if !pr.InFront {
			// Anything that pokes behind the near plane drops the
			// whole segment. Robust near-plane clipping is a
			// follow-up; the bounding-box-fit camera keeps the print
			// in front in normal use.
			return segmentBillboard{}, false
		}
		out.verts[vid] = ebiten.Vertex{
			DstX:   x0 + (pr.X+1)*0.5*fw,
			DstY:   y0 + (1-(pr.Y+1)*0.5)*fh,
			ColorR: baseR * shade,
			ColorG: baseG * shade,
			ColorB: baseB * shade,
			ColorA: 1,
		}
		sumViewZ += pr.ViewZ
	}
	out.avgZ = sumViewZ / segmentVertexCount
	return out, true
}

// emit walks the segments in painter-sorted order and ships their
// 8-vertex / 8-triangle templates through DrawTriangles, flushing the
// shared vertex / index buffer whenever the next segment would push
// us past the uint16 index cap.
func (d *ToolpathDrawer) emit(dst *ebiten.Image) {
	// uint16 cap: 65535 indices. 24 indices per segment → 2730
	// segments per batch.
	const segsPerBatch = 65535 / (len(segmentTriangles) * 3)

	d.verts = d.verts[:0]
	d.indices = d.indices[:0]
	inBatch := 0
	for si := range d.segs {
		if inBatch == segsPerBatch {
			dst.DrawTriangles(d.verts, d.indices, getWhite(), nil)
			d.verts = d.verts[:0]
			d.indices = d.indices[:0]
			inBatch = 0
		}
		base := uint16(len(d.verts))
		d.verts = append(d.verts, d.segs[si].verts[:]...)
		for _, t := range segmentTriangles {
			d.indices = append(d.indices, base+t[0], base+t[1], base+t[2])
		}
		inBatch++
	}
	if len(d.verts) > 0 {
		dst.DrawTriangles(d.verts, d.indices, getWhite(), nil)
	}
}

// Small Vec3 helpers kept local to this file to avoid bloating the
// mesh package with renderer-specific conveniences. All operate on
// the package-shared [3]float32 representation.

func dot3(a, b mesh.Vec3) float32 {
	return a[0]*b[0] + a[1]*b[1] + a[2]*b[2]
}

func cross3(a, b mesh.Vec3) mesh.Vec3 {
	return mesh.Vec3{
		a[1]*b[2] - a[2]*b[1],
		a[2]*b[0] - a[0]*b[2],
		a[0]*b[1] - a[1]*b[0],
	}
}

func norm3(v mesh.Vec3) mesh.Vec3 {
	l := float32(math.Sqrt(float64(v[0]*v[0] + v[1]*v[1] + v[2]*v[2])))
	if l < 1e-12 {
		return mesh.Vec3{}
	}
	return mesh.Vec3{v[0] / l, v[1] / l, v[2] / l}
}

func absF(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func signF(x float32) float32 {
	if x > 0 {
		return 1
	}
	if x < 0 {
		return -1
	}
	return 0
}
