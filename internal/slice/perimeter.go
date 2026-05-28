package slice

import "github.com/lestrrat-go/osafune/internal/config"

// GeneratePerimeters lays down `Process.Perimeters` walls inside each
// contour of layer L. Wall 0 (the outermost) is tagged
// [RoleExternalPerimeter] so the gcode emitter picks the slower
// external-perimeter speed for it; the rest are [RolePerimeter].
//
// Walls are concentric closed loops, each offset inward by an extra
// lineWidth from the one outside it. The infill area returned is the
// region INSIDE the innermost wall — that is what [GenerateInfill]
// consumes for fill paths.
//
// The first layer uses FirstLayerLineWidth / FirstLayerSpeed; subsequent
// layers use LineWidth / PerimeterSpeed / ExternalPerimeterSpeed.
func GeneratePerimeters(layer *Layer, process *config.Process) []ExPolygon {
	if process.Perimeters < 1 {
		return layer.Contours
	}
	lineWidth, extSpeed, intSpeed := perimeterParams(layer, process)

	// `current` is the set of regions the next wall is traced inside.
	// Because a robust inward offset can split one region into several
	// disjoint pieces (a part that necks apart under the offset) or drop a
	// feature that collapses, we carry the whole set forward rather than
	// one contour at a time — every wall after the first is offset from
	// whatever the previous offset produced.
	current := layer.Contours
	for w := 0; w < process.Perimeters; w++ {
		// First wall: offset by half a lineWidth so the extrusion's edge
		// (which sits half a lineWidth from the path) aligns with the
		// slice contour. Subsequent walls: a full lineWidth apart.
		d := lineWidth
		if w == 0 {
			d = lineWidth * 0.5
		}
		current = offsetRegions(current, d)
		role := RolePerimeter
		speed := intSpeed
		if w == 0 {
			role = RoleExternalPerimeter
			speed = extSpeed
		}
		for _, region := range current {
			appendClosedPath(layer, region.Outer, role, lineWidth, speed)
			for _, h := range region.Holes {
				appendClosedPath(layer, h, role, lineWidth, speed)
			}
		}
	}
	// Infill area = innermost walls offset inward by another half
	// lineWidth, so the infill extrusion's outer edge meets the innermost
	// wall's inner edge.
	var infillAreas []ExPolygon
	for _, infill := range offsetRegions(current, lineWidth*0.5) {
		if len(infill.Outer) >= 3 {
			infillAreas = append(infillAreas, infill)
		}
	}
	return infillAreas
}

func perimeterParams(layer *Layer, process *config.Process) (width, extSpeed, intSpeed float64) {
	if layer.Index == 0 {
		return process.FirstLayerLineWidth, process.FirstLayerSpeed, process.FirstLayerSpeed
	}
	return process.LineWidth, process.ExternalPerimeterSpeed, process.PerimeterSpeed
}

func appendClosedPath(layer *Layer, loop Polygon, role PathRole, width, speed float64) {
	if len(loop) < 3 {
		return
	}
	pts := make([]Point2, len(loop))
	copy(pts, loop)
	layer.Paths = append(layer.Paths, Path{
		Points: pts,
		Role:   role,
		Width:  width,
		Speed:  speed,
		Closed: true,
	})
}
