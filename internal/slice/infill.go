package slice

import (
	"math"
	"sort"

	"github.com/lestrrat-3d/osafune/internal/config"
)

// GenerateInfill fills the regions inside the innermost perimeter wall.
// The fill region has already been split by [ClassifySkin] into solid
// (skin) and sparse parts using polygon boolean ops across neighbouring
// layers: solidAreas are the exposed top/bottom surfaces and the shell
// beneath them, sparseAreas are the enclosed interior.
//
// Solid regions get 100% rectilinear coverage (lines spaced at exactly
// lineWidth); sparse regions follow the user-selected
// [config.InfillPattern] at the chosen density. This is geometric skin
// detection — a shape that narrows upward now gets a solid top exactly
// where it loses the layer above, instead of a thin sparse top.
func GenerateInfill(layer *Layer, solidAreas, sparseAreas []ExPolygon, process *config.ResolvedProcess) {
	width := process.LineWidth
	if layer.Index == 0 {
		width = process.FirstLayerLineWidth
	}
	angle := infillAngleForLayer(layer.Index, process.InfillAngles)

	// Solid skin: rectilinear lines at exactly lineWidth spacing for full
	// coverage; the user's sparse pattern is irrelevant here.
	solidSpeed := process.SolidInfillSpeed
	if layer.Index == 0 {
		solidSpeed = process.FirstLayerSpeed
	}
	for _, a := range solidAreas {
		appendRectilinear(layer, a, angle, width, width, solidSpeed, RoleSolidInfill)
	}

	// Sparse interior: user pattern at density-derived spacing.
	sparseSpeed := process.InfillSpeed
	if layer.Index == 0 {
		sparseSpeed = process.FirstLayerSpeed
	}
	density := process.InfillDensity
	if density <= 0 {
		density = 0.01 // avoid divide-by-zero; effectively no infill
	}
	spacing := width / density
	pattern := process.InfillPattern
	if pattern == "" {
		pattern = config.InfillRectilinear
	}
	for _, a := range sparseAreas {
		emitPattern(layer, a, pattern, layer.Index, spacing, width, sparseSpeed, RoleInfill, process.InfillAngles)
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
// and emits each resulting loop as a closed path. A robust inward offset
// can split a region into several disjoint pieces (a dumbbell past its
// neck), so each iteration carries a worklist of regions and offsets them
// all. A piece is emitted until it shrinks below one spacing of area or
// the offset collapses it, which terminates the loop for arbitrary shapes.
func appendConcentric(layer *Layer, area ExPolygon, spacing, width, speed float64, role PathRole) {
	// The first ring sits half a spacing inside the wall so the extruded
	// edge meets the inner wall's edge cleanly, the same trick we use
	// for the outermost perimeter.
	current := OffsetExPolygon(area, spacing*0.5)
	for safety := 0; safety < 1000 && len(current) > 0; safety++ {
		var survivors []ExPolygon
		for _, region := range current {
			if len(region.Outer) < 3 || region.Outer.Area() < spacing*spacing {
				continue
			}
			appendClosedPath(layer, region.Outer, role, width, speed)
			for _, h := range region.Holes {
				appendClosedPath(layer, h, role, width, speed)
			}
			survivors = append(survivors, region)
		}
		if len(survivors) == 0 {
			return
		}
		current = offsetRegions(survivors, spacing)
	}
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
		return Point2{X: p.X*cos + p.Y*sin, Y: -p.X*sin + p.Y*cos}
	}
	unrot := func(p Point2) Point2 {
		return Point2{X: p.X*cos - p.Y*sin, Y: p.X*sin + p.Y*cos}
	}

	rotated := rotateExPolygon(e, rot)
	lo, hi := boundsOf(rotated.BoundingBox())
	if hi.Y-lo.Y < spacing*0.5 {
		return nil
	}

	// Anchor lines on multiples of spacing so adjacent layers' patterns
	// line up at the boundary; otherwise the pattern jitters between
	// layers and printed surfaces look noisy.
	yStart := math.Floor(lo.Y/spacing)*spacing + spacing*0.5
	var out [][]Point2
	for y := yStart; y <= hi.Y; y += spacing {
		xs := scanlineCrossings(rotated, y)
		// Inside the polygon between pairs of sorted crossings.
		sort.Float64s(xs)
		for i := 0; i+1 < len(xs); i += 2 {
			x0, x1 := xs[i], xs[i+1]
			if x1-x0 < Epsilon {
				continue
			}
			out = append(out, []Point2{
				unrot(Point2{X: x0, Y: y}),
				unrot(Point2{X: x1, Y: y}),
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
	for i := range n {
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
