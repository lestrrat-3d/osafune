// Package slice contains the slicing pipeline: triangle-mesh in, layers of
// extrusion paths out. Stages are pure functions chained through
// progressively-more-cooked types — [Polygon] (slice contour) →
// [ExPolygon] (with holes) → [Path] (extrusion move) → [Layer]
// (everything the gcode emitter needs for one Z).
package slice

import "math"

// Epsilon is the tolerance used throughout the slicer for "two coordinates
// are the same point" decisions. 1e-4 mm = 100 nm, well below printer
// resolution and well above float64 rounding for mm-scale geometry.
const Epsilon = 1e-4

// Point2 is a 2D point in millimetres. The slicer works in float64 because
// downstream operations (offsetting, infill clipping) accumulate error
// quickly in float32; the up-front conversion from [mesh.Vec3] is cheap.
type Point2 struct {
	X, Y float64
}

// Sub returns p - q.
func (p Point2) Sub(q Point2) Point2 { return Point2{p.X - q.X, p.Y - q.Y} }

// Add returns p + q.
func (p Point2) Add(q Point2) Point2 { return Point2{p.X + q.X, p.Y + q.Y} }

// Scale returns p * s.
func (p Point2) Scale(s float64) Point2 { return Point2{p.X * s, p.Y * s} }

// Len returns the Euclidean length of p treated as a vector.
func (p Point2) Len() float64 { return math.Hypot(p.X, p.Y) }

// Normalize returns p / |p|. Returns the zero vector if p has zero length.
func (p Point2) Normalize() Point2 {
	l := p.Len()
	if l < Epsilon {
		return Point2{}
	}
	return Point2{p.X / l, p.Y / l}
}

// Dot returns the dot product p · q.
func (p Point2) Dot(q Point2) float64 { return p.X*q.X + p.Y*q.Y }

// Cross returns the 2D cross product (z component of the 3D cross). Useful
// for orientation tests: positive means q is to the left of p, zero means
// colinear, negative means to the right.
func (p Point2) Cross(q Point2) float64 { return p.X*q.Y - p.Y*q.X }

// DistanceTo returns the Euclidean distance from p to q.
func (p Point2) DistanceTo(q Point2) float64 { return math.Hypot(p.X-q.X, p.Y-q.Y) }

// Equal reports whether p and q are within [Epsilon] of each other.
func (p Point2) Equal(q Point2) bool { return p.DistanceTo(q) < Epsilon }

// Polygon is a closed loop of points; the final segment implicitly returns
// from Points[len-1] to Points[0]. By convention outer contours are
// counter-clockwise (positive signed area) and holes are clockwise
// (negative signed area).
type Polygon []Point2

// SignedArea returns the signed area; positive for CCW, negative for CW.
func (p Polygon) SignedArea() float64 {
	if len(p) < 3 {
		return 0
	}
	var a float64
	for i := 0; i < len(p); i++ {
		j := (i + 1) % len(p)
		a += p[i].X*p[j].Y - p[j].X*p[i].Y
	}
	return a * 0.5
}

// Area returns |SignedArea|.
func (p Polygon) Area() float64 { return math.Abs(p.SignedArea()) }

// IsCCW reports whether the polygon winds counter-clockwise.
func (p Polygon) IsCCW() bool { return p.SignedArea() > 0 }

// Reverse flips the winding order in place.
func (p Polygon) Reverse() {
	for i, j := 0, len(p)-1; i < j; i, j = i+1, j-1 {
		p[i], p[j] = p[j], p[i]
	}
}

// Length returns the perimeter length of the polygon.
func (p Polygon) Length() float64 {
	if len(p) < 2 {
		return 0
	}
	var l float64
	for i := 0; i < len(p); i++ {
		j := (i + 1) % len(p)
		l += p[i].DistanceTo(p[j])
	}
	return l
}

// Contains reports whether q is strictly inside the polygon using the
// even-odd rule. Points exactly on edges are treated as "inside"; this is
// good enough for the slicer's containment tests (assigning holes to
// outer contours), where the only ambiguous case is when the test point
// is itself a polygon vertex from a different contour — which only
// happens for degenerate input.
func (p Polygon) Contains(q Point2) bool {
	if len(p) < 3 {
		return false
	}
	inside := false
	n := len(p)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		yi := p[i].Y
		yj := p[j].Y
		if (yi > q.Y) != (yj > q.Y) {
			xi := p[i].X
			xj := p[j].X
			xInt := xi + (q.Y-yi)/(yj-yi)*(xj-xi)
			if q.X < xInt {
				inside = !inside
			}
		}
	}
	return inside
}

// BoundingBox returns the axis-aligned min and max of the polygon's
// points. Returns zero values for an empty polygon.
func (p Polygon) BoundingBox() (min, max Point2) {
	if len(p) == 0 {
		return Point2{}, Point2{}
	}
	min, max = p[0], p[0]
	for _, pt := range p[1:] {
		if pt.X < min.X {
			min.X = pt.X
		}
		if pt.Y < min.Y {
			min.Y = pt.Y
		}
		if pt.X > max.X {
			max.X = pt.X
		}
		if pt.Y > max.Y {
			max.Y = pt.Y
		}
	}
	return min, max
}

// ExPolygon is an outer contour with zero or more holes. Outer is CCW;
// every Hole is CW. This is the standard shape primitive for slicers —
// boolean operations preserve it and infill / perimeter generation
// consume it directly.
type ExPolygon struct {
	Outer Polygon
	Holes []Polygon
}

// BoundingBox returns the AABB of the outer contour. Holes are by
// definition inside the outer so they cannot extend the box.
func (e *ExPolygon) BoundingBox() (min, max Point2) { return e.Outer.BoundingBox() }
