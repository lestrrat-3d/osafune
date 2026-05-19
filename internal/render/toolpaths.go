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
// Each visible path is appended into a [vector.Path] bucketed by role,
// then the four buckets are stroked once per layer. That keeps Z-order
// overdraw correct (lower layers first) while collapsing what used to
// be one [vector.StrokeLine] call per segment into one [vector.StrokePath]
// call per (layer, role) — a 1-to-2-order-of-magnitude reduction in
// per-frame overhead for typical Benchy/Wind-Turbine slices.
type ToolpathDrawer struct {
	// LineWidthPx is the preview stroke thickness in pixels. Real
	// extrusion width is layer-dependent and the path knows its true
	// mm width; for the preview we use a constant pixel width because
	// it reads better at any zoom level.
	LineWidthPx float32

	// rolePaths is one [vector.Path] per role bucket. Kept on the drawer
	// so the underlying segment storage is reused across frames; each
	// bucket is Reset() at the start of every layer's pass.
	rolePaths [numRoleBuckets]vector.Path
}

// roleBucket indexes ToolpathDrawer.rolePaths. The order here also
// dictates draw order within a layer: outer walls go down first, then
// inner walls, then sparse infill, then solid infill, mirroring how
// the slicer emitted them. Keep this in sync with [bucketForRole].
type roleBucket int

const (
	bucketExternalPerimeter roleBucket = iota
	bucketPerimeter
	bucketInfill
	bucketSolidInfill
	numRoleBuckets
)

func bucketForRole(r slice.PathRole) (roleBucket, bool) {
	switch r {
	case slice.RoleExternalPerimeter:
		return bucketExternalPerimeter, true
	case slice.RolePerimeter:
		return bucketPerimeter, true
	case slice.RoleInfill:
		return bucketInfill, true
	case slice.RoleSolidInfill:
		return bucketSolidInfill, true
	}
	return 0, false
}

func roleForBucket(b roleBucket) slice.PathRole {
	switch b {
	case bucketExternalPerimeter:
		return slice.RoleExternalPerimeter
	case bucketPerimeter:
		return slice.RolePerimeter
	case bucketInfill:
		return slice.RoleInfill
	case bucketSolidInfill:
		return slice.RoleSolidInfill
	}
	return slice.RoleTravel
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
//
// Within a layer all paths are first bucketed by role into a single
// [vector.Path] each, then each non-empty bucket is stroked with one
// [vector.StrokePath] call. The bucket order — outer → inner → sparse
// → solid — matches the slicer's emit order, so a Benchy's outer walls
// still sit on top of the infill that lives under them.
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

	strokeOpts := [numRoleBuckets]vector.StrokeOptions{}
	for b := roleBucket(0); b < numRoleBuckets; b++ {
		strokeOpts[b] = vector.StrokeOptions{
			Width:      roleStrokeWidth(d.LineWidthPx, roleForBucket(b)),
			LineJoin:   vector.LineJoinBevel,
			MiterLimit: 4,
		}
	}
	drawOpts := [numRoleBuckets]vector.DrawPathOptions{}
	for b := roleBucket(0); b < numRoleBuckets; b++ {
		drawOpts[b] = vector.DrawPathOptions{}
		drawOpts[b].ColorScale.ScaleWithColor(RoleColor(roleForBucket(b)))
	}

	for li := range layers {
		for b := range d.rolePaths {
			d.rolePaths[b].Reset()
		}
		anyBucket := [numRoleBuckets]bool{}
		layerZ := layers[li].Z
		for _, p := range layers[li].Paths {
			if !p.Role.IsExtrusion() || len(p.Points) < 2 {
				continue
			}
			b, ok := bucketForRole(p.Role)
			if !ok {
				continue
			}
			path := &d.rolePaths[b]
			subStarted := false
			firstOK := false
			var firstSx, firstSy float32
			for i, pt := range p.Points {
				pr := project(cam, layerZ, pt, aspect, x0, y0, fw, fh)
				if !pr.ok {
					// Vertex behind the camera — break the polyline.
					// A later in-front vertex will start a fresh sub-path.
					subStarted = false
					continue
				}
				if i == 0 {
					firstSx, firstSy = pr.sx, pr.sy
					firstOK = true
				}
				if !subStarted {
					path.MoveTo(pr.sx, pr.sy)
					subStarted = true
				} else {
					path.LineTo(pr.sx, pr.sy)
				}
				anyBucket[b] = true
			}
			// Close the loop manually rather than using Path.Close:
			// Close only joins the current sub-path to its MoveTo, but
			// if any vertex went behind the camera we may have emitted
			// multiple sub-paths and want the seam to land back at the
			// original first vertex specifically. Skip when either the
			// first vertex was never in front, or the closing edge
			// would dangle from a broken sub-path.
			if p.Closed && subStarted && firstOK {
				path.LineTo(firstSx, firstSy)
			}
		}
		for b := roleBucket(0); b < numRoleBuckets; b++ {
			if !anyBucket[b] {
				continue
			}
			vector.StrokePath(dst, &d.rolePaths[b], &strokeOpts[b], &drawOpts[b])
		}
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
