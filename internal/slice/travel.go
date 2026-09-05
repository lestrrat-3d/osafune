package slice

import "math"

// OptimizeTravel reorders a layer's paths to shorten the non-extruding travel
// between them. Paths are visited greedily nearest-first, and open paths are
// flipped to start at whichever end is closer to the current head — which
// turns the parallel scanlines of rectilinear infill into a boustrophedon
// (zig-zag) instead of a left-return after every line, the single biggest
// travel saving.
//
// Reordering is confined to each contiguous run of same-role paths, so the
// generator's phase order (outer walls, inner walls, solid skin, sparse
// infill) is preserved — interleaving walls and infill would drag travels
// across the visible surface and string — and the gcode emitter's contiguous
// ;TYPE: blocks stay intact. The head position carries across runs so the
// first path of each phase starts near where the previous phase ended.
func OptimizeTravel(layer *Layer) {
	if len(layer.Paths) < 2 {
		return
	}
	head := pathStart(&layer.Paths[0])
	out := make([]Path, 0, len(layer.Paths))
	for i := 0; i < len(layer.Paths); {
		j := i + 1
		for j < len(layer.Paths) && layer.Paths[j].Role == layer.Paths[i].Role {
			j++
		}
		head = orderRun(layer.Paths[i:j], head, &out)
		i = j
	}
	layer.Paths = out
}

// orderRun greedily appends the paths of one same-role run to out in
// nearest-first order starting from head, flipping open paths so they begin
// at their nearer end. It returns the head position after the last path.
func orderRun(run []Path, head Point2, out *[]Path) Point2 {
	visited := make([]bool, len(run))
	for range run {
		best := -1
		var bestCost float64
		bestFlip := false
		for k := range run {
			if visited[k] {
				continue
			}
			cost, flip := connectCost(head, &run[k])
			if best == -1 || cost < bestCost {
				best, bestCost, bestFlip = k, cost, flip
			}
		}
		if best == -1 {
			break
		}
		visited[best] = true
		p := run[best]
		if bestFlip {
			reversePoints(p.Points)
		}
		*out = append(*out, p)
		head = pathEnd(&p)
	}
	return head
}

// connectCost is the travel distance from head to the cheaper start of p, and
// whether reaching it means printing p reversed. Closed loops (and degenerate
// single-point paths) can only start at Points[0].
func connectCost(head Point2, p *Path) (float64, bool) {
	if len(p.Points) == 0 {
		return math.Inf(1), false
	}
	startD := dist(head, p.Points[0])
	if p.Closed || len(p.Points) < 2 {
		return startD, false
	}
	endD := dist(head, p.Points[len(p.Points)-1])
	if endD < startD {
		return endD, true
	}
	return startD, false
}

// pathStart is where the head must travel to before printing p.
func pathStart(p *Path) Point2 {
	if len(p.Points) == 0 {
		return Point2{}
	}
	return p.Points[0]
}

// pathEnd is where the head sits after printing p: back at the start for a
// closed loop, at the last point for an open path.
func pathEnd(p *Path) Point2 {
	if len(p.Points) == 0 {
		return Point2{}
	}
	if p.Closed {
		return p.Points[0]
	}
	return p.Points[len(p.Points)-1]
}

func reversePoints(pts []Point2) {
	for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
		pts[i], pts[j] = pts[j], pts[i]
	}
}

func dist(a, b Point2) float64 {
	return math.Hypot(a.X-b.X, a.Y-b.Y)
}
