package gcode_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-3d/osafune/internal/config"
	"github.com/lestrrat-3d/osafune/internal/gcode"
	"github.com/lestrrat-3d/osafune/internal/slice"
)

// wall returns an open extrusion path between two points.
func wall(role slice.PathRole, pts ...slice.Point2) slice.Path {
	return slice.Path{Points: pts, Role: role, Width: 0.4, Speed: 60}
}

// emit runs the whole writer over layers with the given profiles and returns
// the gcode text.
func emit(t *testing.T, printer config.Printer, fil config.Filament, proc config.Process, layers []slice.Layer) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, gcode.Write(&buf, layers, &printer, &fil, &proc))
	return buf.String()
}

// printBody returns the emitted gcode up to the end-gcode template, so
// assertions about the printed layers aren't confused by retract/fan tokens
// that the start/end gcode templates happen to contain (the default end
// gcode includes its own " ; retract" comment, for instance).
func printBody(out string) string {
	if i := strings.Index(out, "; --- osafune end gcode ---"); i >= 0 {
		return out[:i]
	}
	return out
}

func TestRetractionOnLongTravel(t *testing.T) {
	t.Parallel()
	// Two far-apart paths in one layer force a long inter-path travel.
	layers := []slice.Layer{{
		Index: 0, Z: 0.2, Height: 0.2,
		Paths: []slice.Path{
			wall(slice.RoleExternalPerimeter, slice.Point2{X: 0, Y: 0}, slice.Point2{X: 10, Y: 0}),
			wall(slice.RoleExternalPerimeter, slice.Point2{X: 50, Y: 50}, slice.Point2{X: 60, Y: 50}),
		},
	}}
	out := emit(t, config.DefaultPrinter(), config.DefaultFilament(), config.DefaultProcess(), layers)

	body := printBody(out)
	require.Contains(t, body, " ; retract", "long travel must retract")
	require.Contains(t, body, " ; unretract", "must prime again at the destination")
	// Retract pulls back RetractLength (0.8) from the deposited length, then
	// unretract restores it: the two E values must differ by exactly 0.8.
	rE := eValueOnLine(t, body, " ; retract")
	uE := eValueOnLine(t, body, " ; unretract")
	require.InDelta(t, config.DefaultFilament().RetractLength, uE-rE, 1e-4,
		"unretract must restore exactly RetractLength")
}

func TestNoRetractionOnShortTravel(t *testing.T) {
	t.Parallel()
	// Second path starts 0.5mm from where the first ended — below the
	// retractMinTravel threshold, so no retract/prime cycle.
	layers := []slice.Layer{{
		Index: 0, Z: 0.2, Height: 0.2,
		Paths: []slice.Path{
			wall(slice.RolePerimeter, slice.Point2{X: 0, Y: 0}, slice.Point2{X: 10, Y: 0}),
			wall(slice.RolePerimeter, slice.Point2{X: 10.5, Y: 0}, slice.Point2{X: 20, Y: 0}),
		},
	}}
	out := emit(t, config.DefaultPrinter(), config.DefaultFilament(), config.DefaultProcess(), layers)
	require.NotContains(t, printBody(out), " ; retract", "short hop must not retract")
}

func TestRetractionDisabled(t *testing.T) {
	t.Parallel()
	fil := config.DefaultFilament()
	fil.RetractLength = 0 // disabled
	layers := []slice.Layer{{
		Index: 0, Z: 0.2, Height: 0.2,
		Paths: []slice.Path{
			wall(slice.RoleExternalPerimeter, slice.Point2{X: 0, Y: 0}, slice.Point2{X: 10, Y: 0}),
			wall(slice.RoleExternalPerimeter, slice.Point2{X: 50, Y: 50}, slice.Point2{X: 60, Y: 50}),
		},
	}}
	out := emit(t, config.DefaultPrinter(), fil, config.DefaultProcess(), layers)
	require.NotContains(t, printBody(out), " ; retract", "retraction disabled when RetractLength<=0")
}

func TestFanOffFirstLayerThenOn(t *testing.T) {
	t.Parallel()
	fil := config.DefaultFilament() // FanSpeed 255
	layers := []slice.Layer{
		{Index: 0, Z: 0.2, Height: 0.2, Paths: []slice.Path{wall(slice.RoleExternalPerimeter, slice.Point2{X: 0, Y: 0}, slice.Point2{X: 10, Y: 0})}},
		{Index: 1, Z: 0.4, Height: 0.2, Paths: []slice.Path{wall(slice.RoleExternalPerimeter, slice.Point2{X: 0, Y: 0}, slice.Point2{X: 10, Y: 0})}},
	}
	out := emit(t, config.DefaultPrinter(), fil, config.DefaultProcess(), layers)

	offIdx := strings.Index(out, "M107 ; fan off")
	onIdx := strings.Index(out, "M106 S255 ; fan")
	require.GreaterOrEqual(t, offIdx, 0, "first layer prints fan-off")
	require.GreaterOrEqual(t, onIdx, 0, "later layer turns the fan on at FanSpeed")
	require.Less(t, offIdx, onIdx, "fan-off (layer 0) must precede fan-on (layer 1)")
}

// eValueOnLine returns the E argument of the first G1 line containing marker.
func eValueOnLine(t *testing.T, out, marker string) float64 {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		for _, tok := range strings.Fields(line) {
			if strings.HasPrefix(tok, "E") {
				v, err := strconv.ParseFloat(tok[1:], 64)
				require.NoError(t, err)
				return v
			}
		}
	}
	t.Fatalf("no E value found on line containing %q", marker)
	return 0
}
