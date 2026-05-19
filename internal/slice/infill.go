package slice

import (
	"math"
	"sort"

	"github.com/lestrrat-go/makislicer/internal/config"
)

// GenerateInfill fills the regions inside the innermost perimeter wall.
// Top and bottom skin layers — the first BottomLayers and the last
// TopLayers — always use solid rectilinear fill (lines spaced at exactly
// lineWidth); the rest follow the user-selected [config.InfillPattern]
// at the chosen density.
//
// The MVP intentionally does NOT do per-region geometric skin detection
// (i.e. "this part of the layer has no layer above, so it must be
// solid"). That requires polygon boolean ops, which the naive offsetter
// doesn't provide. Without it, overhangs and shapes that narrow upward
// will print a thin top where they shouldn't — acceptable for a first
// working slicer.
func GenerateInfill(layer *Layer, areas []ExPolygon, process *config.Process, totalLayers int) {
	if len(areas) == 0 {
		return
	}
	width, spacing, speed, role := infillParams(layer, process, totalLayers)
	pattern := process.InfillPattern
	if role == RoleSolidInfill {
		// Skin layers ignore the user's chosen sparse pattern — they
		// need 100% coverage which only rectilinear lines at exactly
		// lineWidth spacing provides.
		pattern = config.InfillRectilinear
	}
	if pattern == "" {
		pattern = config.InfillRectilinear
	}
	for _, a := range areas {
		emitPattern(layer, a, pattern, layer.Index, spacing, width, speed, role, process.InfillAngles)
	}
}

// emitPattern dispatches the per-area fill generation to the right
// algorithm. Each branch appends one or more [Path]s to the layer.
func emitPattern(layer *Layer, area ExPolygon, pattern config.InfillPattern, layerIdx int, spacing, width, speed float64, role PathRole, angles []float64) {
	switch pattern {
	case config.InfillGrid:
		// Two perpendicular sets per layer. Double the spacing so total
		// extruded volume matches the requested density.
		base := infillAngleForLayer(layerIdx, angles)
		appendRectilinear(layer, area, base, spacing*2, width, speed, role)
		appendRectilinear(layer, area, base+90, spacing*2, width, speed, role)
	case config.InfillTriangles:
		// Three sets at 60° increments. Triple the spacing so volumetric
		// density still matches the requested fraction.
		base := infillAngleForLayer(layerIdx, angles)
		appendRectilinear(layer, area, base, spacing*3, width, speed, role)
		appendRectilinear(layer, area, base+60, spacing*3, width, speed, role)
		appendRectilinear(layer, area, base+120, spacing*3, width, speed, role)
	case config.InfillConcentric:
		appendConcentric(layer, area, spacing, width, speed, role)
	default: // rectilinear
		angle := infillAngleForLayer(layerIdx, angles)
		appendRectilinear(layer, area, angle, spacing, width, speed, role)
	}
}

func appendRectilinear(layer *Layer, area ExPolygon, angleDeg, spacing, width, speed float64, role PathRole) {
	lines := rectilinearLines(area, angleDeg, spacing)
	for _, ln := range lines {
		pts := make([]Point2, len(ln))
		copy(pts, ln)
		layer.Paths = append(layer.Paths, Path{
			Points: pts,
			Role:   role,
			Width:  width,
			Speed:  speed,
			Closed: false,
		})
	}
}

// appendConcentric repeatedly offsets the infill area inward by spacing
// and emits each resulting loop as a closed path. Stops when an offset
// produces a degenerate (<3 vertex) outer or when the polygon has
// shrunk to zero area, which serves as a natural termination for
// arbitrary input shapes.
func appendConcentric(layer *Layer, area ExPolygon, spacing, width, speed float64, role PathRole) {
	// The first ring sits half a spacing inside the wall so the extruded
	// edge meets the inner wall's edge cleanly, the same trick we use
	// for the outermost perimeter.
	current := OffsetExPolygon(area, spacing*0.5)
	for safety := 0; safety < 1000; safety++ {
		if len(current.Outer) < 3 {
			return
		}
		if current.Outer.Area() < spacing*spacing {
			return
		}
		pts := make([]Point2, len(current.Outer))
		copy(pts, current.Outer)
		layer.Paths = append(layer.Paths, Path{
			Points: pts,
			Role:   role,
			Width:  width,
			Speed:  speed,
			Closed: true,
		})
		for _, h := range current.Holes {
			if len(h) < 3 {
				continue
			}
			hpts := make([]Point2, len(h))
			copy(hpts, h)
			layer.Paths = append(layer.Paths, Path{
				Points: hpts,
				Role:   role,
				Width:  width,
				Speed:  speed,
				Closed: true,
			})
		}
		current = OffsetExPolygon(current, spacing)
	}
}

func infillParams(layer *Layer, process *config.Process, totalLayers int) (width, spacing, speed float64, role PathRole) {
	width = process.LineWidth
	if layer.Index == 0 {
		width = process.FirstLayerLineWidth
	}
	solid := layer.Index < process.BottomLayers || layer.Index >= totalLayers-process.TopLayers
	if solid {
		role = RoleSolidInfill
		speed = process.SolidInfillSpeed
		spacing = width
	} else {
		role = RoleInfill
		speed = process.InfillSpeed
		density := process.InfillDensity
		if density <= 0 {
			density = 0.01 // avoid divide-by-zero; effectively no infill
		}
		spacing = width / density
	}
	if layer.Index == 0 {
		speed = process.FirstLayerSpeed
	}
	return width, spacing, speed, role
}

func infillAngleForLayer(layerIdx int, angles []float64) float64 {
	if len(angles) == 0 {
		return 45
	}
	return angles[layerIdx%len(angles)]
}

// rectilinearLines fills e with parallel lines at the given angle and
// spacing. Each returned line is a 2-point segment (start, end) in
// world (post-rotation) coordinates. The MVP emits them in
// monotonically-increasing-coordinate order with no zig-zag connection
// between them — the gcode emitter inserts travels.
func rectilinearLines(e ExPolygon, angleDeg, spacing float64) [][]Point2 {
	if len(e.Outer) < 3 {
		return nil
	}
	rad := angleDeg * math.Pi / 180
	sin, cos := math.Sin(rad), math.Cos(rad)
	// Rotate every vertex by -angle so infill lines run horizontally
	// in the rotated frame; we slice the bbox with horizontal lines,
	// then rotate the resulting segments back.
	rot := func(p Point2) Point2 {
		return Point2{p.X*cos + p.Y*sin, -p.X*sin + p.Y*cos}
	}
	unrot := func(p Point2) Point2 {
		return Point2{p.X*cos - p.Y*sin, p.X*sin + p.Y*cos}
	}

	rotated := rotateExPolygon(e, rot)
	min, max := rotated.BoundingBox()
	if max.Y-min.Y < spacing*0.5 {
		return nil
	}

	// Anchor lines on multiples of spacing so adjacent layers' patterns
	// line up at the boundary; otherwise the pattern jitters between
	// layers and printed surfaces look noisy.
	yStart := math.Floor(min.Y/spacing)*spacing + spacing*0.5
	var out [][]Point2
	for y := yStart; y <= max.Y; y += spacing {
		xs := scanlineCrossings(rotated, y)
		// Inside the polygon between pairs of sorted crossings.
		sort.Float64s(xs)
		for i := 0; i+1 < len(xs); i += 2 {
			x0, x1 := xs[i], xs[i+1]
			if x1-x0 < Epsilon {
				continue
			}
			out = append(out, []Point2{
				unrot(Point2{x0, y}),
				unrot(Point2{x1, y}),
			})
		}
	}
	return out
}

func rotateExPolygon(e ExPolygon, f func(Point2) Point2) ExPolygon {
	out := ExPolygon{Outer: rotatePolygon(e.Outer, f)}
	for _, h := range e.Holes {
		out.Holes = append(out.Holes, rotatePolygon(h, f))
	}
	return out
}

func rotatePolygon(p Polygon, f func(Point2) Point2) Polygon {
	out := make(Polygon, len(p))
	for i, pt := range p {
		out[i] = f(pt)
	}
	return out
}

// scanlineCrossings returns the X coordinates where the horizontal line
// y=Y crosses the boundary of e (outer and holes). Even-odd ordering of
// the sorted result identifies inside/outside intervals.
func scanlineCrossings(e ExPolygon, y float64) []float64 {
	xs := polyCrossings(e.Outer, y)
	for _, h := range e.Holes {
		xs = append(xs, polyCrossings(h, y)...)
	}
	return xs
}

func polyCrossings(p Polygon, y float64) []float64 {
	if len(p) < 3 {
		return nil
	}
	var xs []float64
	n := len(p)
	for i := 0; i < n; i++ {
		a := p[i]
		b := p[(i+1)%n]
		// Half-open rule: treat the edge as containing the lower endpoint
		// but not the upper one. Prevents counting both edges meeting at
		// a vertex when the scanline grazes that vertex's Y.
		if (a.Y <= y && b.Y > y) || (b.Y <= y && a.Y > y) {
			t := (y - a.Y) / (b.Y - a.Y)
			xs = append(xs, a.X+t*(b.X-a.X))
		}
	}
	return xs
}
