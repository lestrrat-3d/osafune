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

// tubeSides is the number of facets around each extrusion segment's
// elliptical cross-section. Eight is the lowest count that reads as
// "round" rather than "polygonal" at typical viewing distances; bump
// it for prettier previews at the cost of proportional CPU work.
const tubeSides = 8

// Precomputed unit-circle samples for the cross-section corners and
// for the midpoints between adjacent corners (used to derive face
// normals). Filled in by init().
var (
	tubeCos [tubeSides]float32 // cos at corner k
	tubeSin [tubeSides]float32 // sin at corner k
	// midCos/midSin give the direction of the inward-of-the-arc
	// midpoint between corner k and corner k+1 — i.e. the direction
	// the k-th face's outward normal points to before being mapped
	// into world space.
	tubeMidCos [tubeSides]float32
	tubeMidSin [tubeSides]float32
)

func init() {
	for k := 0; k < tubeSides; k++ {
		theta := 2 * math.Pi * float64(k) / float64(tubeSides)
		tubeCos[k] = float32(math.Cos(theta))
		tubeSin[k] = float32(math.Sin(theta))
		mid := 2 * math.Pi * (float64(k) + 0.5) / float64(tubeSides)
		tubeMidCos[k] = float32(math.Cos(mid))
		tubeMidSin[k] = float32(math.Sin(mid))
	}
}

// ToolpathDrawer renders sliced layers as round tube-shaped extrusion
// segments — each segment is a world-space prism with an elliptical
// cross-section of (Path.Width × Layer.Height), projected through the
// camera and painter-sorted in view space. The elliptical profile
// matches what a real squashed-extrusion plastic bead looks like:
// wider than tall (the nozzle squashes it against the layer below),
// and just touching the neighbours on every side.
//
// Back-facing facets are culled in the world-space pass; the visible
// front facets of a convex segment cannot overlap each other in
// screen space, so they may be emitted in any order within a segment.
// Across segments, a per-segment painter sort handles inter-segment
// occlusion. End caps are intentionally omitted: adjacent segments
// along a path share endpoints, and caps would just interpenetrate
// without adding visual value.
type ToolpathDrawer struct {
	// LightDir is the unit direction the virtual key light shines
	// toward. Same convention as the mesh rasterizer's LightDir
	// (shade = ambient + (1-ambient) · max(0, n · -LightDir)).
	LightDir mesh.Vec3

	// AmbientFactor in [0, 1] keeps the shadow-side of tubes
	// readable rather than going to black.
	AmbientFactor float32

	// LineWidthPx is retained for backwards compatibility but no
	// longer has any effect on this volumetric renderer.
	LineWidthPx float32

	// Scratch buffers reused across frames.
	segs    []segmentRecord
	verts   []ebiten.Vertex
	indices []uint16
}

// segmentRecord caches the per-segment geometry the emit phase needs:
// the 2·tubeSides projected cross-section corners (one ring at each
// endpoint), the perpendicular direction in XY that the ring is
// parameterised against, the base role colour, and the painter-sort
// key (segment-centre view Z).
type segmentRecord struct {
	aProj [tubeSides]Projected
	bProj [tubeSides]Projected
	rX    float32
	rY    float32
	baseR float32
	baseG float32
	baseB float32
	avgZ  float32
}

// NewToolpathDrawer returns a drawer using the same key light /
// ambient settings as the mesh rasterizer, so the toolpath preview's
// shading reads consistently when the user toggles between mesh and
// toolpath modes.
func NewToolpathDrawer() *ToolpathDrawer {
	return &ToolpathDrawer{
		LightDir:      normalize(mesh.Vec3{-0.4, -0.5, -0.8}),
		AmbientFactor: 0.4,
		LineWidthPx:   1.5,
	}
}

// minExtrusionWidth and minLayerHeight clamp degenerate path metadata
// so a zero-width path never produces a sliver tube that disappears
// at certain angles.
const (
	minExtrusionWidth = 0.05 // mm
	minLayerHeight    = 0.05 // mm
)

// Draw is the renderer entry point — see [ToolpathDrawer] for the
// high-level algorithm. Each frame: collect per-segment records,
// reject any segment whose cross-section pokes behind the near plane,
// sort the survivors by view-space depth, then emit front-facing
// facets in sorted segment order into a single index buffer that's
// chunked at the uint16 cap.
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

	d.segs = d.segs[:0]

	for li := range layers {
		layer := &layers[li]
		layerH := float32(layer.Height)
		if layerH < minLayerHeight {
			layerH = minLayerHeight
		}
		halfH := layerH * 0.5
		// Layer.Z is the top of the layer; the printed bead occupies
		// [Z-Height, Z]. Centring the cross-section on the layer
		// midpoint makes adjacent layers' tubes meet at exactly the
		// layer boundary.
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
				// Right-hand perpendicular to segment in XY (unit).
				rX := -dy * inv
				rY := dx * inv

				rec := segmentRecord{
					rX:    rX,
					rY:    rY,
					baseR: baseR,
					baseG: baseG,
					baseB: baseB,
				}

				allInFront := true
				var sumAZ, sumBZ float32
				for k := 0; k < tubeSides; k++ {
					c := tubeCos[k]
					s := tubeSin[k]
					ox := rX * c * halfW
					oy := rY * c * halfW
					oz := s * halfH
					pa := cam.Project(mesh.Vec3{ax + ox, ay + oy, centerZ + oz}, aspect)
					pb := cam.Project(mesh.Vec3{bx + ox, by + oy, centerZ + oz}, aspect)
					rec.aProj[k] = pa
					rec.bProj[k] = pb
					if !pa.InFront || !pb.InFront {
						allInFront = false
						break
					}
					sumAZ += pa.ViewZ
					sumBZ += pb.ViewZ
				}
				if !allInFront {
					// Any vertex behind the near plane → drop the
					// whole tube. Robust near-plane clipping is a
					// follow-up; the bounding-box-fit camera keeps
					// the print in front in normal use.
					continue
				}
				rec.avgZ = (sumAZ + sumBZ) / (2 * tubeSides)
				d.segs = append(d.segs, rec)
			}
		}
	}

	if len(d.segs) == 0 {
		return
	}

	// Painter sort by segment centre: most-negative view Z (furthest
	// from camera) first, so nearer tubes overdraw them.
	sort.Slice(d.segs, func(i, j int) bool {
		return d.segs[i].avgZ < d.segs[j].avgZ
	})

	d.emitSegments(dst, x0, y0, fw, fh)
}

// emitSegments walks the segment list in painter-sorted order,
// builds each segment's front-facing facets into the shared vertex /
// index buffer, and flushes via DrawTriangles each time we'd
// otherwise exceed the uint16 index cap.
//
// Backface culling is done in screen space via the signed area of
// the projected quad: a back-facing facet's corners wind opposite to
// a front-facing one's, so the cross-product of the two diagonals
// flips sign. That's both cheaper than reconstructing world-space
// coords for an eye-vs-facet dot product, and correct under
// perspective projection.
func (d *ToolpathDrawer) emitSegments(dst *ebiten.Image, x0, y0, fw, fh float32) {
	const quadsPerBatch = 65535 / 6 // 6 indices per quad

	d.verts = d.verts[:0]
	d.indices = d.indices[:0]
	quadsInBatch := 0

	for si := range d.segs {
		seg := &d.segs[si]
		for k := 0; k < tubeSides; k++ {
			kn := (k + 1) % tubeSides

			// World-space outward normal of facet k.
			midC := tubeMidCos[k]
			midS := tubeMidSin[k]
			nx := seg.rX * midC
			ny := seg.rY * midC
			nz := midS
			// (rX, rY) is unit in XY, and (midC, midS) sits on the
			// unit circle, so (nx, ny, nz) is already unit-length —
			// no normalisation needed.

			pa0 := seg.aProj[k]
			pa1 := seg.aProj[kn]
			pb0 := seg.bProj[k]
			pb1 := seg.bProj[kn]

			sax0 := x0 + (pa0.X+1)*0.5*fw
			say0 := y0 + (1-(pa0.Y+1)*0.5)*fh
			sax1 := x0 + (pa1.X+1)*0.5*fw
			say1 := y0 + (1-(pa1.Y+1)*0.5)*fh
			sbx1 := x0 + (pb1.X+1)*0.5*fw
			sby1 := y0 + (1-(pb1.Y+1)*0.5)*fh
			sbx0 := x0 + (pb0.X+1)*0.5*fw
			sby0 := y0 + (1-(pb0.Y+1)*0.5)*fh

			// Screen-space signed area of the quad (a0 → a1 → b1 →
			// b0). Positive means CCW in screen coords with Y-down,
			// which we use as our "front facing" convention. A
			// degenerate (zero-area) facet seen edge-on contributes
			// nothing — skip.
			area := (sax1-sax0)*(sby0-say0) - (sbx0-sax0)*(say1-say0)
			if area <= 0 {
				continue
			}

			// Lambertian shade. The normal we want here is the
			// world-space facet normal; shade independent of view.
			ndotL := -(nx*d.LightDir[0] + ny*d.LightDir[1] + nz*d.LightDir[2])
			if ndotL < 0 {
				ndotL = 0
			}
			shade := d.AmbientFactor + (1-d.AmbientFactor)*ndotL
			sR := seg.baseR * shade
			sG := seg.baseG * shade
			sB := seg.baseB * shade

			// Flush the current batch if appending one more quad
			// would push the index buffer past the uint16 cap.
			if quadsInBatch == quadsPerBatch {
				dst.DrawTriangles(d.verts, d.indices, getWhite(), nil)
				d.verts = d.verts[:0]
				d.indices = d.indices[:0]
				quadsInBatch = 0
			}

			vb := uint16(len(d.verts))
			d.verts = append(d.verts,
				ebiten.Vertex{DstX: sax0, DstY: say0, ColorR: sR, ColorG: sG, ColorB: sB, ColorA: 1},
				ebiten.Vertex{DstX: sax1, DstY: say1, ColorR: sR, ColorG: sG, ColorB: sB, ColorA: 1},
				ebiten.Vertex{DstX: sbx1, DstY: sby1, ColorR: sR, ColorG: sG, ColorB: sB, ColorA: 1},
				ebiten.Vertex{DstX: sbx0, DstY: sby0, ColorR: sR, ColorG: sG, ColorB: sB, ColorA: 1},
			)
			d.indices = append(d.indices,
				vb, vb+1, vb+2,
				vb, vb+2, vb+3,
			)
			quadsInBatch++
		}
	}

	if quadsInBatch > 0 {
		dst.DrawTriangles(d.verts, d.indices, getWhite(), nil)
	}
}
