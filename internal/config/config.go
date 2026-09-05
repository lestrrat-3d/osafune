// Package config holds the three OrcaSlicer-style profile families that
// together fully describe a slicing job: the physical machine ([Printer]),
// the material loaded in it ([Filament]) and the process knobs that decide
// how thick/dense/fast the print will be ([Process]).
//
// # Typed quantities in, plain numbers out
//
// Every dimensional field on a profile is a [units.Value] — a magnitude and
// the unit it is expressed in, together. A profile is what a user edits and
// what is written to disk, and a bare float64 there is how a bed size in
// inches or a feedrate in mm/min silently becomes millimetres: exactly the
// mistake this project already shipped once, in the 3MF loader.
//
// The slicing pipeline does not want that. It runs the same arithmetic
// millions of times per plate, and a fallible conversion in the middle of it
// buys nothing, because the only thing that can go wrong is a profile that was
// already wrong before slicing began. So each profile [Printer.Resolve],
// [Filament.Resolve] and [Process.Resolve] converts once into a Resolved form
// carrying plain float64 in fixed canonical units — millimetres, millimetres
// per second, millimetres per second squared, degrees Celsius, degrees, and
// grams per cubic centimetre. The units are checked at that one boundary, and
// everything downstream is ordinary arithmetic.
//
// The Resolved structs use the same field names as the profiles they come
// from, so a reader moving between the two is never guessing.
package config

import (
	"fmt"

	"github.com/lestrrat-3d/units"
)

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
	FlavorMarlin   GcodeFlavor = "marlin"
	FlavorKlipper  GcodeFlavor = "klipper"
	FlavorBambuLab GcodeFlavor = "bambulab"
)

// resolver converts profile fields and remembers the first failure, so a
// Resolve method reads as a list of fields rather than as a chain of error
// checks. Once it has an error every later conversion is skipped and returns
// zero, and the caller reports that first error.
//
// A field left unset is a dimensionless zero [units.Value], which is not a
// length or a speed, so it fails here rather than passing silently as zero of
// whatever the reader assumed. A field that really may be zero is still
// written as a quantity: units.Millimeters(0), not an empty Value.
type resolver struct{ err error }

// in converts v to u, naming the field in any error so a bad profile says
// which knob is wrong rather than only that something is.
func (r *resolver) in(field string, v units.Value, u units.Unit) float64 {
	if r.err != nil {
		return 0
	}
	x, err := v.In(u)
	if err != nil {
		r.err = fmt.Errorf("config: %s: %w", field, err)
		return 0
	}
	return x
}

// mm, mmPerSec and the helpers below name the canonical unit of each
// dimension once, so a Resolve method never repeats it.
func (r *resolver) mm(field string, v units.Value) float64 {
	return r.in(field, v, units.Millimeter)
}

func (r *resolver) mmPerSec(field string, v units.Value) float64 {
	return r.in(field, v, units.MillimeterPerSecond)
}

func (r *resolver) mmPerSecSq(field string, v units.Value) float64 {
	return r.in(field, v, units.MillimeterPerSecondSquared)
}

func (r *resolver) degrees(field string, v units.Value) float64 {
	return r.in(field, v, units.Degree)
}

// celsius rounds to the whole degree gcode carries: M104 and M140 take an
// integer, so a fractional setpoint has nowhere to go.
func (r *resolver) celsius(field string, v units.Value) int {
	x := r.in(field, v, units.Celsius)
	if x < 0 {
		return int(x - 0.5)
	}
	return int(x + 0.5)
}

// Printer describes the physical machine. Build volume, nozzle, kinematic
// limits and the start/end gcode templates that bracket every print all
// live here.
type Printer struct {
	Name             string
	GcodeFlavor      GcodeFlavor
	BedSizeX         units.Value // usable build width
	BedSizeY         units.Value // usable build depth
	BedSizeZ         units.Value // usable build height
	NozzleDiameter   units.Value // e.g. 0.4 mm
	FilamentDiameter units.Value // e.g. 1.75 mm
	// Acceleration / speed limits emitted as M201/M203 at the top of the
	// file. Zero means "do not emit a limit for this axis", and is written
	// as a zero quantity rather than an unset field.
	MaxAccelX, MaxAccelY, MaxAccelZ, MaxAccelE units.Value
	MaxSpeedX, MaxSpeedY, MaxSpeedZ, MaxSpeedE units.Value
	StartGcode                                 string
	EndGcode                                   string
	LayerChangeGcode                           string
}

// ResolvedPrinter is [Printer] with every quantity converted to the units the
// pipeline and the gcode emitter work in: millimetres for lengths, mm/s for
// speeds, mm/s² for accelerations.
type ResolvedPrinter struct {
	Name             string
	GcodeFlavor      GcodeFlavor
	BedSizeX         float64 // mm
	BedSizeY         float64 // mm
	BedSizeZ         float64 // mm
	NozzleDiameter   float64 // mm
	FilamentDiameter float64 // mm

	MaxAccelX, MaxAccelY, MaxAccelZ, MaxAccelE float64 // mm/s^2
	MaxSpeedX, MaxSpeedY, MaxSpeedZ, MaxSpeedE float64 // mm/s

	StartGcode       string
	EndGcode         string
	LayerChangeGcode string
}

// Resolve converts the profile to the pipeline's units, reporting the first
// field whose quantity is not of the kind that field measures.
func (p Printer) Resolve() (ResolvedPrinter, error) {
	var r resolver
	out := ResolvedPrinter{
		Name:             p.Name,
		GcodeFlavor:      p.GcodeFlavor,
		BedSizeX:         r.mm("BedSizeX", p.BedSizeX),
		BedSizeY:         r.mm("BedSizeY", p.BedSizeY),
		BedSizeZ:         r.mm("BedSizeZ", p.BedSizeZ),
		NozzleDiameter:   r.mm("NozzleDiameter", p.NozzleDiameter),
		FilamentDiameter: r.mm("FilamentDiameter", p.FilamentDiameter),
		MaxAccelX:        r.mmPerSecSq("MaxAccelX", p.MaxAccelX),
		MaxAccelY:        r.mmPerSecSq("MaxAccelY", p.MaxAccelY),
		MaxAccelZ:        r.mmPerSecSq("MaxAccelZ", p.MaxAccelZ),
		MaxAccelE:        r.mmPerSecSq("MaxAccelE", p.MaxAccelE),
		MaxSpeedX:        r.mmPerSec("MaxSpeedX", p.MaxSpeedX),
		MaxSpeedY:        r.mmPerSec("MaxSpeedY", p.MaxSpeedY),
		MaxSpeedZ:        r.mmPerSec("MaxSpeedZ", p.MaxSpeedZ),
		MaxSpeedE:        r.mmPerSec("MaxSpeedE", p.MaxSpeedE),
		StartGcode:       p.StartGcode,
		EndGcode:         p.EndGcode,
		LayerChangeGcode: p.LayerChangeGcode,
	}
	if r.err != nil {
		return ResolvedPrinter{}, r.err
	}
	return out, nil
}

// DefaultPrinter returns a Bambu-P1S-shaped 256x256x256 generic-Marlin
// printer. The start/end gcode is conservative — heat, home, purge line,
// then a clean shutdown — and works on any Marlin-or-Klipper machine.
func DefaultPrinter() Printer {
	return Printer{
		Name:             "Generic 256mm Bed",
		GcodeFlavor:      FlavorMarlin,
		BedSizeX:         units.Millimeters(256),
		BedSizeY:         units.Millimeters(256),
		BedSizeZ:         units.Millimeters(256),
		NozzleDiameter:   units.Millimeters(0.4),
		FilamentDiameter: units.Millimeters(1.75),
		MaxAccelX:        units.MillimetersPerSecondSquared(10000),
		MaxAccelY:        units.MillimetersPerSecondSquared(10000),
		MaxAccelZ:        units.MillimetersPerSecondSquared(500),
		MaxAccelE:        units.MillimetersPerSecondSquared(5000),
		MaxSpeedX:        units.MillimetersPerSecond(500),
		MaxSpeedY:        units.MillimetersPerSecond(500),
		MaxSpeedZ:        units.MillimetersPerSecond(20),
		MaxSpeedE:        units.MillimetersPerSecond(25),
		StartGcode: `; --- osafune start gcode ---
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
		EndGcode: `; --- osafune end gcode ---
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
	Name     string
	Material string // free-form: "PLA", "PETG", "ABS"
	// NozzleTemp and BedTemp are carried in [units.Celsius], which is an
	// affine unit: it converts, compares and persists, but does no
	// arithmetic. Nothing here needs any — a setpoint is set, stored and
	// emitted.
	NozzleTemp      units.Value
	BedTemp         units.Value
	FlowRatio       float64     // 1.0 = nominal
	RetractLength   units.Value
	RetractSpeed    units.Value
	ZHop            units.Value // nozzle lift during a retracted travel; 0 disables
	FanSpeed        int         // 0-255, part cooling fan speed after first few layers
	BridgeFanSpeed  int         // 0-255, part cooling fan while printing bridges (usually max)
	FilamentDensity units.Value // used for weight estimates in header
}

// ResolvedFilament is [Filament] in the emitter's units. Temperatures are whole
// degrees Celsius because M104 and M140 carry an integer.
type ResolvedFilament struct {
	Name            string
	Material        string
	NozzleTemp      int     // °C
	BedTemp         int     // °C
	FlowRatio       float64 // 1.0 = nominal
	RetractLength   float64 // mm
	RetractSpeed    float64 // mm/s
	ZHop            float64 // mm
	FanSpeed        int     // 0-255
	BridgeFanSpeed  int     // 0-255
	FilamentDensity float64 // g/cm^3
}

// Resolve converts the profile to the emitter's units, reporting the first
// field whose quantity is not of the kind that field measures.
func (f Filament) Resolve() (ResolvedFilament, error) {
	var r resolver
	out := ResolvedFilament{
		Name:            f.Name,
		Material:        f.Material,
		NozzleTemp:      r.celsius("NozzleTemp", f.NozzleTemp),
		BedTemp:         r.celsius("BedTemp", f.BedTemp),
		FlowRatio:       f.FlowRatio,
		RetractLength:   r.mm("RetractLength", f.RetractLength),
		RetractSpeed:    r.mmPerSec("RetractSpeed", f.RetractSpeed),
		ZHop:            r.mm("ZHop", f.ZHop),
		FanSpeed:        f.FanSpeed,
		BridgeFanSpeed:  f.BridgeFanSpeed,
		FilamentDensity: r.in("FilamentDensity", f.FilamentDensity, units.GramPerCubicCentimeter),
	}
	if r.err != nil {
		return ResolvedFilament{}, r.err
	}
	return out, nil
}

// DefaultFilament returns a generic PLA profile.
func DefaultFilament() Filament {
	return Filament{
		Name:            "Generic PLA",
		Material:        "PLA",
		NozzleTemp:      units.DegreesCelsius(210),
		BedTemp:         units.DegreesCelsius(60),
		FlowRatio:       1.0,
		RetractLength:   units.Millimeters(0.8),
		RetractSpeed:    units.MillimetersPerSecond(35),
		ZHop:            units.Millimeters(0.4),
		FanSpeed:        255,
		BridgeFanSpeed:  255,
		FilamentDensity: units.GramsPerCubicCentimeter(1.24),
	}
}

// Process describes how thick, dense and fast the print should be. These
// are the knobs an end user tweaks most often; printer and filament tend
// to stay fixed for a given build.
type Process struct {
	Name string

	LayerHeight      units.Value
	FirstLayerHeight units.Value

	LineWidth           units.Value // extruded line width for perimeters/infill
	FirstLayerLineWidth units.Value

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
	// SkirtDistance is the gap between the object (or brim, if any) and
	// the innermost skirt loop.
	SkirtDistance units.Value
	// BrimWidth is how far the brim extends outward from the object's
	// first-layer wall for bed adhesion; 0 disables. Rounded to a whole
	// number of line-width loops.
	BrimWidth units.Value

	// BridgeSpeed is the print speed for unsupported bridge surfaces,
	// kept low so the spanning strands have time to cool taut.
	BridgeSpeed units.Value
	// BridgeFlow scales the extrusion of bridge strands (1.0 = nominal). Some
	// profiles reduce it slightly so the strand stretches without sagging.
	BridgeFlow float64

	// SupportEnable turns on tree (organic) support generation under
	// overhangs. Off by default — supports add print time and need removal.
	SupportEnable bool
	// SupportThreshold is the overhang angle from vertical beyond which a
	// downward surface needs support; e.g. 50° supports surfaces that lean
	// out more than 50° from straight up.
	SupportThreshold units.Value
	// SupportBranchDiameter is the nominal diameter of a support branch
	// tip; merged trunks grow thicker toward the bed.
	SupportBranchDiameter units.Value
	// SupportSpeed is the print speed for support extrusions.
	SupportSpeed units.Value

	TravelSpeed            units.Value
	PerimeterSpeed         units.Value
	ExternalPerimeterSpeed units.Value
	InfillSpeed            units.Value
	SolidInfillSpeed       units.Value
	FirstLayerSpeed        units.Value

	// InfillAngles cycles through these angles for successive layers. Two
	// alternating angles is the OrcaSlicer/PrusaSlicer default; the MVP
	// keeps it that simple.
	InfillAngles []units.Value
}

// ResolvedProcess is [Process] in the pipeline's units: millimetres for
// lengths, mm/s for speeds, degrees for angles.
type ResolvedProcess struct {
	Name string

	LayerHeight      float64 // mm
	FirstLayerHeight float64 // mm

	LineWidth           float64 // mm
	FirstLayerLineWidth float64 // mm

	Perimeters   int
	TopLayers    int
	BottomLayers int

	InfillDensity float64
	InfillPattern InfillPattern
	SeamPosition  SeamPosition

	SkirtLoops    int
	SkirtDistance float64 // mm
	BrimWidth     float64 // mm

	BridgeSpeed float64 // mm/s
	BridgeFlow  float64

	SupportEnable         bool
	SupportThreshold      float64 // degrees from vertical
	SupportBranchDiameter float64 // mm
	SupportSpeed          float64 // mm/s

	// Speeds, all mm/s.
	TravelSpeed            float64
	PerimeterSpeed         float64
	ExternalPerimeterSpeed float64
	InfillSpeed            float64
	SolidInfillSpeed       float64
	FirstLayerSpeed        float64

	InfillAngles []float64 // degrees
}

// Resolve converts the profile to the pipeline's units, reporting the first
// field whose quantity is not of the kind that field measures.
func (p Process) Resolve() (ResolvedProcess, error) {
	var r resolver
	out := ResolvedProcess{
		Name:                   p.Name,
		LayerHeight:            r.mm("LayerHeight", p.LayerHeight),
		FirstLayerHeight:       r.mm("FirstLayerHeight", p.FirstLayerHeight),
		LineWidth:              r.mm("LineWidth", p.LineWidth),
		FirstLayerLineWidth:    r.mm("FirstLayerLineWidth", p.FirstLayerLineWidth),
		Perimeters:             p.Perimeters,
		TopLayers:              p.TopLayers,
		BottomLayers:           p.BottomLayers,
		InfillDensity:          p.InfillDensity,
		InfillPattern:          p.InfillPattern,
		SeamPosition:           p.SeamPosition,
		SkirtLoops:             p.SkirtLoops,
		SkirtDistance:          r.mm("SkirtDistance", p.SkirtDistance),
		BrimWidth:              r.mm("BrimWidth", p.BrimWidth),
		BridgeSpeed:            r.mmPerSec("BridgeSpeed", p.BridgeSpeed),
		BridgeFlow:             p.BridgeFlow,
		SupportEnable:          p.SupportEnable,
		SupportThreshold:       r.degrees("SupportThreshold", p.SupportThreshold),
		SupportBranchDiameter:  r.mm("SupportBranchDiameter", p.SupportBranchDiameter),
		SupportSpeed:           r.mmPerSec("SupportSpeed", p.SupportSpeed),
		TravelSpeed:            r.mmPerSec("TravelSpeed", p.TravelSpeed),
		PerimeterSpeed:         r.mmPerSec("PerimeterSpeed", p.PerimeterSpeed),
		ExternalPerimeterSpeed: r.mmPerSec("ExternalPerimeterSpeed", p.ExternalPerimeterSpeed),
		InfillSpeed:            r.mmPerSec("InfillSpeed", p.InfillSpeed),
		SolidInfillSpeed:       r.mmPerSec("SolidInfillSpeed", p.SolidInfillSpeed),
		FirstLayerSpeed:        r.mmPerSec("FirstLayerSpeed", p.FirstLayerSpeed),
	}
	for i, a := range p.InfillAngles {
		out.InfillAngles = append(out.InfillAngles, r.degrees(fmt.Sprintf("InfillAngles[%d]", i), a))
	}
	if r.err != nil {
		return ResolvedProcess{}, r.err
	}
	return out, nil
}

// DefaultProcess returns a 0.2mm "Standard" profile aimed at a 0.4 nozzle.
// Speeds are conservative defaults that work on most bed-slingers; tune up
// for CoreXY machines.
func DefaultProcess() Process {
	return Process{
		Name:                   "Standard 0.20",
		LayerHeight:            units.Millimeters(0.2),
		FirstLayerHeight:       units.Millimeters(0.2),
		LineWidth:              units.Millimeters(0.42),
		FirstLayerLineWidth:    units.Millimeters(0.5),
		Perimeters:             2,
		TopLayers:              4,
		BottomLayers:           3,
		InfillDensity:          0.15,
		InfillPattern:          InfillGrid,
		SeamPosition:           SeamAligned,
		SkirtLoops:             1,
		SkirtDistance:          units.Millimeters(2.0),
		BrimWidth:              units.Millimeters(0),
		BridgeSpeed:            units.MillimetersPerSecond(25),
		BridgeFlow:             1.0,
		SupportEnable:          false,
		SupportThreshold:       units.Degrees(50),
		SupportBranchDiameter:  units.Millimeters(2.0),
		SupportSpeed:           units.MillimetersPerSecond(40),
		TravelSpeed:            units.MillimetersPerSecond(200),
		PerimeterSpeed:         units.MillimetersPerSecond(60),
		ExternalPerimeterSpeed: units.MillimetersPerSecond(40),
		InfillSpeed:            units.MillimetersPerSecond(80),
		SolidInfillSpeed:       units.MillimetersPerSecond(50),
		FirstLayerSpeed:        units.MillimetersPerSecond(25),
		InfillAngles:           []units.Value{units.Degrees(45), units.Degrees(-45)},
	}
}
