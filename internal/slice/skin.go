package slice

// ClassifySkin splits each layer's fill region into solid (skin) and
// sparse parts by comparing it against its vertical neighbours with
// polygon boolean ops — the geometric top/bottom-shell detection the MVP
// could not do without a real clipper.
//
// areas[i] is the fill region of layer i (inside the innermost wall). The
// result is two parallel slices: solid[i] is the region that must be
// printed at 100% (it lies within topLayers of a surface exposed to air
// above, or within bottomLayers of a surface exposed below), and
// sparse[i] is the rest of areas[i].
//
// The exposure test is the standard one:
//
//   - A layer's region not covered by the layer ABOVE it is exposed to air
//     on top (the model narrows there, or it is the topmost layer). Every
//     layer within topLayers BELOW such a surface must be solid to roof it.
//   - Symmetrically for the layer BELOW and bottomLayers, to floor an
//     overhang or the buildplate-facing bottom.
//
// For a shape with constant cross-section (e.g. a cube) the per-layer
// difference is empty for interior layers, so only the first bottomLayers
// and last topLayers come out solid — matching the old index-based rule,
// but now derived from geometry rather than assumed.
func ClassifySkin(areas [][]ExPolygon, topLayers, bottomLayers int) (solid, sparse [][]ExPolygon) {
	n := len(areas)
	solid = make([][]ExPolygon, n)
	sparse = make([][]ExPolygon, n)
	if n == 0 {
		return solid, sparse
	}

	// exposedTop[i]: part of layer i with no material directly above it.
	// exposedBottom[i]: part of layer i with no material directly below.
	// The topmost / bottommost layers are exposed over their whole area.
	exposedTop := make([][]ExPolygon, n)
	exposedBottom := make([][]ExPolygon, n)
	for i := range n {
		if i == n-1 {
			exposedTop[i] = areas[i]
		} else {
			exposedTop[i] = difference(areas[i], areas[i+1])
		}
		if i == 0 {
			exposedBottom[i] = areas[i]
		} else {
			exposedBottom[i] = difference(areas[i], areas[i-1])
		}
	}

	for i := range n {
		if len(areas[i]) == 0 {
			continue
		}
		// Seed = every exposed surface whose shell reaches layer i:
		// top surfaces in [i, i+topLayers-1] and bottom surfaces in
		// [i-bottomLayers+1, i].
		var seed []ExPolygon
		for j := i; j < n && j < i+topLayers; j++ {
			seed = union(seed, exposedTop[j])
		}
		for j := i; j >= 0 && j > i-bottomLayers; j-- {
			seed = union(seed, exposedBottom[j])
		}
		if len(seed) == 0 {
			sparse[i] = areas[i]
			continue
		}
		s := intersect(areas[i], seed)
		if len(s) == 0 {
			sparse[i] = areas[i]
			continue
		}
		solid[i] = s
		sparse[i] = difference(areas[i], s)
	}
	return solid, sparse
}
