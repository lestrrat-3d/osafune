// Package project layers OrcaSlicer's project / plate / model-instance
// concepts on top of the viewer's [mesh.Scene]. A [Project] owns one or
// more [Plate]s, each of which selects a printer / filament / process
// and references the [ModelInstance]s arranged on it. The MVP only ever
// constructs a single plate, but the data model leaves room for the
// multi-plate UX OrcaSlicer ships without changing the slicer interface.
package project

import (
	"github.com/lestrrat-3d/osafune/internal/config"
	"github.com/lestrrat-3d/osafune/internal/mesh"
)

// Transform is a placement of a [ModelObject] on a [Plate]: a translation
// in millimetres applied to the mesh's vertices. Rotation and scale are
// deliberately omitted for the MVP; the slicer assumes objects are
// already oriented and sized as the user wants.
type Transform struct {
	Translate mesh.Vec3
}

// ModelObject is a named mesh loaded from an STL or 3MF file. Multiple
// [ModelInstance]s can refer to the same ModelObject (think: an array of
// the same part on the bed) so the heavy geometry is stored once.
type ModelObject struct {
	Name string
	Mesh mesh.Mesh
}

// ModelInstance is one placement of a [ModelObject] on a [Plate]. The
// ObjectIndex refers into [Project.Objects].
type ModelInstance struct {
	ObjectIndex int
	Transform   Transform
}

// Plate is one build plate's worth of instances together with the printer
// / filament / process profiles selected for slicing them. A project may
// have several plates; the slicer runs once per plate.
type Plate struct {
	Name      string
	Printer   config.Printer
	Filament  config.Filament
	Process   config.Process
	Instances []ModelInstance
}

// ResolvedPlate is a plate's three profiles converted to the units the
// slicing pipeline and the gcode emitter work in. Slicing a plate takes one
// of these rather than the profiles themselves, so the unit checks happen
// once per plate instead of once per toolpath.
type ResolvedPlate struct {
	Printer  config.ResolvedPrinter
	Filament config.ResolvedFilament
	Process  config.ResolvedProcess
}

// Resolve converts the plate's three profiles, reporting the first field
// whose quantity is not of the kind that field measures.
func (p *Plate) Resolve() (ResolvedPlate, error) {
	printer, err := p.Printer.Resolve()
	if err != nil {
		return ResolvedPlate{}, err
	}
	filament, err := p.Filament.Resolve()
	if err != nil {
		return ResolvedPlate{}, err
	}
	process, err := p.Process.Resolve()
	if err != nil {
		return ResolvedPlate{}, err
	}
	return ResolvedPlate{Printer: printer, Filament: filament, Process: process}, nil
}

// Project is the unit a user opens, edits and saves. It owns the meshes
// and the plates that reference them. The MVP keeps everything in memory
// — there is no on-disk project format yet.
type Project struct {
	Objects []ModelObject
	Plates  []Plate
}

// NewFromScene builds a single-plate Project from a viewer [mesh.Scene]:
// every object becomes a ModelObject with one identity-placed instance on
// the default plate, using default printer / filament / process profiles.
// This is the bridge between today's viewer (which thinks in Scenes) and
// the slicer (which thinks in Projects).
func NewFromScene(s *mesh.Scene) *Project {
	p := &Project{
		Objects: make([]ModelObject, 0, len(s.Objects)),
	}
	plate := Plate{
		Name:     "Plate 1",
		Printer:  config.DefaultPrinter(),
		Filament: config.DefaultFilament(),
		Process:  config.DefaultProcess(),
	}
	for i := range s.Objects {
		p.Objects = append(p.Objects, ModelObject{
			Name: s.Objects[i].Name,
			Mesh: s.Objects[i].Mesh,
		})
		plate.Instances = append(plate.Instances, ModelInstance{ObjectIndex: i})
	}
	p.Plates = []Plate{plate}
	// The plate carries the built-in default profiles, whose quantities are
	// built from the units package's own constructors, so the only failure
	// AutoArrange can report cannot arise here.
	_ = p.AutoArrange(0)
	return p
}

// AutoArrange sets the translation on every instance of the plate so the
// combined mesh sits with its minimum Z on 0 (bed level) and its XY
// centroid at the centre of the printable area. The MVP only ever has
// one instance per plate, but the API takes the whole plate so multi-
// instance layouts can later replace the body with a real packing
// algorithm without changing callers.
//
// It reports an error when the plate's printer profile carries a bed size
// that is not a length, because the centre of the build area cannot be
// computed without one.
func (p *Project) AutoArrange(plateIdx int) error {
	plate := &p.Plates[plateIdx]
	if len(plate.Instances) == 0 {
		return nil
	}
	printer, err := plate.Printer.Resolve()
	if err != nil {
		return err
	}
	// Compute the union AABB of the source meshes (untransformed).
	var bounds mesh.AABB
	first := true
	for _, inst := range plate.Instances {
		b := p.Objects[inst.ObjectIndex].Mesh.Bounds
		if b.Empty() {
			continue
		}
		if first {
			bounds = b
			first = false
			continue
		}
		for ax := 0; ax < 3; ax++ {
			if b.Min[ax] < bounds.Min[ax] {
				bounds.Min[ax] = b.Min[ax]
			}
			if b.Max[ax] > bounds.Max[ax] {
				bounds.Max[ax] = b.Max[ax]
			}
		}
	}
	if first {
		return nil
	}
	cx := (bounds.Min[0] + bounds.Max[0]) * 0.5
	cy := (bounds.Min[1] + bounds.Max[1]) * 0.5
	tx := float32(printer.BedSizeX*0.5) - cx
	ty := float32(printer.BedSizeY*0.5) - cy
	tz := -bounds.Min[2]
	t := mesh.Vec3{tx, ty, tz}
	for i := range plate.Instances {
		plate.Instances[i].Transform.Translate = t
	}
	return nil
}

// PlateMesh returns the union of every instance's transformed mesh on the
// given plate. The slicer wants one flat triangle list per plate, with
// the plate sitting on Z=0 and instance translations already baked in.
func (p *Project) PlateMesh(plateIdx int) mesh.Mesh {
	plate := &p.Plates[plateIdx]
	var out mesh.Mesh
	for _, inst := range plate.Instances {
		obj := &p.Objects[inst.ObjectIndex]
		t := inst.Transform.Translate
		for _, tri := range obj.Mesh.Triangles {
			moved := mesh.Triangle{Normal: tri.Normal}
			for v := 0; v < 3; v++ {
				moved.Vertices[v] = mesh.Vec3{
					tri.Vertices[v][0] + t[0],
					tri.Vertices[v][1] + t[1],
					tri.Vertices[v][2] + t[2],
				}
			}
			out.Triangles = append(out.Triangles, moved)
			for v := 0; v < 3; v++ {
				out.Bounds.Extend(moved.Vertices[v])
			}
		}
	}
	return out
}
