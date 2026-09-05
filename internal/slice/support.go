package slice

import (
	"math"

	"github.com/lestrrat-3d/osafune/internal/config"
)

// Tree-support tuning constants. These trade support strength against print
// time and removability; they are deliberately not exposed as config knobs in
// this first version.
const (
	supportSampleSpacing = 3.0                // mm between support tips sampled in an overhang
	supportMinOverhang   = 4.0                // mm², ignore overhang slivers smaller than this
	supportLeanAngle     = 30 * math.Pi / 180 // branch lean from vertical per layer (toward merging)
	supportCircleSegs    = 16                 // polygon segments per pillar cross-section
)

// supNode is one support branch's cross-section at a layer: a centre and a
// radius that grows as branches merge into trunks toward the bed.
type supNode struct {
	pt Point2
	r  float64
}

// GenerateSupports builds tree (organic) supports for the sliced layers and
// returns the support extrusion paths per layer (parallel to layers). It
// detects overhangs angle-by-angle, seeds support tips just under them, and
// sweeps top-down growing branches that lean toward one another and merge
// into trunks — terminating when a branch reaches the bed or rests on the
// model. It reads only each layer's Contours and never mutates the layers.
//
// This is a first-cut tree support: no paint-on enforce/block, no tuned
// interface layers, and a simplified nearest-neighbour merge rather than a
// full collision-avoiding optimiser. It produces removable pillars that hold
// up overhangs, which is the core of the feature.
func GenerateSupports(layers []Layer, process *config.ResolvedProcess) [][]Path {
	out := make([][]Path, len(layers))
	if len(layers) < 2 {
		return out
	}
	tipR := process.SupportBranchDiameter * 0.5
	if tipR <= 0 {
		tipR = 1.0
	}
	maxR := tipR * 3
	threshold := process.SupportThreshold
	if threshold <= 0 {
		threshold = 50
	}
	speed := process.SupportSpeed
	if speed <= 0 {
		speed = process.InfillSpeed
	}
	width := process.LineWidth

	var carry []supNode
	for L := len(layers) - 1; L >= 0; L-- {
		layerH := layers[L].Height
		if layerH <= 0 {
			layerH = 0.2
		}
		leanStep := layerH * math.Tan(supportLeanAngle)

		// Lean + merge the branches descending from above, then place them at
		// this layer — dropping any that would collide with (rest on) the model.
		cur := mergeAndLean(carry, leanStep, maxR)
		var emit, next []supNode
		for _, n := range cur {
			if regionContains(layers[L].Contours, n.pt) {
				continue // inside the model here → the model supports it; stop
			}
			emit = append(emit, n)
			next = append(next, n)
		}
		out[L] = supportCircles(emit, width, speed)

		if L == 0 {
			break // branches that reach here rest on the bed
		}
		// New tips for this layer's overhang enter one layer down (the z-gap),
		// joining the branches that continue past this layer.
		maxDXY := layerH * math.Tan(threshold*math.Pi/180)
		carry = append(next, overhangTips(layers, L, maxDXY, tipR)...)
	}
	return out
}

// overhangTips finds the parts of layer L that jut more than maxDXY beyond the
// layer below (a downward face steeper than the threshold) and samples them to
// support tips on a grid.
func overhangTips(layers []Layer, L int, maxDXY, tipR float64) []supNode {
	below := offsetRegions(layers[L-1].Contours, -maxDXY) // dilate the support footprint
	overhang := difference(layers[L].Contours, below)

	var tips []supNode
	for _, r := range overhang {
		if r.Outer.Area() < supportMinOverhang {
			continue
		}
		min, max := boundsOf(r.BoundingBox())
		for x := min.X + supportSampleSpacing*0.5; x < max.X; x += supportSampleSpacing {
			for y := min.Y + supportSampleSpacing*0.5; y < max.Y; y += supportSampleSpacing {
				p := Point2{X: x, Y: y}
				if exContains(r, p) {
					tips = append(tips, supNode{pt: p, r: tipR})
				}
			}
		}
	}
	return tips
}

// mergeAndLean collapses overlapping branches into trunks and nudges each
// surviving branch toward its nearest neighbour, which over successive layers
// pulls separate branches together into shared trunks — the organic look.
func mergeAndLean(nodes []supNode, leanStep, maxR float64) []supNode {
	merged := mergeOverlapping(nodes, maxR)
	if len(merged) < 2 {
		return merged
	}
	leaned := make([]supNode, len(merged))
	copy(leaned, merged)
	for i := range merged {
		j := nearestOther(merged, i)
		if j < 0 {
			continue
		}
		d := merged[i].pt.Dist(merged[j].pt)
		if d < 1e-6 {
			continue
		}
		step := math.Min(leanStep, d*0.5)
		dir := merged[j].pt.Sub(merged[i].pt).Scale(1 / d)
		leaned[i].pt = merged[i].pt.Add(dir.Scale(step))
	}
	return leaned
}

// mergeOverlapping fuses branches whose discs overlap into a single thicker
// trunk (radius combined to conserve cross-sectional area, capped at maxR).
func mergeOverlapping(nodes []supNode, maxR float64) []supNode {
	used := make([]bool, len(nodes))
	var res []supNode
	for i := range nodes {
		if used[i] {
			continue
		}
		acc := nodes[i]
		for j := i + 1; j < len(nodes); j++ {
			if used[j] {
				continue
			}
			if acc.pt.Dist(nodes[j].pt) < acc.r+nodes[j].r {
				w1, w2 := acc.r*acc.r, nodes[j].r*nodes[j].r
				acc.pt = acc.pt.Scale(w1).Add(nodes[j].pt.Scale(w2)).Scale(1 / (w1 + w2))
				if nr := math.Sqrt(w1 + w2); nr < maxR {
					acc.r = nr
				} else {
					acc.r = maxR
				}
				used[j] = true
			}
		}
		res = append(res, acc)
	}
	return res
}

func nearestOther(nodes []supNode, i int) int {
	best := -1
	bestD := math.Inf(1)
	for j := range nodes {
		if j == i {
			continue
		}
		if d := nodes[i].pt.Dist(nodes[j].pt); d < bestD {
			bestD, best = d, j
		}
	}
	return best
}

// supportCircles turns each branch cross-section into a closed support loop.
func supportCircles(nodes []supNode, width, speed float64) []Path {
	if len(nodes) == 0 {
		return nil
	}
	ps := make([]Path, 0, len(nodes))
	for _, n := range nodes {
		r := n.r
		if r < width*0.6 {
			r = width * 0.6
		}
		pts := make([]Point2, 0, supportCircleSegs)
		for i := 0; i < supportCircleSegs; i++ {
			a := float64(i) / supportCircleSegs * 2 * math.Pi
			pts = append(pts, Point2{X: n.pt.X + r*math.Cos(a), Y: n.pt.Y + r*math.Sin(a)})
		}
		ps = append(ps, Path{Points: pts, Role: RoleSupport, Width: width, Speed: speed, Closed: true})
	}
	return ps
}

// regionContains reports whether p lies in the solid of any region (inside an
// outer ring and outside that region's holes).
func regionContains(regions []ExPolygon, p Point2) bool {
	for i := range regions {
		if exContains(regions[i], p) {
			return true
		}
	}
	return false
}

// exContains is point-in-ExPolygon: inside the outer ring and not in a hole.
func exContains(e ExPolygon, p Point2) bool {
	if !e.Outer.Contains(p) {
		return false
	}
	for _, h := range e.Holes {
		if h.Contains(p) {
			return false
		}
	}
	return true
}
