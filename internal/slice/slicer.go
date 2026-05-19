package slice

import (
	"github.com/lestrrat-go/makislicer/internal/config"
	"github.com/lestrrat-go/makislicer/internal/mesh"
)

// Slice runs the full MVP pipeline: Z-sweep → perimeter walls →
// rectilinear infill. It does not order paths for travel-minimisation
// (paths come out in the order they were generated) and it does not
// emit gcode — the [gcode] package consumes the returned layers.
//
// The caller passes a single mesh: project-level concerns (multiple
// instances, plates, translations) are handled by [project.Project.PlateMesh]
// before this function ever sees the geometry.
func Slice(m *mesh.Mesh, printer *config.Printer, process *config.Process) []Layer {
	if len(m.Triangles) == 0 {
		return nil
	}
	layers := SliceMesh(m, process.FirstLayerHeight, process.LayerHeight)
	total := len(layers)
	for i := range layers {
		infillAreas := GeneratePerimeters(&layers[i], process)
		GenerateInfill(&layers[i], infillAreas, process, total)
	}
	return layers
}
