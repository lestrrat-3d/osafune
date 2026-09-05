package project_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	tmf "github.com/lestrrat-go/3mf"
	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/units"

	"github.com/lestrrat-3d/osafune/internal/project"
)

// v1Payload is the embedded settings exactly as osafune wrote them before
// quantities were typed: bare numbers, and a Version of 1. It is written by
// hand rather than by the current encoder, because the whole point is that
// nothing writes this shape any more.
const v1Payload = `{
  "Version": 1,
  "Printer": {
    "Name": "Old Printer",
    "GcodeFlavor": "marlin",
    "BedSizeX": 220, "BedSizeY": 220, "BedSizeZ": 250,
    "NozzleDiameter": 0.6, "FilamentDiameter": 1.75,
    "MaxAccelX": 9000, "MaxAccelY": 9000, "MaxAccelZ": 400, "MaxAccelE": 4000,
    "MaxSpeedX": 400, "MaxSpeedY": 400, "MaxSpeedZ": 15, "MaxSpeedE": 20,
    "StartGcode": "; start\n", "EndGcode": "; end\n", "LayerChangeGcode": ""
  },
  "Filament": {
    "Name": "Old PETG", "Material": "PETG",
    "NozzleTemp": 240, "BedTemp": 80,
    "FlowRatio": 0.98,
    "RetractLength": 1.2, "RetractSpeed": 40, "ZHop": 0.25,
    "FanSpeed": 128, "BridgeFanSpeed": 255,
    "FilamentDensity": 1.27
  },
  "Process": {
    "Name": "Old Fine",
    "LayerHeight": 0.12, "FirstLayerHeight": 0.24,
    "LineWidth": 0.45, "FirstLayerLineWidth": 0.55,
    "Perimeters": 3, "TopLayers": 5, "BottomLayers": 4,
    "InfillDensity": 0.25,
    "InfillPattern": "triangles", "SeamPosition": "random",
    "SkirtLoops": 2, "SkirtDistance": 3, "BrimWidth": 5,
    "BridgeSpeed": 20, "BridgeFlow": 0.95,
    "SupportEnable": true, "SupportThreshold": 55,
    "SupportBranchDiameter": 2.5, "SupportSpeed": 35,
    "TravelSpeed": 180, "PerimeterSpeed": 50,
    "ExternalPerimeterSpeed": 30, "InfillSpeed": 70,
    "SolidInfillSpeed": 45, "FirstLayerSpeed": 20,
    "InfillAngles": [30, -60]
  },
  "Objects": [{"Name": "alpha", "Hidden": true}]
}`

// writeV1Project builds a 3MF carrying the version-1 payload, which is what a
// project saved by an older osafune looks like on disk.
func writeV1Project(t *testing.T, payload string) string {
	t.Helper()

	m := tmf.NewMesh(
		tmf.WithVertices([]tmf.Vertex{{X: 0}, {X: 10}, {Y: 10}}),
		tmf.WithTriangles([]tmf.Triangle{{V1: 0, V2: 1, V3: 2}}),
	)
	obj := tmf.NewObject(
		tmf.WithObjectID(1),
		tmf.WithObjectType(tmf.ObjectTypeModel),
		tmf.WithObjectName("alpha"),
		tmf.WithMesh(m),
	)
	opts := []tmf.Option{tmf.WithBuildItem(tmf.NewBuildItem(tmf.WithObjectRef(obj)))}
	opts = append(opts, tmf.WithObjects(obj)...)

	pkg := tmf.NewPackage(tmf.WithModel(tmf.NewModel(opts...)))
	pkg.AddAttachment(tmf.Attachment{
		Path:        "/Metadata/osafune.json",
		ContentType: "application/json",
		Data:        []byte(payload),
	})

	path := filepath.Join(t.TempDir(), "v1.3mf")
	require.NoError(t, pkg.Save(path))
	return path
}

func TestLoadV1ProjectKeepsItsProfiles(t *testing.T) {
	t.Parallel()

	// A project saved before quantities were typed must open with the profiles
	// its author chose, not silently revert to the defaults.
	_, plate, err := project.LoadProjectFile(writeV1Project(t, v1Payload))
	require.NoError(t, err)

	require.Equal(t, "Old Printer", plate.Printer.Name)
	require.Equal(t, "Old PETG", plate.Filament.Name)
	require.Equal(t, "Old Fine", plate.Process.Name)

	// Every bare number gained the unit its old comment promised.
	require.Equal(t, units.Millimeters(220), plate.Printer.BedSizeX)
	require.Equal(t, units.Millimeters(0.6), plate.Printer.NozzleDiameter)
	require.Equal(t, units.MillimetersPerSecondSquared(9000), plate.Printer.MaxAccelX)
	require.Equal(t, units.MillimetersPerSecond(400), plate.Printer.MaxSpeedX)
	require.Equal(t, units.DegreesCelsius(240), plate.Filament.NozzleTemp)
	require.Equal(t, units.DegreesCelsius(80), plate.Filament.BedTemp)
	require.Equal(t, units.GramsPerCubicCentimeter(1.27), plate.Filament.FilamentDensity)
	require.Equal(t, units.Millimeters(0.12), plate.Process.LayerHeight)
	require.Equal(t, units.Degrees(55), plate.Process.SupportThreshold)
	require.Equal(t, []units.Value{units.Degrees(30), units.Degrees(-60)}, plate.Process.InfillAngles)

	// The untyped settings come across untouched.
	require.Equal(t, 3, plate.Process.Perimeters)
	require.Equal(t, 0.25, plate.Process.InfillDensity)
	require.True(t, plate.Process.SupportEnable)
	require.Equal(t, 128, plate.Filament.FanSpeed)
}

func TestMigratedV1ProjectResolves(t *testing.T) {
	t.Parallel()

	// The migration has to produce profiles the pipeline can actually use: a
	// field left as an untyped zero Value would resolve to an error here.
	_, plate, err := project.LoadProjectFile(writeV1Project(t, v1Payload))
	require.NoError(t, err)

	resolved, err := plate.Resolve()
	require.NoError(t, err)
	require.Equal(t, 220.0, resolved.Printer.BedSizeX)
	require.Equal(t, 240, resolved.Filament.NozzleTemp)
	require.Equal(t, 0.12, resolved.Process.LayerHeight)
	require.Equal(t, []float64{30, -60}, resolved.Process.InfillAngles)
}

func TestLoadUnreadableMetaFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	// A payload that is neither version 1 nor the current shape leaves the
	// default profiles in place rather than failing the whole open.
	_, plate, err := project.LoadProjectFile(writeV1Project(t, `{"Version": 99, "Printer": "nonsense"}`))
	require.NoError(t, err)

	var meta struct{ Version int }
	require.NoError(t, json.Unmarshal([]byte(v1Payload), &meta))
	require.Equal(t, 1, meta.Version)
	require.Equal(t, "Generic 256mm Bed", plate.Printer.Name)
}
