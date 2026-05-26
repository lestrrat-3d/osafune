package slice

import (
	"math"

	"github.com/lestrrat-go/polyclip"
	"github.com/lestrrat-go/polyclip/geom"
)

// Sign convention: throughout the slicer a POSITIVE offset distance shrinks
// the printable region (outer contour moves inward, holes grow into the
// solid). This is what perimeter and infill generation want — each
// successive wall sits one line-width further inside. polyclip uses the
// opposite sign (positive inflates), so the bridge negates.
//
// Unlike the MVP's naive per-edge offsetter, polyclip cleans up the
// self-intersections an inward offset produces: a region that necks apart
// under a large offset splits into two disjoint pieces, an over-shrunk
// feature collapses and is dropped, and sharp corners are mitered. That is
// why these functions return a SLICE of [ExPolygon] — one input region can
// become several (or zero) after offsetting.

// offsetOpts is the join configuration used for every offset in the
// slicer. Miter joins match the way a real nozzle traces a corner; the
// default miter limit bevels only genuinely needle-sharp corners.
var offsetOpts = polyclip.OffsetOptions{Join: polyclip.JoinMiter}

// offsetRegions shrinks every region in the set inward by d (or grows it
// when d is negative), returning the cleaned-up result. Disjoint pieces
// that a single input produced under a topology-changing offset come back
// as separate regions; collapsed pieces are dropped. On engine error the
// input is returned unchanged so a layer is never silently lost.
func offsetRegions(regions []ExPolygon, d float64) []ExPolygon {
	if len(regions) == 0 || d == 0 {
		return regions
	}
	// Negate: polyclip's positive d inflates, the slicer's shrinks. The
	// shared safeMulti wrapper absorbs an engine panic the same way the
	// boolean ops do — on failure the region is returned unoffset, which
	// keeps the layer present (the next wall just overlaps slightly).
	out := safeMulti(func() (geom.MultiPolygon, error) {
		return polyclip.Offset(toMulti(regions), -d, offsetOpts)
	}, regions)
	return dropOffsetSpikes(out, regions, math.Abs(d))
}

// dropOffsetSpikes removes output vertices that lie implausibly far outside
// the offset's input bounding box. An offset moves a boundary point by at
// most |d|, so a vertex beyond inputBBox ± (|d| + margin) cannot be real
// geometry — it is a stray spike polyclip.Offset occasionally emits on
// degenerate slice contours, which otherwise renders as a "beam" shooting
// out of the model and pollutes the infill/skin areas. Filtering those
// vertices reconnects the ring across the excursion; rings left with fewer
// than three vertices are dropped.
func dropOffsetSpikes(result, input []ExPolygon, d float64) []ExPolygon {
	if len(result) == 0 {
		return result
	}
	min, max, ok := regionsBBox(input)
	if !ok {
		return result
	}
	const margin = 1.0 // mm of slack beyond the provable |d| reach
	lo := Point2{X: min.X - d - margin, Y: min.Y - d - margin}
	hi := Point2{X: max.X + d + margin, Y: max.Y + d + margin}
	inside := func(p Point2) bool {
		return p.X >= lo.X && p.X <= hi.X && p.Y >= lo.Y && p.Y <= hi.Y
	}
	filter := func(r Polygon) Polygon {
		kept := make(Polygon, 0, len(r))
		for _, p := range r {
			if inside(p) {
				kept = append(kept, p)
			}
		}
		return kept
	}

	out := make([]ExPolygon, 0, len(result))
	for _, e := range result {
		outer := filter(e.Outer)
		if len(outer) < 3 {
			continue
		}
		ne := ExPolygon{Outer: outer}
		for _, h := range e.Holes {
			if hf := filter(h); len(hf) >= 3 {
				ne.Holes = append(ne.Holes, hf)
			}
		}
		out = append(out, ne)
	}
	return out
}

// regionsBBox returns the axis-aligned bounds of every outer contour in
// regions. ok is false when there is no non-empty contour.
func regionsBBox(regions []ExPolygon) (min, max Point2, ok bool) {
	for _, e := range regions {
		for _, p := range e.Outer {
			if !ok {
				min, max, ok = p, p, true
				continue
			}
			min.X = math.Min(min.X, p.X)
			min.Y = math.Min(min.Y, p.Y)
			max.X = math.Max(max.X, p.X)
			max.Y = math.Max(max.Y, p.Y)
		}
	}
	return min, max, ok
}

// OffsetExPolygon shrinks a single region inward by d. It is a thin wrapper
// over [offsetRegions] for callers that start from one region; the result
// is still a slice because the offset can split or drop the region.
func OffsetExPolygon(e ExPolygon, d float64) []ExPolygon {
	return offsetRegions([]ExPolygon{e}, d)
}
