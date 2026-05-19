package ui

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"

	"github.com/guigui-gui/guigui"
	"github.com/guigui-gui/guigui/basicwidget"

	"github.com/lestrrat-go/makislicer/internal/config"
	"github.com/lestrrat-go/makislicer/internal/gcode"
	"github.com/lestrrat-go/makislicer/internal/mesh"
	"github.com/lestrrat-go/makislicer/internal/project"
	"github.com/lestrrat-go/makislicer/internal/slice"
)

// FileSaver picks an output path. Same shape as [FileOpener] so a single
// dialog implementation can satisfy both with a save vs. open mode.
type FileSaver interface {
	PickSave(defaultName string) (path string, ok bool)
}

// FileOpener selects a path interactively. The Root widget calls it from the
// Open button so we can swap implementations (native dialog, mock for tests,
// drag-and-drop later) without touching the widget tree.
type FileOpener interface {
	Pick() (path string, ok bool)
}

// Root is the top-level guigui widget: a toolbar above a (left-pane + 3D
// viewport) row.
type Root struct {
	guigui.DefaultWidget

	background       basicwidget.Background
	openButton       basicwidget.Button
	resetButton      basicwidget.Button
	sliceButton      basicwidget.Button
	meshButton       basicwidget.Button
	saveButton       basicwidget.Button
	patternLabel     basicwidget.Text
	patternSelect    basicwidget.Select[config.InfillPattern]
	densityLabel     basicwidget.Text
	densityInput     basicwidget.NumberInput
	objectPane       ObjectPane
	viewport         *Viewport
	layerSlider      LayerRangeSlider
	opener           FileOpener
	saver            FileSaver

	// infillPattern and infillDensityPct are the user's last selections;
	// they override the defaults whenever runSlice runs. Density is
	// stored as a 0-100 percent because that's what the NumberInput
	// works with — converted to 0..1 at the boundary.
	infillPattern      config.InfillPattern
	infillDensityPct   int
	infillItemsLoaded  bool // true after the first Build populated the pattern dropdown

	// pendingPath, when non-empty, is a path queued for loading on the next
	// Build pass. Setting it from Build (e.g. from the Open button's OnUp)
	// would cause the load to run during widget construction, which both
	// blocks UI updates and risks reentrancy; instead we capture the path
	// here and pull it through Build.
	pendingMu   sync.Mutex
	pendingPath string

	// project + lastLayers cache the slicer state so the Save Gcode
	// button does not need to reslice.
	project    *project.Project
	lastLayers []slice.Layer

	toolbarItems []guigui.LinearLayoutItem
	bodyItems    []guigui.LinearLayoutItem
	rootItems    []guigui.LinearLayoutItem

	// initialPath is the file given on the command line. It is consumed by
	// the first Build call.
	initialPath string
}

// NewRoot constructs the application root. opener is invoked when the user
// clicks "Open"; pass nil to hide the Open button. saver is invoked when
// the user clicks "Save Gcode"; pass nil to hide that button.
func NewRoot(opener FileOpener, saver FileSaver, initialPath string) *Root {
	defaults := config.DefaultProcess()
	return &Root{
		viewport:         NewViewport(),
		opener:           opener,
		saver:            saver,
		initialPath:      initialPath,
		infillPattern:    defaults.InfillPattern,
		infillDensityPct: int(defaults.InfillDensity * 100),
	}
}

func (r *Root) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	adder.AddWidget(&r.background)
	adder.AddWidget(&r.openButton)
	adder.AddWidget(&r.resetButton)
	adder.AddWidget(&r.sliceButton)
	adder.AddWidget(&r.meshButton)
	adder.AddWidget(&r.saveButton)
	adder.AddWidget(&r.patternLabel)
	adder.AddWidget(&r.patternSelect)
	adder.AddWidget(&r.densityLabel)
	adder.AddWidget(&r.densityInput)
	adder.AddWidget(&r.objectPane)
	adder.AddWidget(r.viewport)
	adder.AddWidget(&r.layerSlider)

	r.patternLabel.SetValue("Pattern")
	if !r.infillItemsLoaded {
		// First Build pass: populate the dropdown with the supported
		// patterns. Selected index defaults to whatever
		// r.infillPattern was initialised to.
		r.patternSelect.SetItems([]basicwidget.SelectItem[config.InfillPattern]{
			{Text: "Rectilinear", Value: config.InfillRectilinear},
			{Text: "Grid", Value: config.InfillGrid},
			{Text: "Triangles", Value: config.InfillTriangles},
			{Text: "Concentric", Value: config.InfillConcentric},
		})
		r.patternSelect.SelectItemByValue(r.infillPattern)
		r.infillItemsLoaded = true
	}
	r.patternSelect.OnItemSelected(func(context *guigui.Context, index int) {
		if it, ok := r.patternSelect.ItemByIndex(index); ok {
			r.infillPattern = it.Value
		}
	})

	r.densityLabel.SetValue("Infill %")
	r.densityInput.SetMinimumValue(0)
	r.densityInput.SetMaximumValue(100)
	r.densityInput.SetValue(r.infillDensityPct)
	r.densityInput.OnValueChanged(func(context *guigui.Context, value int, committed bool) {
		r.infillDensityPct = value
	})
	r.layerSlider.OnChanged(func(lo, hi int) {
		r.viewport.SetLayerRange(lo, hi)
	})
	// Slider is meaningful only in toolpath mode with layers loaded.
	context.SetEnabled(&r.layerSlider, r.viewport.Mode() == ViewToolpaths && r.viewport.LayerCount() > 0)

	r.openButton.SetText("Open…")
	r.openButton.OnUp(func(context *guigui.Context) {
		if r.opener == nil {
			return
		}
		path, ok := r.opener.Pick()
		if !ok {
			return
		}
		r.queueLoad(path)
	})
	context.SetEnabled(&r.openButton, r.opener != nil)

	r.resetButton.SetText("Reset View")
	r.resetButton.OnUp(func(context *guigui.Context) {
		r.viewport.ResetView()
	})

	r.sliceButton.SetText("Slice")
	r.sliceButton.OnUp(func(context *guigui.Context) {
		r.runSlice()
	})
	context.SetEnabled(&r.sliceButton, r.project != nil || r.viewport.scene != nil)

	r.meshButton.SetText("Show Mesh")
	r.meshButton.OnUp(func(context *guigui.Context) {
		r.viewport.SetMode(ViewMesh)
	})
	context.SetEnabled(&r.meshButton, r.viewport.Mode() == ViewToolpaths)

	r.saveButton.SetText("Save Gcode…")
	r.saveButton.OnUp(func(context *guigui.Context) {
		r.saveGcode()
	})
	context.SetEnabled(&r.saveButton, r.saver != nil && len(r.lastLayers) > 0)

	// Consume any pending path queued by OnUp / startup.
	if r.initialPath != "" {
		r.queueLoad(r.initialPath)
		r.initialPath = ""
	}
	r.flushPending()

	return nil
}

func (r *Root) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	bounds := widgetBounds.Bounds()
	layouter.LayoutWidget(&r.background, bounds)

	u := basicwidget.UnitSize(context)

	r.toolbarItems = slices.Delete(r.toolbarItems, 0, len(r.toolbarItems))
	r.toolbarItems = append(r.toolbarItems,
		guigui.LinearLayoutItem{Widget: &r.openButton, Size: guigui.FixedSize(5 * u)},
		guigui.LinearLayoutItem{Widget: &r.resetButton, Size: guigui.FixedSize(6 * u)},
		guigui.LinearLayoutItem{Widget: &r.sliceButton, Size: guigui.FixedSize(5 * u)},
		guigui.LinearLayoutItem{Widget: &r.meshButton, Size: guigui.FixedSize(6 * u)},
		guigui.LinearLayoutItem{Widget: &r.saveButton, Size: guigui.FixedSize(7 * u)},
		guigui.LinearLayoutItem{Widget: &r.patternLabel, Size: guigui.FixedSize(3 * u)},
		guigui.LinearLayoutItem{Widget: &r.patternSelect, Size: guigui.FixedSize(7 * u)},
		guigui.LinearLayoutItem{Widget: &r.densityLabel, Size: guigui.FixedSize(3 * u)},
		guigui.LinearLayoutItem{Widget: &r.densityInput, Size: guigui.FixedSize(4 * u)},
		guigui.LinearLayoutItem{Size: guigui.FlexibleSize(1)},
	)
	toolbar := guigui.LinearLayout{
		Direction: guigui.LayoutDirectionHorizontal,
		Items:     r.toolbarItems,
		Gap:       u / 2,
	}

	r.bodyItems = slices.Delete(r.bodyItems, 0, len(r.bodyItems))
	r.bodyItems = append(r.bodyItems,
		guigui.LinearLayoutItem{Widget: &r.objectPane, Size: guigui.FixedSize(10 * u)},
		guigui.LinearLayoutItem{Widget: r.viewport, Size: guigui.FlexibleSize(1)},
		guigui.LinearLayoutItem{Widget: &r.layerSlider, Size: guigui.FixedSize(2 * u)},
	)
	body := guigui.LinearLayout{
		Direction: guigui.LayoutDirectionHorizontal,
		Items:     r.bodyItems,
	}

	r.rootItems = slices.Delete(r.rootItems, 0, len(r.rootItems))
	r.rootItems = append(r.rootItems,
		guigui.LinearLayoutItem{Size: guigui.FixedSize(2 * u), Layout: &toolbar},
		guigui.LinearLayoutItem{Size: guigui.FlexibleSize(1), Layout: &body},
	)
	(guigui.LinearLayout{
		Direction: guigui.LayoutDirectionVertical,
		Items:     r.rootItems,
		Gap:       u / 2,
		Padding:   guigui.Padding{Start: u / 2, Top: u / 2, End: u / 2, Bottom: u / 2},
	}).LayoutWidgets(context, bounds, layouter)
}

func (r *Root) queueLoad(path string) {
	r.pendingMu.Lock()
	r.pendingPath = path
	r.pendingMu.Unlock()
	guigui.RequestRebuild(r)
}

func (r *Root) flushPending() {
	r.pendingMu.Lock()
	path := r.pendingPath
	r.pendingPath = ""
	r.pendingMu.Unlock()
	if path == "" {
		return
	}
	scene, err := mesh.LoadFile(path)
	if err != nil {
		slog.Error("load failed", "path", path, "err", err)
		return
	}
	// Place the scene on the build plate up-front so the viewer and the
	// slicer share one coordinate system. Without this the viewer shows
	// the model in its file-native location while the slicer (via
	// [project.Project.AutoArrange]) operates on a bed-centred copy, and
	// the toolpath preview ends up offset from the visible mesh.
	placeSceneOnBed(scene, config.DefaultPrinter())
	b := scene.Bounds()
	slog.Info("loaded scene",
		"path", path,
		"objects", len(scene.Objects),
		"triangles", scene.TriangleCount(),
		"bounds_min", fmt.Sprint(b.Min),
		"bounds_max", fmt.Sprint(b.Max),
	)
	r.viewport.SetScene(scene)
	r.viewport.SetMode(ViewMesh)
	r.objectPane.SetScene(scene, func() {
		// Object visibility toggled — viewport has a stale frame.
		guigui.RequestRedraw(r.viewport)
	})
	// New mesh → discard any previously-cached slicer state.
	r.project = nil
	r.lastLayers = nil
}

// placeSceneOnBed translates every triangle in the scene so its
// minimum Z lands on 0 (bed level) and its XY footprint is centred on
// the printer's build area. Mutates the scene in place. The fallback
// when the scene has no triangles (or no bounds) is a no-op.
func placeSceneOnBed(s *mesh.Scene, printer config.Printer) {
	b := s.Bounds()
	if b.Empty() {
		return
	}
	cx := (b.Min[0] + b.Max[0]) * 0.5
	cy := (b.Min[1] + b.Max[1]) * 0.5
	s.Translate(mesh.Vec3{
		float32(printer.BedSizeX*0.5) - cx,
		float32(printer.BedSizeY*0.5) - cy,
		-b.Min[2],
	})
}

// runSlice builds a [project.Project] from the currently-loaded scene
// and runs the slicer end-to-end. The result is cached on the Root so
// the Save Gcode button can write it out without reslicing. The viewer
// switches to toolpath preview when this succeeds.
//
// Slicing runs synchronously on the UI thread. For the meshes the MVP
// targets (Benchy / Wind Turbine class) this is fast enough that a
// frame skip is invisible; a background goroutine is the obvious
// follow-up when bigger models start to lag.
func (r *Root) runSlice() {
	if r.viewport.scene == nil {
		return
	}
	proj := project.NewFromScene(r.viewport.scene)
	plate := &proj.Plates[0]
	// Apply the toolbar's pattern + density choices on top of the
	// default process before slicing.
	plate.Process.InfillPattern = r.infillPattern
	plate.Process.InfillDensity = float64(r.infillDensityPct) / 100.0
	m := proj.PlateMesh(0)
	layers := slice.Slice(&m, &plate.Printer, &plate.Process)
	if len(layers) == 0 {
		slog.Warn("slicer produced no layers")
		return
	}
	r.project = proj
	r.lastLayers = layers
	r.viewport.SetLayers(layers)
	r.layerSlider.SetRange(0, len(layers)-1)
	r.layerSlider.SetValues(0, len(layers)-1)
	var paths int
	for _, l := range layers {
		paths += len(l.Paths)
	}
	slog.Info("sliced", "layers", len(layers), "paths", paths)
}

// saveGcode prompts the user for an output path via the [FileSaver],
// then writes the cached layers to it. Does nothing when there is no
// cached slice or no saver wired up.
func (r *Root) saveGcode() {
	if r.saver == nil || len(r.lastLayers) == 0 || r.project == nil {
		return
	}
	path, ok := r.saver.PickSave("plate.gcode")
	if !ok {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		slog.Error("create gcode", "path", path, "err", err)
		return
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	defer bw.Flush()
	plate := &r.project.Plates[0]
	if err := gcode.Write(bw, r.lastLayers, &plate.Printer, &plate.Filament, &plate.Process); err != nil {
		slog.Error("write gcode", "path", path, "err", err)
		return
	}
	slog.Info("saved gcode", "path", path, "layers", len(r.lastLayers))
}
