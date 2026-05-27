package render

import (
	"image"
	"image/color"
	"math"
	"runtime"
	"sync"

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
		return color.NRGBA{0xd9, 0x5c, 0x4f, 0xff} // outer wall — soft red
	case slice.RolePerimeter:
		return color.NRGBA{0x4f, 0xa8, 0x6b, 0xff} // inner walls — muted green
	case slice.RoleInfill:
		return color.NRGBA{0xd9, 0xa3, 0x4a, 0xff} // sparse infill — warm amber
	case slice.RoleSolidInfill:
		return color.NRGBA{0x4a, 0x8f, 0xc2, 0xff} // solid infill — muted blue
	case slice.RoleSkirtBrim:
		return color.NRGBA{0x8a, 0xb0, 0x6f, 0xff} // skirt/brim — sage green
	case slice.RoleBridge:
		return color.NRGBA{0x3f, 0x6f, 0xb0, 0xff} // bridge — deep blue
	}
	return color.NRGBA{0x80, 0x86, 0x8e, 0xff}
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
		{Role: slice.RoleSkirtBrim, Color: RoleColor(slice.RoleSkirtBrim), Label: "Skirt/Brim"},
		{Role: slice.RoleBridge, Color: RoleColor(slice.RoleBridge), Label: "Bridge"},
	}
}

// LegendEntry is one row in the toolpath-preview legend: a role, the
// colour the previewer draws it in, and a human-readable label.
type LegendEntry struct {
	Role  slice.PathRole
	Color color.NRGBA
	Label string
}

// ToolpathDrawer renders sliced layers as true 3D extrusion volumes with a
// software z-buffer, the way OrcaSlicer's GCode viewer does on the GPU. Each
// extrusion segment becomes a box (line width × layer height, swept along
// the path); the boxes are projected, back-face culled, and rasterized with
// per-pixel depth into an offscreen image, which is then blitted to screen.
//
// A real z-buffer (rather than the old camera-facing billboards + painter's
// sort) is what gives faithful occlusion: the boxes tile in 3D by
// construction, so adjacent beads and layers never leave see-through gaps,
// and interior infill is correctly hidden behind the walls that enclose it
// while genuinely concave features still show.
//
// Ebiten has no GPU depth buffer, so the rasterizer is CPU-side and
// parallel. The rendered image is cached and only rebuilt when the camera,
// viewport, or sliced geometry changes (see [toolpathCacheKey]); a static
// view costs only a blit. While the camera is being dragged the frame is
// rendered at a reduced resolution and upscaled, to keep an orbit responsive.
type ToolpathDrawer struct {
	// LightDir is the unit direction the virtual key light shines toward.
	LightDir mesh.Vec3
	// FillDir is the unit direction of a dimmer secondary light, roughly
	// opposite the key, that lifts the shadowed side so it stays legible.
	FillDir mesh.Vec3
	// AmbientFactor in [0,1] keeps shadowed faces readable.
	AmbientFactor float32
	// LineWidthPx is retained so existing callers that assign to it still
	// compile; the volumetric renderer takes width from each path instead.
	LineWidthPx float32

	// target holds the most recently rendered frame; a cache hit blits it.
	target *ebiten.Image
	// rgba, zbuf and nbuf are the reused color (premultiplied RGBA), depth,
	// and world-normal (3 floats/pixel) buffers the rasterizer writes into.
	// They are sized at the internal (possibly supersampled) render
	// resolution. out is the screen-resolution premultiplied RGBA that the
	// supersample downsample writes and that is uploaded to target.
	rgba []byte
	zbuf []float32
	nbuf []float32
	out  []byte
	// tris is the projected triangle list rasterized each frame (shell
	// projection plus, in a cutaway, the interior beads). triBufs are the
	// per-worker buffers the parallel bead projection fills.
	tris    []rtri
	triBufs [][]rtri

	// worldSlab is the camera-independent solid wall shell (built once per
	// geometry change, keyed by slabGeomGen); slabBufs are the per-worker
	// buffers projectSlab fills.
	worldSlab   []wtri
	slabGeomGen int
	slabBufs    [][]rtri

	cacheValid bool
	cacheKey   toolpathCacheKey
}

// dragRenderScale shrinks the render resolution while the camera is being
// dragged, so an orbit stays responsive on a software rasterizer; the full
// resolution is restored when the camera settles. The low-res frame is
// upscaled on blit (slightly soft while moving, crisp once still). The solid
// wall shell is cached (built once per geometry change), so a drag only pays
// projection + rasterization and can afford a fairly high scale.
const dragRenderScale = 0.75

// settleSupersample is the supersample factor applied once the camera settles:
// the frame is rasterized (and SSAO'd) at ss× the output resolution and
// box-downsampled, which anti-aliases the bead/layer edges that otherwise
// stair-step. While dragging, ss drops to 1 (and SSAO is skipped) so an orbit
// stays responsive.
const settleSupersample = 2

// toolpathCacheKey identifies a rendered frame. geomGen is bumped by the
// viewport when the sliced layers or visible layer range change; cam, the
// output dimensions (ow×oh — reduced while dragging) and the supersample
// factor ss are the remaining inputs the rendered pixels depend on. [Camera]
// is an all-value struct, so == is a complete compare.
type toolpathCacheKey struct {
	geomGen        int
	cam            Camera
	ow, oh, ss     int
	topCut, botCut bool
}

// rtri is a screen-space triangle ready to rasterize: three (x, y, viewZ)
// vertices, the world-space face normal (written per-pixel into the normal
// buffer for the SSAO pass), and a packed 0xRRGGBB colour (already shaded).
// Larger viewZ is nearer the eye.
type rtri struct {
	ax, ay, az float32
	bx, by, bz float32
	cx, cy, cz float32
	nx, ny, nz float32
	col        uint32
}

// boxFaces indexes the 12 triangles (6 quads) of an extrusion box into its
// 8 corners: 0–3 are the start cap (−right−up, +right−up, +right+up,
// −right+up), 4–7 the matching end cap.
var boxFaces = [12][3]int{
	{0, 1, 2}, {0, 2, 3}, // start cap
	{4, 6, 5}, {4, 7, 6}, // end cap
	{0, 4, 5}, {0, 5, 1}, // bottom
	{3, 2, 6}, {3, 6, 7}, // top
	{0, 3, 7}, {0, 7, 4}, // left
	{1, 5, 6}, {1, 6, 2}, // right
}

const (
	minExtrusionWidth = 0.05 // mm
	minLayerHeight    = 0.05 // mm
)

var worldUp = mesh.Vec3{0, 0, 1}

// NewToolpathDrawer returns a drawer with the same key-light / ambient
// values as the mesh rasterizer, so the toolpath preview and the mesh
// preview shade consistently when the user toggles between them.
func NewToolpathDrawer() *ToolpathDrawer {
	return &ToolpathDrawer{
		LightDir:      norm3(mesh.Vec3{-0.4, -0.5, -0.8}),
		FillDir:       norm3(mesh.Vec3{0.6, 0.5, -0.25}),
		AmbientFactor: 0.3,
		LineWidthPx:   1.5,
		slabGeomGen:   -1, // force a build on the first thick render
	}
}

// Draw renders the visible layers' toolpaths to dst within dstBounds.
// geomGen identifies the sliced geometry + visible range; interacting
// signals the camera is being dragged; topCut/botCut say whether the top or
// bottom of the visible range is a cut (the slider was narrowed there), in
// which case that boundary layer's toolpaths are drawn on the exposed cut
// face while the sides stay a solid wall shell. The rendered frame is
// cached: when geomGen, the camera, the render resolution, and the cut flags
// all match the last call, the cached image is blitted without re-rendering.
// While dragging, the frame is rendered at a reduced resolution
// ([dragRenderScale]) and upscaled on blit so an orbit stays responsive;
// full resolution is restored on settle.
func (d *ToolpathDrawer) Draw(dst *ebiten.Image, dstBounds image.Rectangle, layers []slice.Layer, cam *Camera, geomGen int, interacting, topCut, botCut bool) {
	w, h := dstBounds.Dx(), dstBounds.Dy()
	if w <= 0 || h <= 0 || len(layers) == 0 {
		return
	}
	// Output resolution: full while still, reduced while dragging. Supersample
	// (and SSAO) only when settled; a drag trades both for responsiveness.
	ow, oh, ss := w, h, settleSupersample
	if interacting {
		ow = max(1, int(float64(w)*dragRenderScale))
		oh = max(1, int(float64(h)*dragRenderScale))
		ss = 1
	}
	rebuilt := d.prepare(ow, oh, ss, layers, cam, geomGen, topCut, botCut, !interacting)
	if d.target == nil || d.target.Bounds().Dx() != ow || d.target.Bounds().Dy() != oh {
		d.target = ebiten.NewImage(ow, oh)
		rebuilt = true // fresh target needs the pixels uploaded
	}
	if rebuilt {
		d.target.WritePixels(d.out)
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(float64(w)/float64(ow), float64(h)/float64(oh)) // upscale reduced-res drag frames
	op.GeoM.Translate(float64(dstBounds.Min.X), float64(dstBounds.Min.Y))
	dst.DrawImage(d.target, op)
}

// prepare ensures the rendered frame in d.rgba is current for the inputs,
// re-rendering at rw×rh only on a cache miss. It returns true when a
// re-render happened. It touches no ebiten image, so it is exercisable
// without a graphics context — Draw is just prepare + upload + blit.
//
// The body is always the solid wall shell (camera-independent geometry
// built once per geomGen, then projected). At a cut (topCut/botCut), that
// boundary layer's toolpaths are overlaid on the exposed cross-section.
func (d *ToolpathDrawer) prepare(ow, oh, ss int, layers []slice.Layer, cam *Camera, geomGen int, topCut, botCut, doSSAO bool) bool {
	key := toolpathCacheKey{geomGen: geomGen, cam: *cam, ow: ow, oh: oh, ss: ss, topCut: topCut, botCut: botCut}
	if d.cacheValid && key == d.cacheKey && len(d.out) == ow*oh*4 {
		return false
	}
	rw, rh := ow*ss, oh*ss
	if len(d.rgba) != rw*rh*4 {
		d.rgba = make([]byte, rw*rh*4)
		d.zbuf = make([]float32, rw*rh)
		d.nbuf = make([]float32, rw*rh*3)
	}
	if len(d.out) != ow*oh*4 {
		d.out = make([]byte, ow*oh*4)
	}

	vp := cam.ViewProj(float32(rw) / float32(rh))
	// Body: the solid wall shell (camera-independent, built once per
	// geometry change). The cut faces' caps are skipped so the cut-face
	// toolpaths below aren't hidden by a wall cap.
	if d.slabGeomGen != geomGen || d.worldSlab == nil {
		d.buildWorldSlab(layers, topCut, botCut)
		d.slabGeomGen = geomGen
	}
	d.projectSlab(rw, rh, &vp)
	// Cutaway: overlay the real toolpath beads of every visible layer. The
	// shell side walls occlude them on the sides (so the exterior stays
	// walls), while the open cut faces reveal the genuine cross-section and
	// the infill lattice going down — instead of a solid wall behind the
	// cut layer's sparse lines.
	if topCut || botCut {
		d.appendBeads(layers, &vp, rw, rh)
	}
	d.rasterize(rw, rh)
	if doSSAO {
		d.ssao(rw, rh, ss, &vp)
	}
	d.downsample(ow, oh, ss)

	d.cacheKey = key
	d.cacheValid = true
	return true
}

// downsample box-filters the rw×rh (= ow*ss × oh*ss) render buffer d.rgba into
// the ow×oh output buffer d.out, averaging each ss×ss block. The rgba bytes
// are effectively premultiplied (covered pixels are opaque, background pixels
// are zero), so a plain average yields correct coverage-weighted anti-aliasing
// against the environment behind the blit. ss==1 is a straight copy.
func (d *ToolpathDrawer) downsample(ow, oh, ss int) {
	if ss == 1 {
		copy(d.out, d.rgba)
		return
	}
	rw := ow * ss
	n := ss * ss
	for oy := 0; oy < oh; oy++ {
		for ox := 0; ox < ow; ox++ {
			var r, g, b, a int
			for dy := 0; dy < ss; dy++ {
				base := ((oy*ss+dy)*rw + ox*ss) * 4
				for dx := 0; dx < ss; dx++ {
					o := base + dx*4
					r += int(d.rgba[o])
					g += int(d.rgba[o+1])
					b += int(d.rgba[o+2])
					a += int(d.rgba[o+3])
				}
			}
			o := (oy*ow + ox) * 4
			d.out[o] = byte(r / n)
			d.out[o+1] = byte(g / n)
			d.out[o+2] = byte(b / n)
			d.out[o+3] = byte(a / n)
		}
	}
}

// appendBeads projects every visible layer's extrusion-path beads and
// appends them to d.tris, parallelized over layers into reused per-worker
// buffers (the same pattern as the shell projection).
func (d *ToolpathDrawer) appendBeads(layers []slice.Layer, vp *ViewProj, w, h int) {
	nw := runtime.NumCPU()
	if nw > len(layers) {
		nw = len(layers)
	}
	if nw < 1 {
		nw = 1
	}
	if len(d.triBufs) != nw {
		d.triBufs = make([][]rtri, nw)
	}
	chunk := (len(layers) + nw - 1) / nw
	var wg sync.WaitGroup
	for wi := 0; wi < nw; wi++ {
		lo := wi * chunk
		hi := lo + chunk
		if hi > len(layers) {
			hi = len(layers)
		}
		if lo >= hi {
			d.triBufs[wi] = d.triBufs[wi][:0]
			continue
		}
		wg.Add(1)
		go func(wi, lo, hi int) {
			defer wg.Done()
			buf := d.triBufs[wi][:0]
			eye := vp.Eye()
			fw, fh := float32(w), float32(h)
			for li := lo; li < hi; li++ {
				buf = appendLayerBeads(buf, &layers[li], vp, eye, fw, fh, d.LightDir, d.FillDir, d.AmbientFactor)
			}
			d.triBufs[wi] = buf
		}(wi, lo, hi)
	}
	wg.Wait()
	for wi := 0; wi < nw; wi++ {
		d.tris = append(d.tris, d.triBufs[wi]...)
	}
}

// appendLayerBeads appends one layer's extrusion-path beads (boxes) to dst.
func appendLayerBeads(dst []rtri, layer *slice.Layer, vp *ViewProj, eye mesh.Vec3, fw, fh float32, light, fill mesh.Vec3, amb float32) []rtri {
	layerH := float32(layer.Height)
	if layerH < minLayerHeight {
		layerH = minLayerHeight
	}
	halfH := layerH * 0.5
	centerZ := float32(layer.Z) - halfH
	for pi := range layer.Paths {
		p := &layer.Paths[pi]
		if !p.Role.IsExtrusion() || len(p.Points) < 2 {
			continue
		}
		if pathOffscreen(vp, p, centerZ, fw, fh) {
			continue
		}
		extrW := float32(p.Width)
		if extrW < minExtrusionWidth {
			extrW = minExtrusionWidth
		}
		halfW := extrW * 0.5
		rc := RoleColor(p.Role)
		n := len(p.Points)
		nseg := n - 1
		if p.Closed {
			nseg = n
		}
		for i := 0; i < nseg; i++ {
			a := p.Points[i]
			b := p.Points[(i+1)%n]
			appendBox(&dst, vp, eye, fw, fh,
				mesh.Vec3{float32(a.X), float32(a.Y), centerZ},
				mesh.Vec3{float32(b.X), float32(b.Y), centerZ},
				halfW, halfH, rc, light, fill, amb)
		}
	}
	return dst
}

// appendBox builds one extrusion segment's box, projects its 8 corners,
// and appends each front-facing (back-face-culled), in-front-of-near-plane
// triangle — shaded by its face normal — to dst.
func appendBox(dst *[]rtri, vp *ViewProj, eye mesh.Vec3, fw, fh float32, A, B mesh.Vec3, halfW, halfH float32, rc color.NRGBA, light, fill mesh.Vec3, amb float32) {
	dir := sub3(B, A)
	if dot3(dir, dir) < 1e-12 {
		return
	}
	dir = norm3(dir)
	right := norm3(cross3(dir, worldUp)) // horizontal, across the bead width
	up := norm3(cross3(right, dir))      // bead height (≈ world up for planar paths)
	rw := scale3(right, halfW)
	uh := scale3(up, halfH)

	corners := [8]mesh.Vec3{
		sub3(sub3(A, rw), uh), sub3(add3(A, rw), uh), add3(add3(A, rw), uh), add3(sub3(A, rw), uh),
		sub3(sub3(B, rw), uh), sub3(add3(B, rw), uh), add3(add3(B, rw), uh), add3(sub3(B, rw), uh),
	}
	var sx, sy, sz [8]float32
	for ci := range corners {
		pr := vp.Project(corners[ci])
		if !pr.InFront {
			return // whole box dropped if any corner is behind the near plane
		}
		sx[ci] = (pr.X + 1) * 0.5 * fw
		sy[ci] = (1 - (pr.Y+1)*0.5) * fh
		sz[ci] = pr.ViewZ
	}

	fr, fg, fb := float32(rc.R), float32(rc.G), float32(rc.B)
	for _, f := range boxFaces {
		n := norm3(cross3(sub3(corners[f[1]], corners[f[0]]), sub3(corners[f[2]], corners[f[0]])))
		ctr := scale3(add3(add3(corners[f[0]], corners[f[1]]), corners[f[2]]), 1.0/3)
		if dot3(n, sub3(ctr, eye)) > 0 {
			continue // back face — points away from the eye
		}
		col := shadePacked(fr, fg, fb, n, light, fill, amb)
		*dst = append(*dst, rtri{
			sx[f[0]], sy[f[0]], sz[f[0]],
			sx[f[1]], sy[f[1]], sz[f[1]],
			sx[f[2]], sy[f[2]], sz[f[2]],
			n[0], n[1], n[2],
			col,
		})
	}
}

// rasterize clears the buffers and scan-converts d.tris into d.rgba with a
// per-pixel depth test, parallelized across horizontal bands so each
// goroutine owns a disjoint set of rows (no shared-pixel contention).
func (d *ToolpathDrawer) rasterize(w, h int) {
	for i := range d.rgba {
		d.rgba[i] = 0 // transparent — viewport background shows through
	}
	for i := range d.zbuf {
		d.zbuf[i] = float32(math.Inf(-1))
	}
	for i := range d.nbuf {
		d.nbuf[i] = 0
	}

	nw := runtime.NumCPU()
	band := (h + nw - 1) / nw
	var wg sync.WaitGroup
	for y0 := 0; y0 < h; y0 += band {
		y1 := y0 + band
		if y1 > h {
			y1 = h
		}
		wg.Add(1)
		go func(y0, y1 int) {
			defer wg.Done()
			d.rasterBand(w, h, y0, y1)
		}(y0, y1)
	}
	wg.Wait()
}

// rasterBand rasterizes every triangle clipped to rows [y0, y1).
func (d *ToolpathDrawer) rasterBand(w, h, y0, y1 int) {
	for ti := range d.tris {
		t := &d.tris[ti]
		minX := int(math.Floor(float64(min3(t.ax, t.bx, t.cx))))
		maxX := int(math.Ceil(float64(max3(t.ax, t.bx, t.cx))))
		minY := int(math.Floor(float64(min3(t.ay, t.by, t.cy))))
		maxY := int(math.Ceil(float64(max3(t.ay, t.by, t.cy))))
		if minY < y0 {
			minY = y0
		}
		if maxY >= y1 {
			maxY = y1 - 1
		}
		if minX < 0 {
			minX = 0
		}
		if maxX >= w {
			maxX = w - 1
		}
		if minX > maxX || minY > maxY {
			continue
		}
		area := (t.bx-t.ax)*(t.cy-t.ay) - (t.by-t.ay)*(t.cx-t.ax)
		if area == 0 {
			continue
		}
		inv := 1 / area
		r := uint8(t.col >> 16)
		g := uint8(t.col >> 8)
		b := uint8(t.col)
		for y := minY; y <= maxY; y++ {
			fy := float32(y) + 0.5
			row := y * w
			for x := minX; x <= maxX; x++ {
				fx := float32(x) + 0.5
				w0 := ((t.bx-fx)*(t.cy-fy) - (t.by-fy)*(t.cx-fx)) * inv
				w1 := ((t.cx-fx)*(t.ay-fy) - (t.cy-fy)*(t.ax-fx)) * inv
				w2 := 1 - w0 - w1
				if (w0 >= 0 && w1 >= 0 && w2 >= 0) || (w0 <= 0 && w1 <= 0 && w2 <= 0) {
					z := w0*t.az + w1*t.bz + w2*t.cz
					idx := row + x
					if z > d.zbuf[idx] {
						d.zbuf[idx] = z
						o := idx * 4
						d.rgba[o] = r
						d.rgba[o+1] = g
						d.rgba[o+2] = b
						d.rgba[o+3] = 0xff
						ni := idx * 3
						d.nbuf[ni] = t.nx
						d.nbuf[ni+1] = t.ny
						d.nbuf[ni+2] = t.nz
					}
				}
			}
		}
	}
}

// pathOffscreen reports whether path p's whole footprint lies beyond a
// single edge of the viewport, so all its segments can be skipped without
// building their boxes. It projects only the four corners of the path's XY
// bounding box (at the layer's centre Z). Returns false (do not cull) if any
// corner is behind the near plane.
func pathOffscreen(vp *ViewProj, p *slice.Path, centerZ, fw, fh float32) bool {
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
			return false
		}
		sx := (pr.X + 1) * 0.5 * fw
		sy := (1 - (pr.Y+1)*0.5) * fh
		allLeft = allLeft && sx < 0
		allRight = allRight && sx > fw
		allAbove = allAbove && sy < 0
		allBelow = allBelow && sy > fh
	}
	return allLeft || allRight || allAbove || allBelow
}

func min3(a, b, c float32) float32 {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func max3(a, b, c float32) float32 {
	if b > a {
		a = b
	}
	if c > a {
		a = c
	}
	return a
}

func sub3(a, b mesh.Vec3) mesh.Vec3 { return mesh.Vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func add3(a, b mesh.Vec3) mesh.Vec3 { return mesh.Vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func scale3(a mesh.Vec3, s float32) mesh.Vec3 {
	return mesh.Vec3{a[0] * s, a[1] * s, a[2] * s}
}
func dot3(a, b mesh.Vec3) float32 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

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
