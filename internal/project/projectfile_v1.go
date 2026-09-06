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
		Name             string             `json:"Name"`
		GcodeFlavor      config.GcodeFlavor `json:"GcodeFlavor"`
		BedSizeX         float64            `json:"BedSizeX"`
		BedSizeY         float64            `json:"BedSizeY"`
		BedSizeZ         float64            `json:"BedSizeZ"` // mm
		NozzleDiameter   float64            `json:"NozzleDiameter"`
		FilamentDiameter float64            `json:"FilamentDiameter"` // mm
		MaxAccelX        float64            `json:"MaxAccelX"`
		MaxAccelY        float64            `json:"MaxAccelY"`
		MaxAccelZ        float64            `json:"MaxAccelZ"`
		MaxAccelE        float64            `json:"MaxAccelE"` // mm/s^2
		MaxSpeedX        float64            `json:"MaxSpeedX"`
		MaxSpeedY        float64            `json:"MaxSpeedY"`
		MaxSpeedZ        float64            `json:"MaxSpeedZ"`
		MaxSpeedE        float64            `json:"MaxSpeedE"` // mm/s
		StartGcode       string             `json:"StartGcode"`
		EndGcode         string             `json:"EndGcode"`
		LayerChangeGcode string             `json:"LayerChangeGcode"`
	}

	v1Filament struct {
		Name            string  `json:"Name"`
		Material        string  `json:"Material"`
		NozzleTemp      int     `json:"NozzleTemp"`
		BedTemp         int     `json:"BedTemp"`       // °C
		FlowRatio       float64 `json:"FlowRatio"`     //
		RetractLength   float64 `json:"RetractLength"` // mm
		RetractSpeed    float64 `json:"RetractSpeed"`  // mm/s
		ZHop            float64 `json:"ZHop"`          // mm
		FanSpeed        int     `json:"FanSpeed"`
		BridgeFanSpeed  int     `json:"BridgeFanSpeed"`
		FilamentDensity float64 `json:"FilamentDensity"` // g/cm^3
	}

	v1Process struct {
		Name                   string               `json:"Name"`
		LayerHeight            float64              `json:"LayerHeight"`
		FirstLayerHeight       float64              `json:"FirstLayerHeight"` // mm
		LineWidth              float64              `json:"LineWidth"`
		FirstLayerLineWidth    float64              `json:"FirstLayerLineWidth"` // mm
		Perimeters             int                  `json:"Perimeters"`
		TopLayers              int                  `json:"TopLayers"`
		BottomLayers           int                  `json:"BottomLayers"`
		InfillDensity          float64              `json:"InfillDensity"`
		InfillPattern          config.InfillPattern `json:"InfillPattern"`
		SeamPosition           config.SeamPosition  `json:"SeamPosition"`
		SkirtLoops             int                  `json:"SkirtLoops"`
		SkirtDistance          float64              `json:"SkirtDistance"`
		BrimWidth              float64              `json:"BrimWidth"`   // mm
		BridgeSpeed            float64              `json:"BridgeSpeed"` // mm/s
		BridgeFlow             float64              `json:"BridgeFlow"`
		SupportEnable          bool                 `json:"SupportEnable"`
		SupportThreshold       float64              `json:"SupportThreshold"`      // degrees
		SupportBranchDiameter  float64              `json:"SupportBranchDiameter"` // mm
		SupportSpeed           float64              `json:"SupportSpeed"`          // mm/s
		TravelSpeed            float64              `json:"TravelSpeed"`
		PerimeterSpeed         float64              `json:"PerimeterSpeed"` // mm/s
		ExternalPerimeterSpeed float64              `json:"ExternalPerimeterSpeed"`
		InfillSpeed            float64              `json:"InfillSpeed"` // mm/s
		SolidInfillSpeed       float64              `json:"SolidInfillSpeed"`
		FirstLayerSpeed        float64              `json:"FirstLayerSpeed"` // mm/s
		InfillAngles           []float64            `json:"InfillAngles"`    // degrees
	}

	v1Meta struct {
		Version  int          `json:"Version"`
		Printer  v1Printer    `json:"Printer"`
		Filament v1Filament   `json:"Filament"`
		Process  v1Process    `json:"Process"`
		Objects  []objectMeta `json:"Objects"`
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
