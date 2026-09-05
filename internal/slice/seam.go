package slice

import "github.com/lestrrat-3d/osafune/internal/config"

// PlaceSeams rotates each closed perimeter loop so its start point — the
// visible seam, where the nozzle starts the loop and later stops on it —
// lands at the vertex chosen by pos, instead of wherever the sliced contour
// happened to begin. Open paths (infill) are left alone.
//
//   - SeamAligned (default): the rear-most vertex (max Y, ties broken by
//     max X). The same reference every layer, so seams stack into one tidy
//     vertical line.
//   - SeamRandom: a per-layer, per-loop pseudo-random vertex, scattering the
//     seam so it reads as faint speckle rather than a line on organic shapes.
//
// Run before [OptimizeTravel]: that pass orders loops by their start point
// and never reverses a closed path, so it preserves the seam placed here.
func PlaceSeams(layer *Layer, pos config.SeamPosition) {
	for i := range layer.Paths {
		p := &layer.Paths[i]
		if !p.Closed || len(p.Points) < 3 {
			continue
		}
		rotatePoints(p.Points, seamIndex(p.Points, pos, layer.Index, i))
	}
}

// seamIndex picks the vertex index of pts to start the loop at.
func seamIndex(pts []Point2, pos config.SeamPosition, layerIdx, pathOrd int) int {
	if pos == config.SeamRandom {
		// Deterministic hash of (layer, loop) so output stays reproducible.
		h := uint32(layerIdx)*2654435761 + uint32(pathOrd)*40503 + 12345
		return int(h % uint32(len(pts)))
	}
	// SeamAligned / empty → the rear-most vertex.
	best := 0
	for i := 1; i < len(pts); i++ {
		if pts[i].Y > pts[best].Y || (pts[i].Y == pts[best].Y && pts[i].X > pts[best].X) {
			best = i
		}
	}
	return best
}

// rotatePoints rotates pts in place so index s becomes the new first point,
// e.g. [a b c d] with s=2 → [c d a b]. The loop is closed (its return to the
// start is implicit), so this only moves the seam — the printed geometry is
// unchanged.
func rotatePoints(pts []Point2, s int) {
	n := len(pts)
	s = ((s % n) + n) % n
	if s == 0 {
		return
	}
	rotated := make([]Point2, 0, n)
	rotated = append(rotated, pts[s:]...)
	rotated = append(rotated, pts[:s]...)
	copy(pts, rotated)
}
