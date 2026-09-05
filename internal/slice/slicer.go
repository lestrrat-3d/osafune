package slice

import (
	"github.com/lestrrat-3d/osafune/internal/config"
	"github.com/lestrrat-3d/osafune/internal/mesh"
)

// Slice runs the full pipeline: Z-sweep → perimeter walls → skin detection
// → infill. It does not order paths for travel-minimisation (paths come
// out in the order they were generated) and it does not emit gcode — the
// [gcode] package consumes the returned layers.
//
// Skin detection runs as a whole-model pass between perimeters and infill
// because it compares each layer's fill region against its vertical
// neighbours; perimeters must be generated for every layer first so the
// fill regions exist to compare.
//
// The caller passes a single mesh: project-level concerns (multiple
// instances, plates, translations) are handled by [project.Project.PlateMesh]
// before this function ever sees the geometry.
func Slice(m *mesh.Mesh, printer *config.ResolvedPrinter, process *config.ResolvedProcess) []Layer {
	if len(m.Triangles) == 0 {
		return nil
	}
	layers := SliceMesh(m, process.FirstLayerHeight, process.LayerHeight)

	// Pass 1: walls for every layer, collecting the per-layer fill regions.
	// Clean the raw contours first — chaining a non-manifold or noisy mesh
	// can leave self-crossing loops and near-duplicate vertices that Offset
	// and the boolean skin pass would otherwise amplify into stray spikes
	// and false-solid regions (and a far larger toolpath count).
	fillAreas := make([][]ExPolygon, len(layers))
	for i := range layers {
		layers[i].Contours = simplifyRegions(layers[i].Contours)
		fillAreas[i] = GeneratePerimeters(&layers[i], process)
	}

	// Pass 2: classify each fill region into solid skin / sparse interior
	// by comparing it against the layers above and below.
	// Discard exposure slivers thinner than a fifth of a line width so
	// adjacent-layer contour noise cannot seed thin spurious solid rings on
	// what are really vertical surfaces. (OrcaSlicer opens its top/bottom
	// diff by ext_perimeter_width/10, which likewise removes features under
	// ~one fifth of a line wide.)
	solid, sparse := ClassifySkin(fillAreas, process.TopLayers, process.BottomLayers, process.LineWidth/5)

	// Tree supports look at every layer's footprint at once (top-down branch
	// growth), so they are generated before the per-layer finalisation and
	// the resulting pillars are merged into each layer's paths below.
	var supports [][]Path
	if process.SupportEnable {
		supports = GenerateSupports(layers, process)
	}

	// Pass 3: lay down the actual fill, place the perimeter seams, then
	// reorder each layer's paths to minimise non-extruding travel
	// (boustrophedon infill, nearest-first loops) without disturbing the
	// wall→infill phase order. Seams are placed before the travel pass so it
	// orders loops by their final seam point and preserves it.
	for i := range layers {
		// Split the solid skin into bridges (overhanging air, no layer below)
		// and ordinary supported solid; bridges get span-aligned strands at
		// bridge speed/flow with the fan boosted by the gcode writer.
		bridge, supportedSolid := SplitBridges(solid[i], i, layers)
		GenerateInfill(&layers[i], supportedSolid, sparse[i], process)
		GenerateBridges(&layers[i], bridge, process)
		if supports != nil {
			layers[i].Paths = append(layers[i].Paths, supports[i]...)
		}
		PlaceSeams(&layers[i], process.SeamPosition)
		// Skirt/brim are first-layer-only adhesion loops; prepend before the
		// travel pass so they head the layer and get chained with it.
		if i == 0 {
			GenerateSkirtBrim(&layers[i], process, process.FirstLayerLineWidth, process.FirstLayerSpeed)
		}
		OptimizeTravel(&layers[i])
	}
	return layers
}
