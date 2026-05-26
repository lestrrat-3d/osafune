package slice

import (
	"github.com/lestrrat-go/polyclip"
	"github.com/lestrrat-go/polyclip/geom"
)

// This file is the bridge between the slicer's own geometry types
// ([Point2], [Polygon], [ExPolygon]) and the polyclip library's geom
// types. polyclip provides the robust 2D boolean ops (union, difference,
// intersection) and polygon offsetting that the MVP's naive edge offsetter
// could not do correctly — self-intersection cleanup, topology changes
// (a region that necks apart into two), and miter/round joins.
//
// Conventions line up between the two libraries: outer rings are CCW,
// holes are CW, and the closing edge is implicit (the first point is not
// repeated at the end). So conversion is a straight copy of coordinates
// with no winding fix-up.

func toGeomPolygon(p Polygon) geom.Polygon {
	out := make(geom.Polygon, len(p))
	for i, pt := range p {
		out[i] = geom.Point{X: pt.X, Y: pt.Y}
	}
	return out
}

func fromGeomPolygon(p geom.Polygon) Polygon {
	out := make(Polygon, len(p))
	for i, pt := range p {
		out[i] = Point2{X: pt.X, Y: pt.Y}
	}
	return out
}

func toGeomExPolygon(e ExPolygon) geom.ExPolygon {
	out := geom.ExPolygon{Outer: toGeomPolygon(e.Outer)}
	for _, h := range e.Holes {
		out.Holes = append(out.Holes, toGeomPolygon(h))
	}
	return out
}

func fromGeomExPolygon(e geom.ExPolygon) ExPolygon {
	out := ExPolygon{Outer: fromGeomPolygon(e.Outer)}
	for _, h := range e.Holes {
		out.Holes = append(out.Holes, fromGeomPolygon(h))
	}
	return out
}

// toMulti packs a set of slicer regions into a polyclip MultiPolygon, the
// closed type every boolean/offset op consumes and returns. Degenerate
// (<3 vertex) outers are dropped on the way in.
func toMulti(regions []ExPolygon) geom.MultiPolygon {
	out := make(geom.MultiPolygon, 0, len(regions))
	for _, e := range regions {
		if len(e.Outer) < 3 {
			continue
		}
		out = append(out, toGeomExPolygon(e))
	}
	return out
}

// fromMulti unpacks a polyclip result back into slicer regions, dropping
// any degenerate piece the engine may have produced at the limits of an
// over-aggressive offset.
func fromMulti(m geom.MultiPolygon) []ExPolygon {
	out := make([]ExPolygon, 0, len(m))
	for _, e := range m {
		if len(e.Outer) < 3 {
			continue
		}
		out = append(out, fromGeomExPolygon(e))
	}
	return out
}

// safeMulti runs a polyclip boolean/offset op and converts the result back
// to slicer regions. It absorbs both the op's error return AND a panic,
// returning fallback in either case.
//
// The panic guard is deliberate: polyclip's scanline engine can hit a
// degeneracy on real-world mesh slices (coincident horizontal edges, near-
// pinch self-crossings) that the public boolean ops do not recover from —
// polyclip itself only recovers on its internal offset self-union path. A
// crashed boolean op must not take the whole slice down, so we recover here
// and fall back, the same "a slightly wrong layer beats a lost layer"
// philosophy the segment-chaining stage uses for non-manifold meshes.
func safeMulti(fn func() (geom.MultiPolygon, error), fallback []ExPolygon) (out []ExPolygon) {
	defer func() {
		if recover() != nil {
			out = fallback
		}
	}()
	res, err := fn()
	if err != nil {
		return fallback
	}
	return fromMulti(res)
}

// simplifyRegions resolves self-intersections and removes near-duplicate
// vertices from a set of regions. Raw slice contours from a non-manifold or
// noisy mesh can self-cross or carry sliver/duplicate vertices; feeding
// those straight into Offset and the boolean skin pass amplifies them into
// stray spikes and false-solid areas. Cleaning once up front keeps every
// downstream op working on simple polygons. On engine error/panic the input
// is returned unchanged.
func simplifyRegions(regions []ExPolygon) []ExPolygon {
	if len(regions) == 0 {
		return regions
	}
	out := safeMulti(func() (geom.MultiPolygon, error) {
		m, err := polyclip.Simplify(toMulti(regions))
		if err != nil {
			return m, err
		}
		// Weld vertices closer than the chaining grid and drop slivers so
		// collinear noise doesn't survive as micro-segments.
		return m.Clean(Epsilon, Epsilon*Epsilon), nil
	}, regions)
	return out
}

// union returns a ∪ b. On engine error/panic it falls back to the
// concatenation of inputs, which keeps the slice usable.
func union(a, b []ExPolygon) []ExPolygon {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	fallback := append(append([]ExPolygon{}, a...), b...)
	return safeMulti(func() (geom.MultiPolygon, error) {
		return polyclip.Union(toMulti(a), toMulti(b))
	}, fallback)
}

// difference returns a ∖ b (the part of a not covered by b). On engine
// error/panic it falls back to a unchanged.
func difference(a, b []ExPolygon) []ExPolygon {
	if len(a) == 0 || len(b) == 0 {
		return a
	}
	return safeMulti(func() (geom.MultiPolygon, error) {
		return polyclip.Difference(toMulti(a), toMulti(b))
	}, a)
}

// intersect returns a ∩ b. On engine error/panic it falls back to a (the
// region being classified), which is the conservative choice for skin
// detection: treating more area as solid is safer than leaving a gap.
func intersect(a, b []ExPolygon) []ExPolygon {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	return safeMulti(func() (geom.MultiPolygon, error) {
		return polyclip.Intersect(toMulti(a), toMulti(b))
	}, a)
}
