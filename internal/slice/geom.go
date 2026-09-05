// Package slice contains the slicing pipeline: triangle-mesh in, layers of
// extrusion paths out. Stages are pure functions chained through
// progressively-more-cooked types — [Polygon] (slice contour) →
// [ExPolygon] (with holes) → [Path] (extrusion move) → [Layer]
// (everything the gcode emitter needs for one Z).
package slice

import (
	"github.com/lestrrat-go/polyclip/geom"
)

// Epsilon is the tolerance used throughout the slicer for "two coordinates
// are the same point" decisions. 1e-4 mm = 100 nm, well below printer
// resolution and well above float64 rounding for mm-scale geometry.
const Epsilon = 1e-4

// The slicer's 2D geometry is polyclip's, under the slicer's names. These are
// aliases, not definitions: the same types the boolean and offset operations
// take, so a contour crosses into polyclip and back with no conversion and no
// second representation to drift out of step with this one.
//
// Point2 is a point in millimetres. The slicer works in float64 because
// downstream operations (offsetting, infill clipping) accumulate error quickly
// in float32; the up-front conversion from [mesh.Vec3] is cheap. Its Z field is
// polyclip's auxiliary channel and the slicer leaves it alone — layer height
// lives on the [Layer], not on its points.
//
// A Polygon is a closed loop whose final segment implicitly returns to the
// first point. By convention outer contours are counter-clockwise (positive
// signed area) and holes are clockwise (negative), which is polyclip's
// convention too.
type (
	Point2    = geom.Point
	Polygon   = geom.Polygon
	ExPolygon = geom.ExPolygon
)

// samePoint reports whether a and b are the same point to within [Epsilon].
// polyclip's [geom.Point.Equal] takes the tolerance as an argument, because a
// general geometry library has no domain epsilon of its own; the slicer does,
// so this names it once.
func samePoint(a, b Point2) bool { return a.Equal(b, Epsilon) }

// boundsOf returns the corners of a region's bounding box, the shape the
// slicer's scanline code wants. polyclip returns a [geom.BBox]; unpacking it
// here keeps that at one place.
func boundsOf(b geom.BBox) (min, max Point2) { return b.Min, b.Max }
