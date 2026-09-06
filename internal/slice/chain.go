package slice

import "math"

// chainKey is the spatial hash key used when matching segment endpoints
// during chaining. We snap to a 0.001 mm grid (1 µm), well below printer
// resolution and well above float64 noise from the edge-crossing math.
const chainGrid = 1000.0

type chainKey struct{ ix, iy int64 }

func keyOf(p Point2) chainKey {
	return chainKey{
		ix: int64(math.Round(p.X * chainGrid)),
		iy: int64(math.Round(p.Y * chainGrid)),
	}
}

// chainSegments walks the unordered segment list produced by the Z-sweep
// and stitches segments end-to-end into closed polygons. The mesh is
// assumed watertight, so every endpoint should have a matching partner;
// when a partner is missing (non-manifold mesh) we close the loop where
// it is and emit it anyway — the user gets a slightly bad slice rather
// than an empty layer.
func chainSegments(segs []segment2) []Polygon {
	if len(segs) == 0 {
		return nil
	}

	// Build endpoint → []segIdx index. Each segment registers both ends.
	type endRef struct {
		seg int
		end int // 0 = A, 1 = B
	}
	idx := make(map[chainKey][]endRef, len(segs)*2)
	for i, s := range segs {
		ka := keyOf(s.A)
		kb := keyOf(s.B)
		idx[ka] = append(idx[ka], endRef{seg: i, end: 0})
		idx[kb] = append(idx[kb], endRef{seg: i, end: 1})
	}

	used := make([]bool, len(segs))

	// Pick the *other* endpoint of segment s.
	otherEnd := func(s int, end int) Point2 {
		if end == 0 {
			return segs[s].B
		}
		return segs[s].A
	}

	// Find the continuation of the contour at junction k, arriving along
	// dirIn (the unit-ish direction into k). Where the slice plane passes
	// through a mesh vertex, more than two segment-ends share k and a naive
	// "first unused" pick can thread the chain across the interior, closing
	// a self-crossing loop that encloses ~zero area (a see-through gap in
	// the preview). Instead, among the unused candidates pick the sharpest
	// right turn relative to dirIn: consistently turning the same way walks
	// the boundary of the planar segment graph without crossing it — the
	// standard face-traversal rule. With only two ends at k (the common
	// case) this is just the one continuation.
	findPartner := func(k chainKey, except int, head, dirIn Point2) (int, int, bool) {
		best, bestEnd := -1, 0
		bestTurn := math.Inf(-1)
		for _, r := range idx[k] {
			if r.seg == except || used[r.seg] {
				continue
			}
			dirOut := otherEnd(r.seg, r.end).Sub(head)
			// Signed turn angle from dirIn to dirOut in (-π, π]. Taking the
			// most positive (sharpest left / counter-clockwise) turn keeps
			// the walk on its own loop: at a vertex where two regions touch,
			// the rightmost turn would cross into the other region and close
			// a zero-area figure-eight, while the leftmost turn separates
			// them into two simple loops.
			turn := math.Atan2(dirIn.Cross(dirOut), dirIn.Dot(dirOut))
			if turn > bestTurn {
				bestTurn = turn
				best, bestEnd = r.seg, r.end
			}
		}
		if best < 0 {
			return 0, 0, false
		}
		return best, bestEnd, true
	}

	var out []Polygon
	for start := range segs {
		if used[start] {
			continue
		}
		used[start] = true
		// poly grows head-forward; head is always the polygon's last
		// point. headEnd is the index (0=A, 1=B) of `cur`'s endpoint
		// that currently sits at head. We start with head = B, so
		// headEnd = 1.
		poly := Polygon{segs[start].A, segs[start].B}
		cur := start
		headEnd := 1
		startKey := keyOf(segs[start].A)
		for {
			head := otherEnd(cur, 1-headEnd) // the actual head point
			k := keyOf(head)
			// Direction we arrived along, used to pick the continuation
			// that stays on the boundary at multi-segment junctions.
			dirIn := head.Sub(poly[len(poly)-2])
			next, nend, ok := findPartner(k, cur, head, dirIn)
			if !ok {
				break
			}
			used[next] = true
			// We matched `next` at its endpoint nend. The new head is
			// the *opposite* endpoint of next.
			newHead := otherEnd(next, nend)
			poly = append(poly, newHead)
			cur = next
			headEnd = 1 - nend
			// Closed once the head walks back to the polygon's start.
			if keyOf(newHead) == startKey {
				break
			}
		}
		// Drop polygons too small to matter (degenerate intersections).
		if len(poly) >= 3 {
			if samePoint(poly[len(poly)-1], poly[0]) {
				poly = poly[:len(poly)-1]
			}
			if len(poly) >= 3 && poly.Area() > 1e-4 {
				out = append(out, poly)
			}
		}
	}
	return out
}

// assembleExPolygons takes the unordered list of closed contours from
// chaining and groups them into outer/holes pairs. The rule is the
// standard one: for each polygon, count how many other polygons contain
// its first vertex; an even count makes it an outer (CCW oriented in
// output), an odd count makes it a hole of the nearest enclosing outer
// (CW oriented in output).
//
// "Nearest" here means smallest-area enclosing outer; that is correct
// for arbitrary nesting depth as long as no two outers' areas tie, which
// is essentially impossible in real geometry.
func assembleExPolygons(polys []Polygon) []ExPolygon {
	if len(polys) == 0 {
		return nil
	}
	areas := make([]float64, len(polys))
	for i, p := range polys {
		areas[i] = p.Area()
	}
	depth := make([]int, len(polys))
	for i, p := range polys {
		anchor := p[0]
		for j, q := range polys {
			if i == j {
				continue
			}
			if q.Contains(anchor) {
				depth[i]++
			}
		}
	}
	// Even depth = outer (CCW), odd = hole (CW).
	outerOf := make([]int, len(polys))
	for i := range outerOf {
		outerOf[i] = -1
	}
	for i, d := range depth {
		if d%2 == 0 {
			continue // outer itself
		}
		// Find smallest-area outer that contains it.
		bestArea := math.Inf(1)
		best := -1
		anchor := polys[i][0]
		for j := range polys {
			if i == j {
				continue
			}
			if depth[j]%2 != 0 {
				continue
			}
			if !polys[j].Contains(anchor) {
				continue
			}
			if areas[j] < bestArea {
				bestArea = areas[j]
				best = j
			}
		}
		outerOf[i] = best
	}
	// Force orientation: outers CCW, holes CW.
	for i := range polys {
		if depth[i]%2 == 0 {
			if !polys[i].IsCCW() {
				polys[i].Reverse()
			}
		} else if polys[i].IsCCW() {
			polys[i].Reverse()
		}
	}
	// Emit one ExPolygon per outer, with its holes attached.
	out := make([]ExPolygon, 0)
	idxOf := make(map[int]int) // poly idx → out idx
	for i, d := range depth {
		if d%2 != 0 {
			continue
		}
		idxOf[i] = len(out)
		out = append(out, ExPolygon{Outer: polys[i]})
	}
	for i, parent := range outerOf {
		if parent < 0 {
			continue
		}
		oi, ok := idxOf[parent]
		if !ok {
			continue
		}
		out[oi].Holes = append(out[oi].Holes, polys[i])
	}
	return out
}
