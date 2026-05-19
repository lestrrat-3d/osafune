package slice

import "math"

// MiterLimit caps the miter at sharp corners. If the bisector distance
// exceeds MiterLimit * |d|, the corner is bevelled (replaced by two close
// vertices). A real slicer would compute a proper bevel; the MVP just
// truncates the corner inward by the miter limit, which produces a tiny
// chamfer for very sharp angles. Acceptable for first-pass quality.
const MiterLimit = 5.0

// perpLeft rotates v 90° counter-clockwise. For an edge a→b, perpLeft of
// the edge direction is the "left normal" used by [OffsetPolygon].
func perpLeft(v Point2) Point2 { return Point2{-v.Y, v.X} }

// OffsetPolygon shifts every edge of p by d in the direction of its
// left-perpendicular (90° CCW rotation of the edge direction). The
// resulting polygon has the same vertex count as the input.
//
// Sign convention: for a CCW polygon, positive d shrinks it (the left
// normal points into the interior). For a CW polygon, positive d grows
// it. Combined, applying the same positive d to a CCW outer and the CW
// holes inside it produces an [ExPolygon] whose printable region has
// shrunk by d on every boundary — which is exactly what perimeter and
// infill-area calculation need.
//
// Naive caveats: there is no self-intersection cleanup, so very thin
// features collapse to bow-tie polygons rather than vanishing cleanly,
// and a U-shape that should split into two disjoint pieces under a large
// offset comes out as a single self-intersecting polygon. Both are
// known MVP limitations.
func OffsetPolygon(p Polygon, d float64) Polygon {
	if len(p) < 3 {
		return nil
	}
	n := len(p)
	// Pre-compute unit edge directions and their left-normals.
	dirs := make([]Point2, n)
	norms := make([]Point2, n)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		e := p[j].Sub(p[i]).Normalize()
		dirs[i] = e
		norms[i] = perpLeft(e)
	}
	out := make(Polygon, 0, n)
	for i := 0; i < n; i++ {
		prev := (i - 1 + n) % n
		nPrev := norms[prev]
		nNext := norms[i]
		// Closed-form miter offset. Denominator 1 + nPrev·nNext is in
		// [0, 2]; it approaches 0 only at near-180° reflex corners.
		denom := 1 + nPrev.Dot(nNext)
		var off Point2
		if denom < 0.05 {
			// Very sharp corner — fall back to the average normal,
			// clamped to the miter limit. This is the bevel surrogate.
			avg := nPrev.Add(nNext)
			l := avg.Len()
			if l < Epsilon {
				// 180° reflex: just slide along the previous normal.
				off = nPrev.Scale(d)
			} else {
				avg = avg.Scale(1 / l)
				off = avg.Scale(d * MiterLimit)
			}
		} else {
			off = nPrev.Add(nNext).Scale(d / denom)
		}
		// Clamp very long offsets at the miter limit. |off| should not
		// exceed MiterLimit * |d| even with the formula above.
		l := off.Len()
		max := math.Abs(d) * MiterLimit
		if l > max && max > 0 {
			off = off.Scale(max / l)
		}
		out = append(out, p[i].Add(off))
	}
	return out
}

// OffsetExPolygon applies the same offset d to the outer contour and to
// every hole. With the sign convention from [OffsetPolygon], positive d
// shrinks the printable region (outer shrinks, holes grow into the
// solid). Multiple disjoint output regions are not synthesised; the
// naive offsetter just returns one ExPolygon per input.
func OffsetExPolygon(e ExPolygon, d float64) ExPolygon {
	out := ExPolygon{Outer: OffsetPolygon(e.Outer, d)}
	for _, h := range e.Holes {
		oh := OffsetPolygon(h, d)
		if len(oh) >= 3 {
			out.Holes = append(out.Holes, oh)
		}
	}
	return out
}
