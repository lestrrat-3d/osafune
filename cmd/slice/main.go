// Command slice is the headless slicer entry point: take an STL or 3MF
// in, produce a gcode file out. It uses [config.DefaultPrinter] /
// [config.DefaultFilament] / [config.DefaultProcess] as a baseline and
// accepts a small set of CLI flag overrides for the knobs an MVP user
// is most likely to want to change without editing source.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/lestrrat-3d/units"

	"github.com/lestrrat-3d/osafune/internal/gcode"
	"github.com/lestrrat-3d/osafune/internal/mesh"
	"github.com/lestrrat-3d/osafune/internal/project"
	"github.com/lestrrat-3d/osafune/internal/slice"
)

func main() {
	out := flag.String("o", "out.gcode", "output gcode path")
	layerHeight := flag.Float64("layer", 0.2, "layer height in mm")
	perimeters := flag.Int("perimeters", 2, "perimeter (wall) count")
	infill := flag.Float64("infill", 0.15, "infill density 0..1")
	nozzle := flag.Float64("nozzle", 0.4, "nozzle diameter in mm")
	nozzleTemp := flag.Int("nozzle-temp", 210, "nozzle temperature °C")
	bedTemp := flag.Int("bed-temp", 60, "bed temperature °C")
	support := flag.Bool("support", false, "generate tree supports under overhangs")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: slice [flags] <input.stl|input.3mf>")
		os.Exit(2)
	}
	in := flag.Arg(0)

	scene, err := mesh.LoadFile(in)
	if err != nil {
		slog.Error("load mesh", "err", err)
		os.Exit(1)
	}

	proj := project.NewFromScene(scene)
	plate := &proj.Plates[0]
	// The flags are bare numbers, so each is given the unit its help text
	// promises right here, at the one boundary where the user's number
	// enters the program.
	plate.Process.LayerHeight = units.Millimeters(*layerHeight)
	plate.Process.FirstLayerHeight = units.Millimeters(*layerHeight)
	plate.Process.Perimeters = *perimeters
	plate.Process.InfillDensity = *infill
	plate.Printer.NozzleDiameter = units.Millimeters(*nozzle)
	plate.Filament.NozzleTemp = units.DegreesCelsius(float64(*nozzleTemp))
	plate.Filament.BedTemp = units.DegreesCelsius(float64(*bedTemp))
	plate.Process.SupportEnable = *support

	resolved, err := plate.Resolve()
	if err != nil {
		slog.Error("plate profiles", "err", err)
		os.Exit(1)
	}

	m := proj.PlateMesh(0)
	layers := slice.Slice(&m, &resolved.Printer, &resolved.Process)
	if len(layers) == 0 {
		slog.Error("slicer produced no layers (mesh outside printable Z range?)")
		os.Exit(1)
	}

	f, err := os.Create(*out)
	if err != nil {
		slog.Error("create output", "err", err)
		os.Exit(1)
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	defer bw.Flush()

	if err := gcode.Write(bw, layers, &resolved.Printer, &resolved.Filament, &resolved.Process); err != nil {
		slog.Error("emit gcode", "err", err)
		os.Exit(1)
	}

	var paths int
	for _, l := range layers {
		paths += len(l.Paths)
	}
	slog.Info("sliced", "layers", len(layers), "paths", paths, "out", *out)
}
