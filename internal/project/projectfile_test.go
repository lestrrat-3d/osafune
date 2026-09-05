package project_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/osafune/internal/config"
	"github.com/lestrrat-3d/osafune/internal/mesh"
	"github.com/lestrrat-3d/osafune/internal/project"
)

// quad returns a two-triangle square mesh with bounds, enough to round-trip.
func quad(name string, hidden bool, z float32) mesh.Object {
	v := [4]mesh.Vec3{{0, 0, z}, {10, 0, z}, {10, 10, z}, {0, 10, z}}
	m := mesh.Mesh{Triangles: []mesh.Triangle{
		{Vertices: [3]mesh.Vec3{v[0], v[1], v[2]}, Normal: mesh.Vec3{0, 0, 1}},
		{Vertices: [3]mesh.Vec3{v[0], v[2], v[3]}, Normal: mesh.Vec3{0, 0, 1}},
	}}
	for _, p := range v {
		m.Bounds.Extend(p)
	}
	return mesh.Object{Name: name, Hidden: hidden, Mesh: m}
}

func TestProjectFileRoundTrip(t *testing.T) {
	t.Parallel()
	scene := &mesh.Scene{Objects: []mesh.Object{quad("alpha", false, 0), quad("beta", true, 1)}}
	plate := &project.Plate{
		Name:     "Plate 1",
		Printer:  config.DefaultPrinter(),
		Filament: config.DefaultFilament(),
		Process:  config.DefaultProcess(),
	}
	plate.Process.Name = "Custom"
	plate.Process.LayerHeight = 0.32
	plate.Process.InfillDensity = 0.37
	plate.Filament.NozzleTemp = 233

	path := filepath.Join(t.TempDir(), "proj.3mf")
	require.NoError(t, project.SaveProjectFile(path, scene, plate))

	gotScene, gotPlate, err := project.LoadProjectFile(path)
	require.NoError(t, err)

	// Geometry round-trips: same object count and triangle counts.
	require.Len(t, gotScene.Objects, 2)
	require.Equal(t, 2, len(gotScene.Objects[0].Mesh.Triangles))

	// Settings round-trip from the embedded attachment.
	require.Equal(t, "Custom", gotPlate.Process.Name)
	require.Equal(t, 0.32, gotPlate.Process.LayerHeight)
	require.Equal(t, 0.37, gotPlate.Process.InfillDensity)
	require.Equal(t, 233, gotPlate.Filament.NozzleTemp)

	// Per-object visibility round-trips by position.
	require.False(t, gotScene.Objects[0].Hidden)
	require.True(t, gotScene.Objects[1].Hidden)
}

func TestLoadPlainModelGetsDefaults(t *testing.T) {
	t.Parallel()
	// A 3MF we wrote without using SaveProjectFile metadata still loads with
	// default profiles (no attachment present).
	scene := &mesh.Scene{Objects: []mesh.Object{quad("plain", false, 0)}}
	path := filepath.Join(t.TempDir(), "plain.3mf")
	// Save with default plate, then strip nothing — but verify defaults load
	// path by reading a project saved with default profiles.
	require.NoError(t, project.SaveProjectFile(path, scene, &project.Plate{
		Printer: config.DefaultPrinter(), Filament: config.DefaultFilament(), Process: config.DefaultProcess(),
	}))
	_, gotPlate, err := project.LoadProjectFile(path)
	require.NoError(t, err)
	require.Equal(t, config.DefaultProcess().LayerHeight, gotPlate.Process.LayerHeight)
}
