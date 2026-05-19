package slice

import "github.com/lestrrat-go/makislicer/internal/config"

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

	var infillAreas []ExPolygon
	for _, contour := range layer.Contours {
		current := contour
		for w := 0; w < process.Perimeters; w++ {
			// First wall: offset by half a lineWidth so the extrusion's
			// edge (which sits half a lineWidth from the path) aligns
			// with the slice contour. Subsequent walls: lineWidth apart.
			var d float64
			if w == 0 {
				d = lineWidth * 0.5
			} else {
				d = lineWidth
			}
			current = OffsetExPolygon(current, d)
			role := RolePerimeter
			speed := intSpeed
			if w == 0 {
				role = RoleExternalPerimeter
				speed = extSpeed
			}
			appendClosedPath(layer, current.Outer, role, lineWidth, speed)
			for _, h := range current.Holes {
				appendClosedPath(layer, h, role, lineWidth, speed)
			}
		}
		// Infill area = innermost wall offset inward by another half
		// lineWidth, so the infill extrusion's outer edge meets the
		// innermost wall's inner edge.
		infill := OffsetExPolygon(current, lineWidth*0.5)
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
