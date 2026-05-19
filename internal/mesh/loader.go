package mesh

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	tmf "github.com/lestrrat-go/3mf"
	"github.com/lestrrat-go/stl"
)

// LoadFile loads an STL or 3MF mesh from disk into a [Scene]. The format is
// chosen by the file extension; the viewer's file dialog filters the same
// way. STL files become a Scene with a single Object named after the file;
// 3MF files become a Scene with one Object per build item.
func LoadFile(path string) (*Scene, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".stl":
		return loadSTL(path)
	case ".3mf":
		return load3MF(path)
	default:
		return nil, fmt.Errorf("mesh: unsupported file extension %q", filepath.Ext(path))
	}
}

func loadSTL(path string) (*Scene, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := stl.NewReader(f)
	var m Mesh
	for {
		t, err := r.ReadTriangle()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("mesh: read stl: %w", err)
		}
		tri := Triangle{
			Vertices: [3]Vec3{
				{t.Vertices[0][0], t.Vertices[0][1], t.Vertices[0][2]},
				{t.Vertices[1][0], t.Vertices[1][1], t.Vertices[1][2]},
				{t.Vertices[2][0], t.Vertices[2][1], t.Vertices[2][2]},
			},
		}
		// STL stores a normal but many writers leave it zero. Recompute when
		// the stored normal is degenerate so lighting and back-face culling
		// remain well-defined.
		n := Vec3{t.Normal[0], t.Normal[1], t.Normal[2]}
		if n == (Vec3{}) {
			cn := t.ComputedNormal()
			n = Vec3{cn[0], cn[1], cn[2]}
		}
		tri.Normal = n
		for _, v := range tri.Vertices {
			m.Bounds.Extend(v)
		}
		m.Triangles = append(m.Triangles, tri)
	}
	if len(m.Triangles) == 0 {
		return nil, errors.New("mesh: stl file contained no triangles")
	}

	name := r.Name()
	if name == "" {
		// "solid <name>" was absent in the header; fall back to the file
		// stem so the user has something to identify the object by.
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return &Scene{Objects: []Object{{Name: name, Mesh: m}}}, nil
}

func load3MF(path string) (*Scene, error) {
	pkg, err := tmf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("mesh: open 3mf: %w", err)
	}
	model := pkg.Model()
	if model == nil {
		return nil, errors.New("mesh: 3mf package has no model")
	}
	res := model.Resources()
	build := model.Build()
	if res == nil || build == nil || len(build.Items) == 0 {
		return nil, errors.New("mesh: 3mf model has no build items")
	}

	scene := &Scene{}
	// visiting guards against component cycles; the 3MF spec forbids them but
	// nothing in the file format prevents a malformed model from including one.
	visiting := map[uint32]struct{}{}

	for i, item := range build.Items {
		obj := res.FindObject(item.ObjectID)
		if obj == nil {
			continue
		}
		var m Mesh
		if err := emitObject(&m, res, obj, item.Transform, visiting); err != nil {
			return nil, err
		}
		if len(m.Triangles) == 0 {
			continue
		}
		name := obj.Name()
		if name == "" {
			name = obj.PartNumber()
		}
		if name == "" {
			name = fmt.Sprintf("Object #%d", obj.ID())
		}
		// Disambiguate when the same Object is referenced by multiple build
		// items (e.g. an array of identical parts).
		if duplicateObjectName(scene, name) {
			name = fmt.Sprintf("%s (instance %d)", name, i+1)
		}
		scene.Objects = append(scene.Objects, Object{Name: name, Mesh: m})
	}

	if len(scene.Objects) == 0 {
		return nil, errors.New("mesh: 3mf produced no triangles (build items reference no geometry)")
	}
	return scene, nil
}

func duplicateObjectName(s *Scene, name string) bool {
	for i := range s.Objects {
		if s.Objects[i].Name == name {
			return true
		}
	}
	return false
}

// emitObject appends every triangle reachable from obj into m, applying the
// accumulated affine transform. Mixed objects (with both a mesh and
// components) are handled by emitting both.
func emitObject(m *Mesh, res *tmf.Resources, obj *tmf.Object, acc tmf.Matrix, visiting map[uint32]struct{}) error {
	if _, ok := visiting[obj.ID()]; ok {
		return fmt.Errorf("mesh: 3mf component cycle through object id %d", obj.ID())
	}
	visiting[obj.ID()] = struct{}{}
	defer delete(visiting, obj.ID())

	if mm := obj.Mesh(); mm != nil {
		verts := mm.Vertices()
		for _, tri := range mm.Triangles() {
			if int(tri.V1) >= len(verts) || int(tri.V2) >= len(verts) || int(tri.V3) >= len(verts) {
				return fmt.Errorf("mesh: 3mf triangle index out of range in object %d", obj.ID())
			}
			v0 := transformVertex(acc, verts[tri.V1])
			v1 := transformVertex(acc, verts[tri.V2])
			v2 := transformVertex(acc, verts[tri.V3])
			t := Triangle{Vertices: [3]Vec3{v0, v1, v2}}
			t.Normal = computeNormal(v0, v1, v2)
			m.Bounds.Extend(v0)
			m.Bounds.Extend(v1)
			m.Bounds.Extend(v2)
			m.Triangles = append(m.Triangles, t)
		}
	}

	for _, c := range obj.Components() {
		child := res.FindObject(c.ObjectID)
		if child == nil {
			continue
		}
		if err := emitObject(m, res, child, mulMatrix(c.Transform, acc), visiting); err != nil {
			return err
		}
	}
	return nil
}

// transformVertex applies the 3MF 4x3 affine transform to v. 3MF stores
// matrices in row-vector convention: v' = v * M.
func transformVertex(m tmf.Matrix, v tmf.Vertex) Vec3 {
	x := v.X*m[0] + v.Y*m[3] + v.Z*m[6] + m[9]
	y := v.X*m[1] + v.Y*m[4] + v.Z*m[7] + m[10]
	z := v.X*m[2] + v.Y*m[5] + v.Z*m[8] + m[11]
	return Vec3{float32(x), float32(y), float32(z)}
}

// mulMatrix returns a * b under the row-vector convention used by 3MF, so
// that v * (a*b) == (v*a) * b. a is applied first (the inner / child
// transform), b second (the outer / parent transform).
func mulMatrix(a, b tmf.Matrix) tmf.Matrix {
	var r tmf.Matrix
	for i := 0; i < 4; i++ {
		for j := 0; j < 3; j++ {
			v := a[i*3]*b[j] + a[i*3+1]*b[3+j] + a[i*3+2]*b[6+j]
			if i == 3 {
				v += b[9+j]
			}
			r[i*3+j] = v
		}
	}
	return r
}

func computeNormal(a, b, c Vec3) Vec3 {
	ux, uy, uz := b[0]-a[0], b[1]-a[1], b[2]-a[2]
	vx, vy, vz := c[0]-a[0], c[1]-a[1], c[2]-a[2]
	nx := uy*vz - uz*vy
	ny := uz*vx - ux*vz
	nz := ux*vy - uy*vx
	l := float32(math.Sqrt(float64(nx*nx + ny*ny + nz*nz)))
	if l == 0 {
		return Vec3{}
	}
	return Vec3{nx / l, ny / l, nz / l}
}
