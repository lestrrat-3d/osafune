package slice

// PathRole tags an extrusion path with its purpose. The gcode emitter
// uses the role to pick the right speed and to emit a marker comment
// (`;TYPE:External perimeter`, OrcaSlicer-style) so gcode previewers can
// colorize the toolpath.
type PathRole int

const (
	RoleTravel PathRole = iota
	RoleExternalPerimeter
	RolePerimeter
	RoleInfill
	RoleSolidInfill
	// RoleSkirtBrim tags the first-layer adhesion loops — both the skirt
	// (free-standing priming loops around the print) and the brim (loops
	// fused to the object's outer wall). OrcaSlicer groups them under one
	// "Skirt/Brim" type, so we do too.
	RoleSkirtBrim
	// RoleBridge tags solid fill that spans empty space (no layer directly
	// beneath), printed slowly with extra cooling and span-aligned strands so
	// it sets taut instead of drooping.
	RoleBridge
)

// String returns the OrcaSlicer-compatible role marker used in gcode
// comments. These strings are what PrusaSlicer/OrcaSlicer's gcode preview
// recognises, so emitting them keeps preview tools working.
func (r PathRole) String() string {
	switch r {
	case RoleTravel:
		return "Travel"
	case RoleExternalPerimeter:
		return "External perimeter"
	case RolePerimeter:
		return "Perimeter"
	case RoleInfill:
		return "Internal infill"
	case RoleSolidInfill:
		return "Solid infill"
	case RoleSkirtBrim:
		return "Skirt/Brim"
	case RoleBridge:
		return "Bridge infill"
	}
	return "Unknown"
}

// IsExtrusion reports whether the path lays down filament (as opposed to
// a non-extruding travel).
func (r PathRole) IsExtrusion() bool { return r != RoleTravel }

// Path is one continuous run of head motion: either a travel (no
// extrusion) or an extrusion at a given width and speed. Closed paths
// implicitly return from Points[len-1] to Points[0]; open paths do not.
type Path struct {
	Points []Point2
	Role   PathRole
	Width  float64 // mm, extrusion width — needed to compute E per move
	Speed  float64 // mm/s
	Closed bool
}

// Layer holds everything the gcode emitter needs for one Z. Contours is
// the raw slice geometry (used by the toolpath previewer when no walls
// have been generated yet); Paths is the ordered, ready-to-extrude
// motion. Z is the top-of-layer height where the nozzle sits while
// drawing this layer.
type Layer struct {
	Index    int
	Z        float64 // mm, nozzle Z while printing this layer
	Height   float64 // mm, thickness of this layer (matters for E calc)
	Contours []ExPolygon
	Paths    []Path
}
