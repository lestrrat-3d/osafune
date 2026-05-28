package slice

import (
	"math"

	"github.com/lestrrat-go/osafune/internal/config"
)

// SplitBridges separates a layer's solid skin into the part supported by the
// layer below (which stays ordinary solid fill) and the part that overhangs
// empty space (a bridge). A point is supported when it lies over the previous
// layer's printed cross-section, so the bridge is the boolean difference of
// the solid areas and that footprint, and the supported solid is their
// intersection.
//
// The first layer (layerIndex 0) rests on the bed, not on air, so none of it
// bridges. With no previous footprint, all the solid stays solid.
// It returns (bridge, supported).
func SplitBridges(solidAreas []ExPolygon, layerIndex int, layers []Layer) ([]ExPolygon, []ExPolygon) {
	if layerIndex <= 0 || len(solidAreas) == 0 {
		return nil, solidAreas
	}
	support := layers[layerIndex-1].Contours
	if len(support) == 0 {
		// Nothing beneath at all — the whole solid surface bridges.
		return solidAreas, nil
	}
	return difference(solidAreas, support), intersect(solidAreas, support)
}

// GenerateBridges fills each bridge region with solid rectilinear strands run
// along the orientation that minimises the longest unsupported span (so the
// filament crosses the gap by its shortest dimension), tagged [RoleBridge] at
// the bridge speed and flow so the gcode writer prints them slowly with the
// cooling fan boosted. Appends to layer.Paths.
func GenerateBridges(layer *Layer, bridgeAreas []ExPolygon, process *config.Process) {
	if len(bridgeAreas) == 0 {
		return
	}
	lineWidth := process.LineWidth
	if layer.Index == 0 {
		lineWidth = process.FirstLayerLineWidth
	}
	if lineWidth <= 0 {
		return
	}
	flow := process.BridgeFlow
	if flow <= 0 {
		flow = 1.0
	}
	speed := process.BridgeSpeed
	if speed <= 0 {
		speed = process.SolidInfillSpeed
	}
	for _, a := range bridgeAreas {
		angle := bestBridgeAngle(a, lineWidth)
		// Space strands at the full line width for solid coverage, but extrude
		// at width×flow so the bridge flow ratio scales the deposited volume.
		appendRectilinear(layer, a, angle, lineWidth, lineWidth*flow, speed, RoleBridge)
	}
}

// bestBridgeAngle returns the fill angle (degrees), among a few candidates,
// whose longest scanline is shortest — i.e. the direction whose strands span
// the region's narrow dimension and so sag the least. Falls back to 0.
func bestBridgeAngle(area ExPolygon, spacing float64) float64 {
	best := 0.0
	bestSpan := math.Inf(1)
	for _, ang := range []float64{0, 45, 90, 135} {
		var maxSpan float64
		for _, ln := range rectilinearLines(area, ang, spacing) {
			if len(ln) < 2 {
				continue
			}
			if s := dist(ln[0], ln[len(ln)-1]); s > maxSpan {
				maxSpan = s
			}
		}
		if maxSpan > 0 && maxSpan < bestSpan {
			bestSpan = maxSpan
			best = ang
		}
	}
	return best
}
