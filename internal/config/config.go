// Package config holds the three OrcaSlicer-style profile families that
// together fully describe a slicing job: the physical machine ([Printer]),
// the material loaded in it ([Filament]) and the process knobs that decide
// how thick/dense/fast the print will be ([Process]).
//
// The MVP keeps every profile as a plain Go struct with hardcoded sensible
// defaults; on-disk JSON profile bundles compatible with OrcaSlicer's
// schema are an explicit non-goal for this milestone. Code wanting to
// override a value either constructs the struct literally or applies a
// CLI/flag override after calling one of the DefaultXxx constructors.
package config

// InfillPattern selects the geometric pattern the slicer uses to fill
// the interior of each layer. The MVP supports four; OrcaSlicer's full
// catalogue (gyroid, honeycomb, cubic, etc.) is intentionally left for
// later because each adds a non-trivial chunk of geometry work.
type InfillPattern string

const (
	// InfillRectilinear is parallel lines that alternate angle every
	// layer (45° / -45°). Cheap to compute, prints fast, weak in shear.
	InfillRectilinear InfillPattern = "rectilinear"
	// InfillGrid lays two perpendicular sets of lines per layer at the
	// process angle and angle+90°. Visually denser than rectilinear for
	// the same density, stiffer, but with more retraction-free travels
	// at the crossings.
	InfillGrid InfillPattern = "grid"
	// InfillTriangles lays three line sets at 0°, 60° and 120°. Best
	// isotropic stiffness of the line-based patterns; print time goes up
	// with the extra direction.
	InfillTriangles InfillPattern = "triangles"
	// InfillConcentric repeats the perimeter offsetter inward at the
	// infill spacing. Looks like nested rings of the contour and prints
	// very clean for round parts; poor at filling narrow rectangles.
	InfillConcentric InfillPattern = "concentric"
)

// SeamPosition selects where the visible start/stop "seam" of a closed
// perimeter loop is placed. OrcaSlicer offers more (nearest, back, sharpest
// corner); the MVP supports the two that cover most needs.
type SeamPosition string

const (
	// SeamAligned places every layer's seam at the same reference corner
	// (the rear-most vertex) so the seams stack into one tidy vertical line
	// — the OrcaSlicer/PrusaSlicer default look.
	SeamAligned SeamPosition = "aligned"
	// SeamRandom scatters the seam to a different vertex each layer, hiding
	// it on organic shapes at the cost of a faint speckle instead of a line.
	SeamRandom SeamPosition = "random"
)

// GcodeFlavor selects which dialect of gcode the emitter produces. OrcaSlicer
// supports several; the MVP emitter is Marlin-with-OrcaSlicer-comment-headers
// since every Bambu/Voron/Prusa/generic-Marlin printer accepts that subset,
// and Bambu-specific macros can be layered on top later by editing the
// Printer's start/end gcode templates.
type GcodeFlavor string

const (
	FlavorMarlin    GcodeFlavor = "marlin"
	FlavorKlipper   GcodeFlavor = "klipper"
	FlavorBambuLab  GcodeFlavor = "bambulab"
)

// Printer describes the physical machine. Build volume, nozzle, kinematic
// limits and the start/end gcode templates that bracket every print all
// live here. Fields use millimetres unless documented otherwise.
type Printer struct {
	Name             string
	GcodeFlavor      GcodeFlavor
	BedSizeX         float64 // mm, usable build width
	BedSizeY         float64 // mm, usable build depth
	BedSizeZ         float64 // mm, usable build height
	NozzleDiameter   float64 // mm, e.g. 0.4
	FilamentDiameter float64 // mm, e.g. 1.75
	// Acceleration / speed limits emitted as M201/M203 at the top of the
	// file. Zero means "do not emit a limit for this axis".
	MaxAccelX, MaxAccelY, MaxAccelZ, MaxAccelE float64 // mm/s^2
	MaxSpeedX, MaxSpeedY, MaxSpeedZ, MaxSpeedE float64 // mm/s
	StartGcode                                 string
	EndGcode                                   string
	LayerChangeGcode                           string
}

// DefaultPrinter returns a Bambu-P1S-shaped 256x256x256 generic-Marlin
// printer. The start/end gcode is conservative — heat, home, purge line,
// then a clean shutdown — and works on any Marlin-or-Klipper machine.
func DefaultPrinter() Printer {
	return Printer{
		Name:             "Generic 256mm Bed",
		GcodeFlavor:      FlavorMarlin,
		BedSizeX:         256,
		BedSizeY:         256,
		BedSizeZ:         256,
		NozzleDiameter:   0.4,
		FilamentDiameter: 1.75,
		MaxAccelX:        10000,
		MaxAccelY:        10000,
		MaxAccelZ:        500,
		MaxAccelE:        5000,
		MaxSpeedX:        500,
		MaxSpeedY:        500,
		MaxSpeedZ:        20,
		MaxSpeedE:        25,
		StartGcode: `; --- makislicer start gcode ---
M140 S[bed_temperature]      ; set bed
M104 S[nozzle_temperature]   ; set hotend
G28                          ; home all axes
M190 S[bed_temperature]      ; wait for bed
M109 S[nozzle_temperature]   ; wait for hotend
G92 E0                       ; reset extruder
G1 Z2.0 F3000                ; lift
G1 X5 Y5 Z0.3 F5000          ; move to purge start
G1 X100 Y5 Z0.3 F1500 E15    ; purge line
G92 E0
; --- end start gcode ---
`,
		EndGcode: `; --- makislicer end gcode ---
M104 S0                      ; turn off hotend
M140 S0                      ; turn off bed
G91                          ; relative
G1 E-2 F2700                 ; retract
G1 Z10 F600                  ; lift
G90                          ; absolute
G28 X Y                      ; park
M84                          ; disable motors
; --- end end gcode ---
`,
	}
}

// Filament describes the material loaded in the printer. Temperatures and
// retraction live with the material because they are properties of the
// material, not of the machine — swapping PLA for PETG changes them all
// without touching the [Printer] profile.
type Filament struct {
	Name           string
	Material       string  // free-form: "PLA", "PETG", "ABS"
	NozzleTemp     int     // °C
	BedTemp        int     // °C
	FlowRatio      float64 // 1.0 = nominal
	RetractLength  float64 // mm
	RetractSpeed   float64 // mm/s
	ZHop           float64 // mm, nozzle lift during a retracted travel; 0 disables
	FanSpeed       int     // 0-255, part cooling fan speed after first few layers
	BridgeFanSpeed int     // 0-255, part cooling fan while printing bridges (usually max)
	FilamentDensity float64 // g/cm^3, used for weight estimates in header
}

// DefaultFilament returns a generic PLA profile.
func DefaultFilament() Filament {
	return Filament{
		Name:            "Generic PLA",
		Material:        "PLA",
		NozzleTemp:      210,
		BedTemp:         60,
		FlowRatio:       1.0,
		RetractLength:   0.8,
		RetractSpeed:    35,
		ZHop:            0.4,
		FanSpeed:        255,
		BridgeFanSpeed:  255,
		FilamentDensity: 1.24,
	}
}

// Process describes how thick, dense and fast the print should be. These
// are the knobs an end user tweaks most often; printer and filament tend
// to stay fixed for a given build.
type Process struct {
	Name string

	LayerHeight      float64 // mm
	FirstLayerHeight float64 // mm

	LineWidth           float64 // mm, extruded line width for perimeters/infill
	FirstLayerLineWidth float64 // mm

	Perimeters   int // wall count, >= 1
	TopLayers    int // solid top layer count
	BottomLayers int // solid bottom layer count

	// InfillDensity is the fraction of interior to fill, 0..1. The MVP
	// renders that as line spacing = LineWidth / InfillDensity for the
	// rectilinear pattern; multi-direction patterns (grid, triangles)
	// adjust the spacing by the number of directions so that volumetric
	// density still matches the requested fraction.
	InfillDensity float64

	// InfillPattern selects the fill geometry. See [InfillPattern].
	InfillPattern InfillPattern

	// SeamPosition selects where closed-loop seams are placed. See
	// [SeamPosition]. Empty is treated as [SeamAligned].
	SeamPosition SeamPosition

	// SkirtLoops is the number of free-standing loops traced around the
	// whole print on the first layer to prime the nozzle; 0 disables.
	SkirtLoops int
	// SkirtDistance is the gap (mm) between the object (or brim, if any) and
	// the innermost skirt loop.
	SkirtDistance float64
	// BrimWidth is how far (mm) the brim extends outward from the object's
	// first-layer wall for bed adhesion; 0 disables. Rounded to a whole
	// number of line-width loops.
	BrimWidth float64

	// BridgeSpeed is the print speed (mm/s) for unsupported bridge surfaces,
	// kept low so the spanning strands have time to cool taut.
	BridgeSpeed float64
	// BridgeFlow scales the extrusion of bridge strands (1.0 = nominal). Some
	// profiles reduce it slightly so the strand stretches without sagging.
	BridgeFlow float64

	// Speeds, all mm/s.
	TravelSpeed            float64
	PerimeterSpeed         float64
	ExternalPerimeterSpeed float64
	InfillSpeed            float64
	SolidInfillSpeed       float64
	FirstLayerSpeed        float64

	// InfillAngles cycles through these angles for successive layers. Two
	// alternating angles is the OrcaSlicer/PrusaSlicer default; the MVP
	// keeps it that simple.
	InfillAngles []float64 // degrees
}

// DefaultProcess returns a 0.2mm "Standard" profile aimed at a 0.4 nozzle.
// Speeds are conservative defaults that work on most bed-slingers; tune up
// for CoreXY machines.
func DefaultProcess() Process {
	return Process{
		Name:                   "Standard 0.20",
		LayerHeight:            0.2,
		FirstLayerHeight:       0.2,
		LineWidth:              0.42,
		FirstLayerLineWidth:    0.5,
		Perimeters:             2,
		TopLayers:              4,
		BottomLayers:           3,
		InfillDensity:          0.15,
		InfillPattern:          InfillGrid,
		SeamPosition:           SeamAligned,
		SkirtLoops:             1,
		SkirtDistance:          2.0,
		BrimWidth:              0,
		BridgeSpeed:            25,
		BridgeFlow:             1.0,
		TravelSpeed:            200,
		PerimeterSpeed:         60,
		ExternalPerimeterSpeed: 40,
		InfillSpeed:            80,
		SolidInfillSpeed:       50,
		FirstLayerSpeed:        25,
		InfillAngles:           []float64{45, -45},
	}
}
