package mesh_test

import (
	"path/filepath"
	"testing"

	tmf "github.com/lestrrat-go/3mf"
	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/osafune/internal/mesh"
)

// write3MF builds a single-triangle 3MF declaring unit u and saves it under
// t.TempDir(). The triangle spans one unit along X and Y, so the loaded
// bounds read back as the number of millimetres in one of u.
func write3MF(t *testing.T, u tmf.Unit) string {
	t.Helper()

	m := tmf.NewMesh(
		tmf.WithVertices([]tmf.Vertex{
			{X: 0, Y: 0, Z: 0},
			{X: 1, Y: 0, Z: 0},
			{X: 0, Y: 1, Z: 0},
		}),
		tmf.WithTriangles([]tmf.Triangle{{V1: 0, V2: 1, V3: 2}}),
	)
	obj := tmf.NewObject(
		tmf.WithObjectID(1),
		tmf.WithObjectType(tmf.ObjectTypeModel),
		tmf.WithObjectName("unit probe"),
		tmf.WithMesh(m),
	)

	opts := []tmf.Option{tmf.WithUnit(u), tmf.WithBuildItem(tmf.NewBuildItem(tmf.WithObjectRef(obj)))}
	opts = append(opts, tmf.WithObjects(obj)...)

	path := filepath.Join(t.TempDir(), "probe.3mf")
	pkg := tmf.NewPackage(tmf.WithModel(tmf.NewModel(opts...)))
	require.NoError(t, pkg.Save(path))
	return path
}

func TestLoad3MFModelUnit(t *testing.T) {
	t.Parallel()

	// The unit is declared on the <model> element, so a file authored in
	// inches or metres carries coordinates that are not millimetres. Every
	// one of these must arrive on the bed at its real size.
	for _, tc := range []struct {
		name string
		unit tmf.Unit
		want float32 // millimetres spanned by the one-unit triangle
	}{
		{name: "micron", unit: tmf.UnitMicron, want: 0.001},
		{name: "millimeter", unit: tmf.UnitMillimeter, want: 1},
		{name: "centimeter", unit: tmf.UnitCentimeter, want: 10},
		{name: "inch", unit: tmf.UnitInch, want: 25.4},
		{name: "foot", unit: tmf.UnitFoot, want: 304.8},
		{name: "meter", unit: tmf.UnitMeter, want: 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			scene, err := mesh.LoadFile(write3MF(t, tc.unit))
			require.NoError(t, err)
			require.Len(t, scene.Objects, 1)

			b := scene.Objects[0].Mesh.Bounds
			require.InDelta(t, tc.want, b.Max[0]-b.Min[0], float64(tc.want)*1e-5)
			require.InDelta(t, tc.want, b.Max[1]-b.Min[1], float64(tc.want)*1e-5)
		})
	}
}
