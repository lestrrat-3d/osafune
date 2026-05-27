// Package gcode turns a sliced [slice.Layer] sequence into a textual
// gcode file. The output is OrcaSlicer-style Marlin flavour: a header
// comment block carrying every config knob (so the file is reproducible
// and gcode previewers can colorize it), Marlin M-codes for temperature
// and acceleration, and G1 extrude/travel moves per layer with
// PrusaSlicer-compatible `;TYPE:` markers in front of each role.
package gcode

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/lestrrat-go/makislicer/internal/config"
	"github.com/lestrrat-go/makislicer/internal/slice"
)

// Writer streams gcode for a single plate. One Writer per output file:
// internal state (current position, current extruder E, current layer)
// makes reusing one across plates unsafe.
type Writer struct {
	w        io.Writer
	printer  *config.Printer
	filament *config.Filament
	process  *config.Process

	// Mutable head state. Z and E are tracked to emit relative-ish
	// moves; X/Y is tracked so we know when a travel is needed.
	x, y, z float64
	e       float64 // logical filament deposited (mm); the retraction offset is not folded in
	speed   float64 // last commanded F (mm/s) — converted to mm/min in gcode
	primed  bool    // true once at least one move has happened

	// retracted is true while the filament is pulled back (between a
	// retract before a travel and the prime at its destination). E is held
	// at e-RetractLength during that window.
	retracted bool
	// fan is the last commanded part-cooling fan PWM (0-255), or -1 before
	// any M106/M107 has been emitted, so the first layer always commands it.
	fan int
}

// New constructs a [Writer] for the given output stream and profiles.
func New(w io.Writer, printer *config.Printer, filament *config.Filament, process *config.Process) *Writer {
	return &Writer{w: w, printer: printer, filament: filament, process: process, fan: -1}
}

const (
	// retractMinTravel is the shortest travel (mm) worth retracting for:
	// shorter hops don't ooze enough to justify the wear of a retract/prime
	// cycle. Mirrors OrcaSlicer's "minimum travel after retraction".
	retractMinTravel = 1.5
	// firstFanLayer is the layer index from which the part-cooling fan runs
	// at the filament's configured speed; earlier layers print fan-off for
	// bed adhesion (OrcaSlicer likewise holds the fan off on the first layer).
	firstFanLayer = 1
)

// retract pulls the filament back by the filament profile's RetractLength
// before a travel, so the nozzle stops oozing across the gap. E is an
// absolute axis (M82), so a retract is a single move to e-RetractLength; the
// logical deposited length g.e is left untouched and restored by unretract.
// No-op when retraction is disabled (length<=0) or already retracted.
func (g *Writer) retract() error {
	l := g.filament.RetractLength
	if l <= 0 || g.retracted {
		return nil
	}
	speed := g.filament.RetractSpeed
	if speed <= 0 {
		speed = 30
	}
	g.retracted = true
	g.speed = speed // the next XY move differs, so it will re-emit F
	_, err := fmt.Fprintf(g.w, "G1 E%.5f F%.0f ; retract\n", g.e-l, speed*60)
	return err
}

// unretract restores the filament to the logical deposited length g.e at the
// travel's destination, priming the nozzle before the next extrusion. No-op
// when not currently retracted.
func (g *Writer) unretract() error {
	if !g.retracted {
		return nil
	}
	speed := g.filament.RetractSpeed
	if speed <= 0 {
		speed = 30
	}
	g.retracted = false
	g.speed = speed
	_, err := fmt.Fprintf(g.w, "G1 E%.5f F%.0f ; unretract\n", g.e, speed*60)
	return err
}

// setFan commands the part-cooling fan to the given PWM (0-255) when it
// differs from the last commanded value, emitting M107 for off and M106
// otherwise. Tracked so a steady fan speed isn't re-emitted every layer.
func (g *Writer) setFan(pwm int) error {
	if pwm < 0 {
		pwm = 0
	}
	if pwm > 255 {
		pwm = 255
	}
	if pwm == g.fan {
		return nil
	}
	g.fan = pwm
	if pwm == 0 {
		_, err := io.WriteString(g.w, "M107 ; fan off\n")
		return err
	}
	_, err := fmt.Fprintf(g.w, "M106 S%d ; fan\n", pwm)
	return err
}

// zhopUp lifts the nozzle by the filament's ZHop above the current layer
// height for the duration of a travel, so it clears already-printed walls
// rather than scraping across them. g.z stays at the base layer height — only
// the emitted Z changes — so zhopDown can restore it. No-op when disabled.
func (g *Writer) zhopUp() error {
	if g.filament.ZHop <= 0 {
		return nil
	}
	_, err := fmt.Fprintf(g.w, "G1 Z%.3f ; z-hop\n", g.z+g.filament.ZHop)
	return err
}

// zhopDown returns the nozzle to the base layer height at the travel's
// destination, before the next extrusion. No-op when z-hop is disabled.
func (g *Writer) zhopDown() error {
	if g.filament.ZHop <= 0 {
		return nil
	}
	_, err := fmt.Fprintf(g.w, "G1 Z%.3f\n", g.z)
	return err
}

// WriteHeader emits the comment header and start gcode. Call once before
// [Writer.WriteLayer] is called for the first layer. The header carries
// every relevant config value so a gcode previewer (or a human) can
// recover the exact slicer parameters used; OrcaSlicer / PrusaSlicer
// emit a similar block at the top of every output file.
func (g *Writer) WriteHeader(layers []slice.Layer) error {
	now := time.Now().UTC().Format(time.RFC3339)
	totalLayers := len(layers)
	var maxZ float64
	for _, l := range layers {
		if l.Z > maxZ {
			maxZ = l.Z
		}
	}
	header := strings.Builder{}
	fmt.Fprintf(&header, "; generated by makislicer at %s\n", now)
	fmt.Fprintf(&header, "; printer: %s (flavor %s)\n", g.printer.Name, g.printer.GcodeFlavor)
	fmt.Fprintf(&header, "; filament: %s (%s)\n", g.filament.Name, g.filament.Material)
	fmt.Fprintf(&header, "; process: %s\n", g.process.Name)
	fmt.Fprintf(&header, ";\n")
	fmt.Fprintf(&header, "; layer_height: %.3f\n", g.process.LayerHeight)
	fmt.Fprintf(&header, "; first_layer_height: %.3f\n", g.process.FirstLayerHeight)
	fmt.Fprintf(&header, "; line_width: %.3f\n", g.process.LineWidth)
	fmt.Fprintf(&header, "; perimeters: %d\n", g.process.Perimeters)
	fmt.Fprintf(&header, "; top_layers: %d\n", g.process.TopLayers)
	fmt.Fprintf(&header, "; bottom_layers: %d\n", g.process.BottomLayers)
	fmt.Fprintf(&header, "; infill_density: %.3f\n", g.process.InfillDensity)
	fmt.Fprintf(&header, "; nozzle_diameter: %.3f\n", g.printer.NozzleDiameter)
	fmt.Fprintf(&header, "; filament_diameter: %.3f\n", g.printer.FilamentDiameter)
	fmt.Fprintf(&header, "; nozzle_temperature: %d\n", g.filament.NozzleTemp)
	fmt.Fprintf(&header, "; bed_temperature: %d\n", g.filament.BedTemp)
	fmt.Fprintf(&header, "; retract_length: %.3f\n", g.filament.RetractLength)
	fmt.Fprintf(&header, "; retract_speed: %.0f\n", g.filament.RetractSpeed)
	fmt.Fprintf(&header, "; z_hop: %.3f\n", g.filament.ZHop)
	fmt.Fprintf(&header, "; seam_position: %s\n", g.process.SeamPosition)
	fmt.Fprintf(&header, "; fan_speed: %d\n", g.filament.FanSpeed)
	fmt.Fprintf(&header, "; total_layer_count: %d\n", totalLayers)
	fmt.Fprintf(&header, "; max_z_height: %.3f\n", maxZ)
	fmt.Fprintln(&header, ";")
	if _, err := io.WriteString(g.w, header.String()); err != nil {
		return err
	}

	// OrcaSlicer convention: M201/M203 acceleration & speed limits up
	// top so the firmware doesn't clamp moves silently.
	if err := g.writeLimits(); err != nil {
		return err
	}

	// Start gcode template — render the simple {placeholder} tokens.
	start := expandTemplate(g.printer.StartGcode, map[string]string{
		"bed_temperature":    fmt.Sprintf("%d", g.filament.BedTemp),
		"nozzle_temperature": fmt.Sprintf("%d", g.filament.NozzleTemp),
	})
	if _, err := io.WriteString(g.w, start); err != nil {
		return err
	}

	// Absolute extrusion + absolute positioning. The MVP emitter does
	// not use relative-extrusion (M83) anywhere; it's simpler to track
	// total E and emit absolute values per move.
	if _, err := io.WriteString(g.w, "G90 ; absolute positioning\nM82 ; absolute extrusion\nG92 E0 ; reset extruder\n"); err != nil {
		return err
	}
	g.e = 0
	return nil
}

// WriteFooter emits the end-gcode template. Call once after the last
// [Writer.WriteLayer] returns.
func (g *Writer) WriteFooter() error {
	end := expandTemplate(g.printer.EndGcode, nil)
	_, err := io.WriteString(g.w, end)
	return err
}

// WriteLayer emits every Path on layer L as gcode. Paths are emitted in
// the order they appear; a travel move is inserted before each path's
// first point if the head is not already there.
func (g *Writer) WriteLayer(layer *slice.Layer) error {
	fmt.Fprintf(g.w, ";LAYER_CHANGE\n;Z:%.3f\n;LAYER:%d\n", layer.Z, layer.Index)
	if g.printer.LayerChangeGcode != "" {
		if _, err := io.WriteString(g.w, g.printer.LayerChangeGcode); err != nil {
			return err
		}
	}
	// Part-cooling fan: off until firstFanLayer (bed adhesion), then the
	// filament's configured speed for the rest of the print.
	fanTarget := 0
	if layer.Index >= firstFanLayer {
		fanTarget = g.filament.FanSpeed
	}
	if err := g.setFan(fanTarget); err != nil {
		return err
	}
	// Move to new Z before laying down any extrusion on this layer.
	if err := g.moveZ(layer.Z, g.process.TravelSpeed); err != nil {
		return err
	}

	var currentRole slice.PathRole = -1
	for _, p := range layer.Paths {
		if p.Role != currentRole {
			fmt.Fprintf(g.w, ";TYPE:%s\n", p.Role)
			currentRole = p.Role
		}
		if err := g.writePath(&p, layer.Height); err != nil {
			return err
		}
	}
	return nil
}

// writePath travels to the start of p (if needed), then extrudes through
// each segment at the path's commanded speed. Closed paths get one extra
// segment back to the start point.
func (g *Writer) writePath(p *slice.Path, layerHeight float64) error {
	if len(p.Points) == 0 {
		return nil
	}
	start := p.Points[0]
	if !g.primed || math.Hypot(start.X-g.x, start.Y-g.y) > slice.Epsilon {
		dist := math.Hypot(start.X-g.x, start.Y-g.y)
		// Retract across non-trivial travels once we've started laying
		// filament, so the nozzle doesn't string over the gap; prime again at
		// the destination before extruding. Short hops and the very first
		// move (head still at the purge line) skip it.
		retractHere := g.primed && dist > retractMinTravel
		if retractHere {
			if err := g.retract(); err != nil {
				return err
			}
			// Lift after retracting so the hopped travel clears printed walls.
			if err := g.zhopUp(); err != nil {
				return err
			}
		}
		if err := g.travel(start, g.process.TravelSpeed); err != nil {
			return err
		}
		if retractHere {
			if err := g.zhopDown(); err != nil {
				return err
			}
			if err := g.unretract(); err != nil {
				return err
			}
		}
	}
	for i := 1; i < len(p.Points); i++ {
		to := p.Points[i]
		if err := g.extrude(to, p.Width, layerHeight, p.Speed); err != nil {
			return err
		}
	}
	if p.Closed && len(p.Points) >= 2 {
		if err := g.extrude(p.Points[0], p.Width, layerHeight, p.Speed); err != nil {
			return err
		}
	}
	return nil
}

// extrude emits one G1 to point `to`, computing the E increment from
// the segment length, line width, layer height and filament diameter so
// the volumetric flow is consistent.
func (g *Writer) extrude(to slice.Point2, width, layerHeight, speed float64) error {
	dx := to.X - g.x
	dy := to.Y - g.y
	dist := math.Hypot(dx, dy)
	if dist < slice.Epsilon {
		return nil
	}
	// Volumetric model: extruded line is a rectangle of cross-section
	// width × layerHeight. Filament cross-section is a circle of
	// diameter D. So Δfilament_length = dist * width * height / (π/4·D²).
	d := g.printer.FilamentDiameter
	filArea := math.Pi * d * d * 0.25
	flow := g.filament.FlowRatio
	if flow <= 0 {
		flow = 1.0
	}
	g.e += dist * width * layerHeight * flow / filArea
	return g.g1XY(to.X, to.Y, &g.e, speed)
}

// travel emits a non-extruding G1 to `to`.
func (g *Writer) travel(to slice.Point2, speed float64) error {
	dx := to.X - g.x
	dy := to.Y - g.y
	if math.Hypot(dx, dy) < slice.Epsilon {
		return nil
	}
	return g.g1XY(to.X, to.Y, nil, speed)
}

func (g *Writer) moveZ(z, speed float64) error {
	if math.Abs(z-g.z) < slice.Epsilon && g.primed {
		return nil
	}
	g.z = z
	g.primed = true
	if speedChanged := g.commandSpeed(speed); speedChanged {
		fmt.Fprintf(g.w, "G1 Z%.3f F%.0f\n", z, speed*60)
	} else {
		fmt.Fprintf(g.w, "G1 Z%.3f\n", z)
	}
	return nil
}

func (g *Writer) g1XY(x, y float64, e *float64, speed float64) error {
	g.x = x
	g.y = y
	g.primed = true
	speedChanged := g.commandSpeed(speed)
	switch {
	case e != nil && speedChanged:
		fmt.Fprintf(g.w, "G1 X%.3f Y%.3f E%.5f F%.0f\n", x, y, *e, speed*60)
	case e != nil:
		fmt.Fprintf(g.w, "G1 X%.3f Y%.3f E%.5f\n", x, y, *e)
	case speedChanged:
		fmt.Fprintf(g.w, "G1 X%.3f Y%.3f F%.0f\n", x, y, speed*60)
	default:
		fmt.Fprintf(g.w, "G1 X%.3f Y%.3f\n", x, y)
	}
	return nil
}

// commandSpeed returns true if the active feed-rate changed and the
// next G1 should include an F field. Marlin keeps the last F across
// moves, so we only emit F when it changes — keeps the output compact.
func (g *Writer) commandSpeed(speed float64) bool {
	if math.Abs(speed-g.speed) < 0.01 {
		return false
	}
	g.speed = speed
	return true
}

func (g *Writer) writeLimits() error {
	p := g.printer
	if p.MaxAccelX > 0 || p.MaxAccelY > 0 || p.MaxAccelZ > 0 || p.MaxAccelE > 0 {
		fmt.Fprintf(g.w, "M201 X%.0f Y%.0f Z%.0f E%.0f ; max accel\n",
			p.MaxAccelX, p.MaxAccelY, p.MaxAccelZ, p.MaxAccelE)
	}
	if p.MaxSpeedX > 0 || p.MaxSpeedY > 0 || p.MaxSpeedZ > 0 || p.MaxSpeedE > 0 {
		fmt.Fprintf(g.w, "M203 X%.0f Y%.0f Z%.0f E%.0f ; max speed\n",
			p.MaxSpeedX, p.MaxSpeedY, p.MaxSpeedZ, p.MaxSpeedE)
	}
	return nil
}

// expandTemplate replaces `[token]` occurrences with the corresponding
// value. Tokens not in the map are left in place — that's permissive
// behaviour appropriate for an MVP where the user might have an
// elaborate start-gcode with vendor-specific macros.
func expandTemplate(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "["+k+"]", v)
	}
	return s
}

// Write drives the whole emission for a sliced plate in one call:
// header, every layer, footer. Convenience wrapper around the lower-level
// methods for callers that don't need per-layer streaming.
func Write(w io.Writer, layers []slice.Layer, printer *config.Printer, filament *config.Filament, process *config.Process) error {
	gw := New(w, printer, filament, process)
	if err := gw.WriteHeader(layers); err != nil {
		return err
	}
	for i := range layers {
		if err := gw.WriteLayer(&layers[i]); err != nil {
			return err
		}
	}
	return gw.WriteFooter()
}
