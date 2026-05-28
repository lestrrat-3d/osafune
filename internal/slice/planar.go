package slice

import (
	"math"
	"sort"

	"github.com/lestrrat-go/osafune/internal/mesh"
)

// LayerHeights produces the sequence of Z heights at which the mesh
// should be sliced, given a first-layer thickness and a constant
// layer-height for the rest. Z values are the top-of-layer where the
// nozzle sits; the first value is firstLayer (not 0), the next is
// firstLayer + layerHeight, and so on, up to and including the mesh top.
//
// We slice at the *midpoint* of each layer rather than the top: this is
// the standard slicer convention because a feature whose top lies exactly
// at a layer boundary should still produce a contour, and a feature only
// barely poking into a layer should be captured at half-thickness up.
// Returned Z values are still "nozzle Z", i.e. top of layer; the sweep
// just uses (Z - height/2) when intersecting.
func LayerHeights(meshMinZ, meshMaxZ, firstLayer, layerHeight float64) []float64 {
	if meshMaxZ-meshMinZ < Epsilon {
		return nil
	}
	// Shift so layer 1 sits between meshMinZ and meshMinZ+firstLayer.
	z := meshMinZ + firstLayer
	var out []float64
	for z < meshMaxZ+layerHeight*0.5 {
		out = append(out, z)
		z += layerHeight
	}
	return out
}

// segment2 is one 2D line segment produced by intersecting a triangle
// with a Z plane. We keep them flat (rather than already-chained) so the
// chaining stage can handle missing partners gracefully.
type segment2 struct {
	A, B Point2
}

// SliceMesh runs the Z-sweep over mesh m, producing one entry per layer
// height in zs. Each entry is the list of ExPolygons describing the
// horizontal cross-section at that Z.
//
// Triangles whose Z range does not include the layer's slicing plane are
// skipped. Vertices that lie exactly on the slicing plane are biased to
// "above" — this is the conventional fix for the otherwise-ambiguous
// case where a triangle sits flat on a layer boundary.
func SliceMesh(m *mesh.Mesh, firstLayerHeight, layerHeight float64) []Layer {
	if len(m.Triangles) == 0 {
		return nil
	}
	minZ := float64(m.Bounds.Min[2])
	maxZ := float64(m.Bounds.Max[2])
	zs := LayerHeights(minZ, maxZ, firstLayerHeight, layerHeight)
	if len(zs) == 0 {
		return nil
	}

	// Precompute (zMin, zMax) per triangle so we can early-reject; this
	// turns the per-layer pass from O(triangles) into O(triangles in
	// vertical range) on tall meshes.
	ranges := make([]triRange, len(m.Triangles))
	for i, t := range m.Triangles {
		z0 := float64(t.Vertices[0][2])
		z1 := float64(t.Vertices[1][2])
		z2 := float64(t.Vertices[2][2])
		ranges[i] = triRange{
			idx:  i,
			zMin: math.Min(z0, math.Min(z1, z2)),
			zMax: math.Max(z0, math.Max(z1, z2)),
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].zMin < ranges[j].zMin })

	layers := make([]Layer, len(zs))
	for li, z := range zs {
		// Slice midway through the layer thickness. For the very first
		// layer we use firstLayerHeight; for the rest, layerHeight.
		h := layerHeight
		if li == 0 {
			h = firstLayerHeight
		}
		layers[li] = Layer{
			Index:    li,
			Z:        z,
			Height:   h,
			Contours: contoursAtPlane(m, ranges, z-h*0.5),
		}
	}
	fixNotchLayers(m, ranges, zs, layers, firstLayerHeight, layerHeight)
	return layers
}

// triRange is the precomputed vertical extent of one triangle, used to
// skip triangles that cannot cross a given slice plane.
type triRange struct {
	idx        int
	zMin, zMax float64
}

// contoursAtPlane intersects the mesh with a single horizontal plane and
// returns the assembled cross-section. ranges must be sorted by zMin.
func contoursAtPlane(m *mesh.Mesh, ranges []triRange, slicePlane float64) []ExPolygon {
	var segs []segment2
	for _, r := range ranges {
		if r.zMin > slicePlane {
			break // sorted by zMin, no more candidates
		}
		if r.zMax < slicePlane {
			continue
		}
		if s, ok := intersectTriangle(&m.Triangles[r.idx], slicePlane); ok {
			segs = append(segs, s)
		}
	}
	return assembleExPolygons(chainSegments(segs))
}

// contoursArea sums the outer area of every contour in a layer.
func contoursArea(cs []ExPolygon) float64 {
	var a float64
	for _, c := range cs {
		a += c.Outer.Area()
	}
	return a
}

// nudgeOffsets are the slice-plane shifts (mm) tried by [fixNotchLayers],
// small enough to stay well inside the layer band but enough to clear a
// mesh vertex sitting on the plane.
var nudgeOffsets = []float64{0.02, -0.02, 0.05, -0.05, 0.08, -0.08}

// fixNotchLayers repairs the occasional layer whose cross-section area
// notches sharply below BOTH neighbours. A real feature does not lose then
// regain area within a single 0.2 mm layer, so such a notch is a slicing
// artifact: the plane grazed a mesh vertex, the chaining took a shortcut,
// and area was cut. Re-slicing a few µm above/below dodges the degenerate
// plane; the attempt with the most area (closest to the true section, since
// the artifact only ever removes area) replaces the notch.
func fixNotchLayers(m *mesh.Mesh, ranges []triRange, zs []float64, layers []Layer, firstLayerHeight, layerHeight float64) {
	for i := 1; i < len(layers)-1; i++ {
		a := contoursArea(layers[i].Contours)
		ref := math.Min(contoursArea(layers[i-1].Contours), contoursArea(layers[i+1].Contours))
		if ref <= 0 || a >= 0.8*ref {
			continue // not an isolated notch
		}
		h := layerHeight
		if i == 0 {
			h = firstLayerHeight
		}
		plane := zs[i] - h*0.5
		best, bestA := layers[i].Contours, a
		for _, nudge := range nudgeOffsets {
			alt := contoursAtPlane(m, ranges, plane+nudge)
			if aa := contoursArea(alt); aa > bestA {
				best, bestA = alt, aa
			}
		}
		layers[i].Contours = best
	}
}

// intersectTriangle returns the line segment where triangle t crosses the
// horizontal plane z=plane. The second return is false when the triangle
// does not cross (lies entirely above or below) or is degenerate.
//
// Vertices within [Epsilon] of the plane are treated as strictly above —
// this is the standard rule that keeps mesh-on-layer-boundary cases from
// producing duplicate or missing segments.
func intersectTriangle(t *mesh.Triangle, plane float64) (segment2, bool) {
	type vz struct {
		x, y, z float64
		side    int // -1 below, +1 above-or-on
	}
	var vs [3]vz
	for i := 0; i < 3; i++ {
		v := t.Vertices[i]
		z := float64(v[2])
		s := -1
		if z >= plane-Epsilon {
			s = 1
		}
		vs[i] = vz{float64(v[0]), float64(v[1]), z, s}
	}
	// Sum of sides: +3 = all above, -3 = all below, ±1 = one across.
	sum := vs[0].side + vs[1].side + vs[2].side
	if sum == 3 || sum == -3 {
		return segment2{}, false
	}
	// The "lone" vertex is the one on the opposite side from the other
	// two. Two edges cross the plane: lone→other1 and lone→other2.
	lone := -1
	for i := 0; i < 3; i++ {
		if vs[i].side != vs[(i+1)%3].side && vs[i].side != vs[(i+2)%3].side {
			lone = i
			break
		}
	}
	if lone < 0 {
		return segment2{}, false
	}
	a := edgeCross(vs[lone], vs[(lone+1)%3], plane)
	b := edgeCross(vs[lone], vs[(lone+2)%3], plane)
	if a == b {
		return segment2{}, false
	}
	return segment2{A: a, B: b}, true
}

// edgeCross returns the (x,y) point where the line from p to q crosses
// the horizontal plane z=plane. Assumes the segment actually crosses
// (caller checked).
func edgeCross(p, q struct {
	x, y, z float64
	side    int
}, plane float64) Point2 {
	dz := q.z - p.z
	if math.Abs(dz) < Epsilon {
		// Edge is parallel to slicing plane — return the midpoint as a
		// best-effort. In practice this case is rejected by the caller
		// because both endpoints will land on the same side.
		return Point2{(p.x + q.x) * 0.5, (p.y + q.y) * 0.5}
	}
	t := (plane - p.z) / dz
	return Point2{p.x + t*(q.x-p.x), p.y + t*(q.y-p.y)}
}
