package project

import (
	"encoding/json"

	"github.com/lestrrat-3d/units"

	"github.com/lestrrat-3d/osafune/internal/config"
)

// The version-1 embedded settings, kept exactly as they were written: every
// dimensional field a bare float64 or int, with the unit living only in a
// comment on the struct it came from. Version 2 replaced those with typed
// quantities, and these types exist only so a project saved before that change
// still opens with the profiles its author chose.
//
// They are frozen. A new setting belongs on the current [projectMeta] and must
// never be added here, because nothing ever wrote it into a version-1 file.
type (
	v1Printer struct {
		Name                                       string
		GcodeFlavor                                config.GcodeFlavor
		BedSizeX, BedSizeY, BedSizeZ               float64 // mm
		NozzleDiameter, FilamentDiameter           float64 // mm
		MaxAccelX, MaxAccelY, MaxAccelZ, MaxAccelE float64 // mm/s^2
		MaxSpeedX, MaxSpeedY, MaxSpeedZ, MaxSpeedE float64 // mm/s
		StartGcode, EndGcode, LayerChangeGcode     string
	}

	v1Filament struct {
		Name, Material           string
		NozzleTemp, BedTemp      int     // °C
		FlowRatio                float64 //
		RetractLength            float64 // mm
		RetractSpeed             float64 // mm/s
		ZHop                     float64 // mm
		FanSpeed, BridgeFanSpeed int
		FilamentDensity          float64 // g/cm^3
	}

	v1Process struct {
		Name                                string
		LayerHeight, FirstLayerHeight       float64 // mm
		LineWidth, FirstLayerLineWidth      float64 // mm
		Perimeters, TopLayers, BottomLayers int
		InfillDensity                       float64
		InfillPattern                       config.InfillPattern
		SeamPosition                        config.SeamPosition
		SkirtLoops                          int
		SkirtDistance, BrimWidth            float64 // mm
		BridgeSpeed                         float64 // mm/s
		BridgeFlow                          float64
		SupportEnable                       bool
		SupportThreshold                    float64   // degrees
		SupportBranchDiameter               float64   // mm
		SupportSpeed                        float64   // mm/s
		TravelSpeed, PerimeterSpeed         float64   // mm/s
		ExternalPerimeterSpeed, InfillSpeed float64   // mm/s
		SolidInfillSpeed, FirstLayerSpeed   float64   // mm/s
		InfillAngles                        []float64 // degrees
	}

	v1Meta struct {
		Version  int
		Printer  v1Printer
		Filament v1Filament
		Process  v1Process
		Objects  []objectMeta
	}
)

// migrateV1 reads a version-1 payload and gives every number the unit its
// version-1 comment promised. The upgrade is total: a version-1 file has a
// value for every field, so nothing is left to a default.
func migrateV1(data []byte) (projectMeta, bool) {
	var old v1Meta
	if err := json.Unmarshal(data, &old); err != nil {
		return projectMeta{}, false
	}

	angles := make([]units.Value, 0, len(old.Process.InfillAngles))
	for _, a := range old.Process.InfillAngles {
		angles = append(angles, units.Degrees(a))
	}

	return projectMeta{
		Version: projectVersion,
		Printer: config.Printer{
			Name:             old.Printer.Name,
			GcodeFlavor:      old.Printer.GcodeFlavor,
			BedSizeX:         units.Millimeters(old.Printer.BedSizeX),
			BedSizeY:         units.Millimeters(old.Printer.BedSizeY),
			BedSizeZ:         units.Millimeters(old.Printer.BedSizeZ),
			NozzleDiameter:   units.Millimeters(old.Printer.NozzleDiameter),
			FilamentDiameter: units.Millimeters(old.Printer.FilamentDiameter),
			MaxAccelX:        units.MillimetersPerSecondSquared(old.Printer.MaxAccelX),
			MaxAccelY:        units.MillimetersPerSecondSquared(old.Printer.MaxAccelY),
			MaxAccelZ:        units.MillimetersPerSecondSquared(old.Printer.MaxAccelZ),
			MaxAccelE:        units.MillimetersPerSecondSquared(old.Printer.MaxAccelE),
			MaxSpeedX:        units.MillimetersPerSecond(old.Printer.MaxSpeedX),
			MaxSpeedY:        units.MillimetersPerSecond(old.Printer.MaxSpeedY),
			MaxSpeedZ:        units.MillimetersPerSecond(old.Printer.MaxSpeedZ),
			MaxSpeedE:        units.MillimetersPerSecond(old.Printer.MaxSpeedE),
			StartGcode:       old.Printer.StartGcode,
			EndGcode:         old.Printer.EndGcode,
			LayerChangeGcode: old.Printer.LayerChangeGcode,
		},
		Filament: config.Filament{
			Name:            old.Filament.Name,
			Material:        old.Filament.Material,
			NozzleTemp:      units.DegreesCelsius(float64(old.Filament.NozzleTemp)),
			BedTemp:         units.DegreesCelsius(float64(old.Filament.BedTemp)),
			FlowRatio:       old.Filament.FlowRatio,
			RetractLength:   units.Millimeters(old.Filament.RetractLength),
			RetractSpeed:    units.MillimetersPerSecond(old.Filament.RetractSpeed),
			ZHop:            units.Millimeters(old.Filament.ZHop),
			FanSpeed:        old.Filament.FanSpeed,
			BridgeFanSpeed:  old.Filament.BridgeFanSpeed,
			FilamentDensity: units.GramsPerCubicCentimeter(old.Filament.FilamentDensity),
		},
		Process: config.Process{
			Name:                   old.Process.Name,
			LayerHeight:            units.Millimeters(old.Process.LayerHeight),
			FirstLayerHeight:       units.Millimeters(old.Process.FirstLayerHeight),
			LineWidth:              units.Millimeters(old.Process.LineWidth),
			FirstLayerLineWidth:    units.Millimeters(old.Process.FirstLayerLineWidth),
			Perimeters:             old.Process.Perimeters,
			TopLayers:              old.Process.TopLayers,
			BottomLayers:           old.Process.BottomLayers,
			InfillDensity:          old.Process.InfillDensity,
			InfillPattern:          old.Process.InfillPattern,
			SeamPosition:           old.Process.SeamPosition,
			SkirtLoops:             old.Process.SkirtLoops,
			SkirtDistance:          units.Millimeters(old.Process.SkirtDistance),
			BrimWidth:              units.Millimeters(old.Process.BrimWidth),
			BridgeSpeed:            units.MillimetersPerSecond(old.Process.BridgeSpeed),
			BridgeFlow:             old.Process.BridgeFlow,
			SupportEnable:          old.Process.SupportEnable,
			SupportThreshold:       units.Degrees(old.Process.SupportThreshold),
			SupportBranchDiameter:  units.Millimeters(old.Process.SupportBranchDiameter),
			SupportSpeed:           units.MillimetersPerSecond(old.Process.SupportSpeed),
			TravelSpeed:            units.MillimetersPerSecond(old.Process.TravelSpeed),
			PerimeterSpeed:         units.MillimetersPerSecond(old.Process.PerimeterSpeed),
			ExternalPerimeterSpeed: units.MillimetersPerSecond(old.Process.ExternalPerimeterSpeed),
			InfillSpeed:            units.MillimetersPerSecond(old.Process.InfillSpeed),
			SolidInfillSpeed:       units.MillimetersPerSecond(old.Process.SolidInfillSpeed),
			FirstLayerSpeed:        units.MillimetersPerSecond(old.Process.FirstLayerSpeed),
			InfillAngles:           angles,
		},
		Objects: old.Objects,
	}, true
}
