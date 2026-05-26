package slice

import "testing"

// TestDropOffsetSpikes verifies the defensive filter that removes the
// far-flung vertices polyclip.Offset occasionally emits (the "beams"
// sticking out of a sliced model). It exercises unexported logic that can't
// be triggered deterministically through the public offset API, so it lives
// in the internal test package.
func TestDropOffsetSpikes(t *testing.T) {
	input := []ExPolygon{{Outer: Polygon{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}, {X: 0, Y: 10}}}}

	// An offset result whose ring carries one stray vertex thousands of mm
	// outside the input — the spike must be dropped, the rest kept.
	spiked := []ExPolygon{{Outer: Polygon{
		{X: 1, Y: 1}, {X: 9, Y: 1}, {X: 1599, Y: 1624}, {X: 9, Y: 9}, {X: 1, Y: 9},
	}}}
	got := dropOffsetSpikes(spiked, input, 0.5)
	if len(got) != 1 {
		t.Fatalf("want 1 region, got %d", len(got))
	}
	if len(got[0].Outer) != 4 {
		t.Fatalf("want spike removed (4 vertices), got %d: %v", len(got[0].Outer), got[0].Outer)
	}
	for _, p := range got[0].Outer {
		if p.X > 100 || p.Y > 100 {
			t.Fatalf("spike vertex survived: %v", p)
		}
	}

	// A clean result (every vertex within reach) must pass through unchanged.
	clean := []ExPolygon{{Outer: Polygon{{X: 1, Y: 1}, {X: 9, Y: 1}, {X: 9, Y: 9}, {X: 1, Y: 9}}}}
	got = dropOffsetSpikes(clean, input, 0.5)
	if len(got) != 1 || len(got[0].Outer) != 4 {
		t.Fatalf("clean result altered: %+v", got)
	}

	// A ring reduced below three vertices by filtering is dropped entirely.
	mostlySpike := []ExPolygon{{Outer: Polygon{
		{X: 1, Y: 1}, {X: 5000, Y: 1}, {X: 1, Y: 5000},
	}}}
	if got = dropOffsetSpikes(mostlySpike, input, 0.5); len(got) != 0 {
		t.Fatalf("want degenerate ring dropped, got %d regions", len(got))
	}
}
