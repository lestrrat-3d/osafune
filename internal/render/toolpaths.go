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

	// segs is a scratch buffer reused across rebuilds: the projected,
	// painter-sorted billboards for the current camera. It is rebuilt only
	// when the cache key changes (see below), not every frame.
	segs []segmentBillboard

	// batches holds the finished DrawTriangles payloads (each within the
	// uint16 index cap). Projecting and painter-sorting every segment is
	// the expensive part of a frame, and its result depends only on the
	// camera, the viewport bounds, and which sliced geometry is shown — so
	// we keep the built batches and re-issue them verbatim while that key
	// is unchanged. A static view (the common case once a model is sliced)
	// then costs only the draw calls, which is what stops a
	// million-segment preview from pinning the UI on every redraw.
	batches    []drawBatch
	cacheValid bool
	cacheKey   toolpathCacheKey

	// lastFullSegCount is the segment count from the most recent
	// full-detail (non-decimated) rebuild. It estimates the cost of drawing
	// everything, so a drag keeps full detail — infill included — when the
	// visible geometry is light enough, and only falls back to the
	// walls-only draft when drawing it all would stutter.
	lastFullSegCount int
	// decimationBudget is the full-detail segment count above which a drag
	// switches to the walls-only draft. At/under it, dragging draws
	// everything. Roughly tuned so a drag rebuild stays within a few tens
	// of milliseconds.
	decimationBudget int
}

// drawBatch is one ready-to-issue DrawTriangles payload: up to the uint16
// index cap of segment billboards, already projected and shaded.
type drawBatch struct {
	verts   []ebiten.Vertex
	indices []uint16
}

// toolpathCacheKey identifies a built [ToolpathDrawer.batches] set. geomGen
// is bumped by the viewport whenever the sliced layers or the visible layer
// range change; cam and bounds capture the only other inputs the projection
// depends on. [Camera] is an all-value struct, so == is a complete compare.
type toolpathCacheKey struct {
	geomGen   int
	cam       Camera
	bounds    image.Rectangle
	wallsOnly bool // decimated draft drawn while the camera is moving
}

// segmentBillboard caches one segment's 8 finished screen-space vertices
// plus the painter-sort depth key. The key is the segment's NEAREST
// endpoint depth (largest view Z, i.e. closest to the eye), not the mean of
// the corners: a wall segment is a ribbon tilted in depth, so its mean sits
// behind a flat interior-infill segment at the same pixel and the infill
// wrongly paints over the enclosing wall. Sorting by the nearest point
// makes the outer wall win while still letting a genuinely concave feature
// (e.g. a recess) show its interior. Generated in the projection pass and
// consumed, in sorted order, by the emit pass.
type segmentBillboard struct {
	verts    [segmentVertexCount]ebiten.Vertex
	depthKey float32
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
		LightDir:         normalize(mesh.Vec3{-0.4, -0.5, -0.8}),
		AmbientFactor:    0.4,
		LineWidthPx:      1.5,
		decimationBudget: 150_000,
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

// Draw is the renderer entry point. geomGen identifies the sliced
// geometry + visible layer range (the viewport bumps it on change); when
// it, the camera, and the viewport bounds all match the last frame, the
// already-built batches are re-issued without re-projecting or re-sorting.
// Otherwise the batches are rebuilt: each extrusion segment becomes 8
// screen-space vertices (the OrcaSlicer ribbon template), the segments are
// painter-sorted by mean view Z, and packed into DrawTriangles batches at
// the uint16 index cap.
// interacting signals that the camera is being dragged. The drawer then
// decides whether it can still afford full detail: only when the last
// full-detail rebuild exceeded [ToolpathDrawer.decimationBudget] does a
// drag fall back to the walls-only draft (perimeters, infill skipped).
// So a light view — including one scrubbed to a narrow layer range to
// inspect infill — keeps its infill visible while rotating; only a
// genuinely dense view decimates, and it rebuilds full detail when the
// camera settles.
func (d *ToolpathDrawer) Draw(dst *ebiten.Image, dstBounds image.Rectangle, layers []slice.Layer, cam *Camera, geomGen int, interacting bool) {
	w := dstBounds.Dx()
	h := dstBounds.Dy()
	if w <= 0 || h <= 0 || len(layers) == 0 {
		return
	}
	d.prepare(dstBounds, layers, cam, geomGen, interacting)
	d.drawBatches(dst)
}

// prepare ensures [ToolpathDrawer.batches] is current for the given inputs,
// rebuilding (reproject + sort + pack) only on a cache miss. It returns
// true when a rebuild happened. It does no drawing, so it is exercisable
// without an Ebiten graphics context — Draw is just prepare + drawBatches.
func (d *ToolpathDrawer) prepare(dstBounds image.Rectangle, layers []slice.Layer, cam *Camera, geomGen int, interacting bool) bool {
	// Decimate to walls only while dragging ONLY if the full view is too
	// heavy to redraw smoothly. Below the budget, dragging keeps infill.
	wallsOnly := interacting && d.lastFullSegCount > d.decimationBudget

	key := toolpathCacheKey{geomGen: geomGen, cam: *cam, bounds: dstBounds, wallsOnly: wallsOnly}
	if d.cacheValid && key == d.cacheKey {
		return false
	}
	d.rebuild(dstBounds, layers, cam, wallsOnly)
	if !wallsOnly {
		d.lastFullSegCount = len(d.segs)
	}
	d.cacheKey = key
	d.cacheValid = true
	return true
}

// rebuild reprojects every visible segment for the current camera, painter-
// sorts them, and packs them into [ToolpathDrawer.batches]. This is the
// expensive path; [ToolpathDrawer.Draw] runs it only when the cache key
// changes.
func (d *ToolpathDrawer) rebuild(dstBounds image.Rectangle, layers []slice.Layer, cam *Camera, wallsOnly bool) {
	aspect := float32(dstBounds.Dx()) / float32(dstBounds.Dy())
	x0 := float32(dstBounds.Min.X)
	y0 := float32(dstBounds.Min.Y)
	fw := float32(dstBounds.Dx())
	fh := float32(dstBounds.Dy())

	// Precompute the projection constants once per rebuild instead of
	// recomputing the camera basis (and its trig) for every one of the
	// millions of projected vertices.
	vp := cam.ViewProj(aspect)

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
		// Billboard vertical half-extent: two full layer heights rather than
		// the geometric half (a 4× layer-height ribbon), so each bead heavily
		// overlaps its neighbours. On a curved or sloped surface seen at a
		// near-edge-on (grazing) angle the thin per-layer ribbons project far
		// apart and leave see-through horizontal gaps between layers; this
		// overlap bridges them down to ~2° elevation (one full layer height /
		// 2× overlap was enough at ~6° but not at ~2°). centerZ (above) is
		// unchanged, so beads stay centred on their true layer midpoint.
		billboardHalfH := layerH * 2

		for _, p := range layer.Paths {
			if !p.Role.IsExtrusion() || len(p.Points) < 2 {
				continue
			}
			// Draft pass while the camera moves: walls only, infill skipped.
			if wallsOnly && p.Role != slice.RoleExternalPerimeter && p.Role != slice.RolePerimeter {
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

			// Path-level cull: if the whole path's footprint sits beyond one
			// edge of the viewport we can skip it without projecting any of
			// its segments — the win that makes a zoomed-in view cheap
			// instead of paying to project every off-screen segment just to
			// discard it. Conservative: only skip when every bbox corner is
			// in front of the near plane (so the projection is meaningful)
			// AND they all fall off the same side (so a path larger than the
			// viewport, which surrounds the screen, is never wrongly culled).
			if pathOffscreen(&vp, &p, centerZ, x0, y0, fw, fh) {
				continue
			}

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
				if rec, ok := d.buildSegment(&vp, x0, y0, fw, fh,
					posA, posB, halfW, billboardHalfH, baseR, baseG, baseB); ok {
					d.segs = append(d.segs, rec)
				}
			}
		}
	}

	// Painter sort by each segment's nearest-endpoint depth: furthest
	// (most-negative view Z) first, nearer segments overdraw them.
	sort.Slice(d.segs, func(i, j int) bool {
		return d.segs[i].depthKey < d.segs[j].depthKey
	})

	d.buildBatches()
}

// pathOffscreen reports whether path p's whole footprint lies beyond a
// single edge of the viewport, so all its segments can be skipped without
// projecting them. It projects only the four corners of the path's XY
// bounding box (at the layer's centre Z), not its potentially thousands of
// points. Returns false (do not cull) if any corner is behind the near
// plane, where the projection is unreliable.
func pathOffscreen(vp *ViewProj, p *slice.Path, centerZ, x0, y0, fw, fh float32) bool {
	pts := p.Points
	minX, minY := pts[0].X, pts[0].Y
	maxX, maxY := minX, minY
	for _, q := range pts[1:] {
		if q.X < minX {
			minX = q.X
		} else if q.X > maxX {
			maxX = q.X
		}
		if q.Y < minY {
			minY = q.Y
		} else if q.Y > maxY {
			maxY = q.Y
		}
	}
	corners := [4]mesh.Vec3{
		{float32(minX), float32(minY), centerZ},
		{float32(maxX), float32(minY), centerZ},
		{float32(minX), float32(maxY), centerZ},
		{float32(maxX), float32(maxY), centerZ},
	}
	allLeft, allRight, allAbove, allBelow := true, true, true, true
	for _, c := range corners {
		pr := vp.Project(c)
		if !pr.InFront {
			return false // can't trust the projection; don't cull
		}
		sx := x0 + (pr.X+1)*0.5*fw
		sy := y0 + (1-(pr.Y+1)*0.5)*fh
		allLeft = allLeft && sx < x0
		allRight = allRight && sx > x0+fw
		allAbove = allAbove && sy < y0
		allBelow = allBelow && sy > y0+fh
	}
	return allLeft || allRight || allAbove || allBelow
}

// buildSegment computes the eight projected vertices for one segment and
// returns false when any of them lands behind the near plane OR when the
// segment's whole screen footprint falls outside the viewport rectangle
// (off-screen culling — "only draw what's visible"). Walking through the
// OrcaSlicer vertex shader: pick the horizontal-or-vertical sign table
// based on the camera direction relative to the segment's cross-section
// diagonal, place each vertex at endpoint + signs·half-axis, extend ids 2
// & 7 along the line direction to form the spike caps, then shade
// per-vertex using normalize(pos - endpoint) as a fake smooth normal.
func (d *ToolpathDrawer) buildSegment(
	vp *ViewProj,
	x0, y0, fw, fh float32,
	posA, posB mesh.Vec3,
	halfW, halfH float32,
	baseR, baseG, baseB float32,
) (segmentBillboard, bool) {
	eye := vp.Eye()
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
	// Screen-space bounds of the eight vertices, for off-screen culling.
	minSX, minSY := float32(math.MaxFloat32), float32(math.MaxFloat32)
	maxSX, maxSY := -float32(math.MaxFloat32), -float32(math.MaxFloat32)
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

		pr := vp.Project(pos)
		if !pr.InFront {
			// Anything that pokes behind the near plane drops the
			// whole segment. Robust near-plane clipping is a
			// follow-up; the bounding-box-fit camera keeps the print
			// in front in normal use.
			return segmentBillboard{}, false
		}
		sx := x0 + (pr.X+1)*0.5*fw
		sy := y0 + (1-(pr.Y+1)*0.5)*fh
		if sx < minSX {
			minSX = sx
		}
		if sx > maxSX {
			maxSX = sx
		}
		if sy < minSY {
			minSY = sy
		}
		if sy > maxSY {
			maxSY = sy
		}
		out.verts[vid] = ebiten.Vertex{
			DstX:   sx,
			DstY:   sy,
			ColorR: baseR * shade,
			ColorG: baseG * shade,
			ColorB: baseB * shade,
			ColorA: 1,
		}
	}
	// Off-screen cull: if the segment's whole screen footprint lies beyond
	// any edge of the viewport, it contributes nothing — skip it so the
	// sort and the draw only handle visible geometry. (A zoomed-in or
	// panned view is where this pays off; when the model fits the viewport
	// nothing is culled.)
	if maxSX < x0 || minSX > x0+fw || maxSY < y0 || minSY > y0+fh {
		return segmentBillboard{}, false
	}
	// Painter-sort key: the nearest endpoint depth (largest view Z). See
	// segmentBillboard. Both endpoints are in front of the near plane here
	// (any vertex behind it already returned false above).
	za := vp.Project(posA).ViewZ
	zb := vp.Project(posB).ViewZ
	out.depthKey = za
	if zb > za {
		out.depthKey = zb
	}
	return out, true
}

// buildBatches packs the painter-sorted segments into
// [ToolpathDrawer.batches], each batch holding the 8-vertex / 8-triangle
// templates for up to the uint16 index cap of segments. Existing batch
// slices are reused (resliced to length 0, capacity kept) so a camera drag
// that rebuilds every frame does not churn the allocator; trailing batches
// left over from a larger previous frame are released and trimmed.
func (d *ToolpathDrawer) buildBatches() {
	// uint16 cap: 65535 indices. 24 indices per segment → 2730
	// segments per batch.
	const segsPerBatch = 65535 / (len(segmentTriangles) * 3)

	nb := 0                 // batches used this rebuild
	inBatch := segsPerBatch // force a fresh batch on the first segment
	var b *drawBatch
	for si := range d.segs {
		if inBatch >= segsPerBatch {
			if nb < len(d.batches) {
				b = &d.batches[nb]
				b.verts = b.verts[:0]
				b.indices = b.indices[:0]
			} else {
				// Append first, THEN take the address: append may
				// reallocate d.batches and invalidate an earlier pointer.
				d.batches = append(d.batches, drawBatch{})
				b = &d.batches[nb]
			}
			nb++
			inBatch = 0
		}
		base := uint16(len(b.verts))
		b.verts = append(b.verts, d.segs[si].verts[:]...)
		for _, t := range segmentTriangles {
			b.indices = append(b.indices, base+t[0], base+t[1], base+t[2])
		}
		inBatch++
	}

	// Release references held by now-unused trailing batches so their
	// vertex/index backing arrays can be collected, then trim.
	for i := nb; i < len(d.batches); i++ {
		d.batches[i] = drawBatch{}
	}
	d.batches = d.batches[:nb]
}

// drawBatches issues the cached batches. This is all a static-camera frame
// has to do.
func (d *ToolpathDrawer) drawBatches(dst *ebiten.Image) {
	for i := range d.batches {
		if len(d.batches[i].verts) == 0 {
			continue
		}
		dst.DrawTriangles(d.batches[i].verts, d.batches[i].indices, getWhite(), nil)
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
