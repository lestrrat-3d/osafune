package slice

import (
	"math"
	"sort"

	"github.com/lestrrat-go/makislicer/internal/config"
)

// GenerateInfill fills the regions inside the innermost perimeter wall
// with a rectilinear (parallel-line) pattern, alternating angles per
// layer. Top and bottom skin layers — the first BottomLayers and the
// last TopLayers — get solid fill (lines spaced at exactly lineWidth);
// the rest get sparse fill (spacing = lineWidth / density).
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
	angle := infillAngleForLayer(layer.Index, process.InfillAngles)
	for _, a := range areas {
		lines := rectilinearLines(a, angle, spacing)
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
