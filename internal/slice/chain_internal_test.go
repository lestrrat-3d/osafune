package slice

import (
	"math"
	"testing"
)

// TestChainSegmentsTouchingCorner guards the junction-handling rule in
// chainSegments. Two unit-ish squares touch at the single vertex (10,10),
// so four segment-ends share that point. A naive "first unused" or
// rightmost-turn continuation would cross through the touch point and close
// a single zero-area figure-eight (the see-through gap bug); the correct
// leftmost-turn rule keeps them as two separate loops of area 100.
func TestChainSegmentsTouchingCorner(t *testing.T) {
	seg := func(ax, ay, bx, by float64) segment2 {
		return segment2{A: Point2{ax, ay}, B: Point2{bx, by}}
	}
	segs := []segment2{
		// square A
		seg(0, 0, 10, 0), seg(10, 0, 10, 10), seg(10, 10, 0, 10), seg(0, 10, 0, 0),
		// square B, touching A only at (10,10)
		seg(10, 10, 20, 10), seg(20, 10, 20, 20), seg(20, 20, 10, 20), seg(10, 20, 10, 10),
	}

	polys := chainSegments(segs)
	if len(polys) != 2 {
		t.Fatalf("want 2 separate loops at the touch point, got %d", len(polys))
	}
	for i, p := range polys {
		if a := p.Area(); math.Abs(a-100) > 1e-6 {
			t.Fatalf("loop %d: want area 100, got %.4f (degenerate/figure-eight?)", i, a)
		}
	}
}

// TestChainSegmentsSimpleSquare is the non-junction sanity case: a lone
// square chains into one loop of the right area.
func TestChainSegmentsSimpleSquare(t *testing.T) {
	seg := func(ax, ay, bx, by float64) segment2 {
		return segment2{A: Point2{ax, ay}, B: Point2{bx, by}}
	}
	segs := []segment2{
		seg(0, 0, 4, 0), seg(4, 0, 4, 4), seg(4, 4, 0, 4), seg(0, 4, 0, 0),
	}
	polys := chainSegments(segs)
	if len(polys) != 1 {
		t.Fatalf("want 1 loop, got %d", len(polys))
	}
	if a := polys[0].Area(); math.Abs(a-16) > 1e-6 {
		t.Fatalf("want area 16, got %.4f", a)
	}
}
