package slice

import (
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
	return safeMulti(func() (geom.MultiPolygon, error) {
		return polyclip.Offset(toMulti(regions), -d, offsetOpts)
	}, regions)
}

// OffsetExPolygon shrinks a single region inward by d. It is a thin wrapper
// over [offsetRegions] for callers that start from one region; the result
// is still a slice because the offset can split or drop the region.
func OffsetExPolygon(e ExPolygon, d float64) []ExPolygon {
	return offsetRegions([]ExPolygon{e}, d)
}
