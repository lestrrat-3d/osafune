package project

import (
	"encoding/json"

	tmf "github.com/lestrrat-go/3mf"

	"github.com/lestrrat-go/osafune/internal/config"
	"github.com/lestrrat-go/osafune/internal/mesh"
)

// projectMetaPath is the in-package part that carries our settings alongside
// the standard 3MF geometry. A plain 3MF reader ignores it; we read it back.
const projectMetaPath = "/Metadata/osafune.json"

// projectVersion tags the embedded metadata so a future format change can be
// detected on load.
const projectVersion = 1

type objectMeta struct {
	Name   string
	Hidden bool
}

// projectMeta is the JSON payload embedded in a saved project: the plate's
// three profiles plus per-object bookkeeping. Geometry lives in the standard
// 3MF model, not here.
type projectMeta struct {
	Version  int
	Printer  config.Printer
	Filament config.Filament
	Process  config.Process
	Objects  []objectMeta
}

// SaveProjectFile writes the scene geometry and the plate's settings to a 3MF
// project at path. The geometry is a standard 3MF model (so it opens in any
// 3MF tool); the settings and object visibility ride along as a JSON
// attachment that [LoadProjectFile] reads back. Viewport transforms are
// already baked into the scene vertices, so the saved geometry is exactly
// what would be sliced.
func SaveProjectFile(path string, scene *mesh.Scene, plate *Plate) error {
	meta := projectMeta{
		Version:  projectVersion,
		Printer:  plate.Printer,
		Filament: plate.Filament,
		Process:  plate.Process,
	}

	var opts []tmf.Option
	var objs []*tmf.Object
	for oi := range scene.Objects {
		o := &scene.Objects[oi]
		meta.Objects = append(meta.Objects, objectMeta{Name: o.Name, Hidden: o.Hidden})
		obj := tmf.NewObject(
			tmf.WithObjectID(uint32(oi+1)),
			tmf.WithObjectType(tmf.ObjectTypeModel),
			tmf.WithObjectName(o.Name),
			tmf.WithMesh(meshToTMF(&o.Mesh)),
		)
		objs = append(objs, obj)
		opts = append(opts, tmf.WithBuildItem(tmf.NewBuildItem(tmf.WithObjectRef(obj))))
	}
	opts = append(opts, tmf.WithObjects(objs...)...)

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	pkg := tmf.NewPackage(tmf.WithModel(tmf.NewModel(opts...)))
	pkg.AddAttachment(tmf.Attachment{Path: projectMetaPath, ContentType: "application/json", Data: data})
	return pkg.Save(path)
}

// LoadProjectFile reads a 3MF at path into a scene and a single-plate
// configuration. Geometry is loaded with the standard mesh loader; our
// embedded settings are restored when present, and a plain (non-project) 3MF
// just comes back with default profiles.
func LoadProjectFile(path string) (*mesh.Scene, *Plate, error) {
	scene, err := mesh.LoadFile(path)
	if err != nil {
		return nil, nil, err
	}
	plate := defaultPlate()

	if pkg, err := tmf.Open(path); err == nil {
		if data := pkg.Part(projectMetaPath); len(data) > 0 {
			var meta projectMeta
			if json.Unmarshal(data, &meta) == nil {
				plate.Printer = meta.Printer
				plate.Filament = meta.Filament
				plate.Process = meta.Process
				applyObjectMeta(scene, meta.Objects)
			}
		}
	}
	return scene, plate, nil
}

func defaultPlate() *Plate {
	return &Plate{
		Name:     "Plate 1",
		Printer:  config.DefaultPrinter(),
		Filament: config.DefaultFilament(),
		Process:  config.DefaultProcess(),
	}
}

// applyObjectMeta restores per-object visibility by position; the loader keeps
// build-item order, which matches the order objects were written in.
func applyObjectMeta(scene *mesh.Scene, metas []objectMeta) {
	for i := range scene.Objects {
		if i < len(metas) {
			scene.Objects[i].Hidden = metas[i].Hidden
		}
	}
}

// meshToTMF converts a mesh to a 3MF mesh, de-duplicating exactly-equal
// vertices so the triangles index a compact shared vertex list.
func meshToTMF(m *mesh.Mesh) *tmf.Mesh {
	index := make(map[[3]float32]uint32, len(m.Triangles)*3)
	var verts []tmf.Vertex
	vid := func(v mesh.Vec3) uint32 {
		k := [3]float32{v[0], v[1], v[2]}
		if i, ok := index[k]; ok {
			return i
		}
		i := uint32(len(verts))
		verts = append(verts, tmf.Vertex{X: float64(v[0]), Y: float64(v[1]), Z: float64(v[2])})
		index[k] = i
		return i
	}
	tris := make([]tmf.Triangle, 0, len(m.Triangles))
	for _, t := range m.Triangles {
		tris = append(tris, tmf.Triangle{
			V1: vid(t.Vertices[0]),
			V2: vid(t.Vertices[1]),
			V3: vid(t.Vertices[2]),
		})
	}
	return tmf.NewMesh(tmf.WithVertices(verts), tmf.WithTriangles(tris))
}
