package ui

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/guigui-gui/guigui"
	"github.com/guigui-gui/guigui/basicwidget"

	"github.com/lestrrat-3d/osafune/internal/config"
	"github.com/lestrrat-3d/osafune/internal/gcode"
	"github.com/lestrrat-3d/osafune/internal/mesh"
	"github.com/lestrrat-3d/osafune/internal/project"
	"github.com/lestrrat-3d/osafune/internal/slice"
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

	background        basicwidget.Background
	openButton        basicwidget.Button
	resetButton       basicwidget.Button
	dropButton        basicwidget.Button
	saveProjectButton basicwidget.Button
	sliceButton       basicwidget.Button
	meshButton        basicwidget.Button
	saveButton        basicwidget.Button
	patternLabel      basicwidget.Text
	patternSelect     basicwidget.Select[config.InfillPattern]
	densityLabel      basicwidget.Text
	densityInput      basicwidget.NumberInput
	supportLabel      basicwidget.Text
	supportToggle     basicwidget.Toggle
	objectPane        ObjectPane
	viewport          *Viewport
	layerSlider       LayerRangeSlider
	opener            FileOpener
	saver             FileSaver

	// supportEnabled mirrors the Support toggle; runSlice feeds it into the
	// plate so the slicer generates tree supports when on.
	supportEnabled bool

	// infillPattern and infillDensityPct are the user's last selections;
	// they override the defaults whenever runSlice runs. Density is
	// stored as a 0-100 percent because that's what the NumberInput
	// works with — converted to 0..1 at the boundary.
	infillPattern     config.InfillPattern
	infillDensityPct  int
	infillItemsLoaded bool // true after the first Build populated the pattern dropdown

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

	// loadedPlate holds the profiles restored from an opened project .3mf, so
	// slicing and re-saving honour them instead of the built-in defaults.
	loadedPlate *project.Plate

	// slicing tracks whether a background slice goroutine is in flight.
	// Read and written from the UI thread only (the goroutine itself
	// never touches this field); subsequent Slice clicks are dropped
	// while it is true. sliceCh is a 1-slot mailbox the background
	// goroutine drops the finished slice into; Build drains it on the
	// next pass and clears slicing.
	slicing bool
	sliceCh chan sliceResult

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
	// Seed the on-disk profile store on first run so the default printer /
	// filament / process profiles always exist for the user to copy or edit.
	if err := config.EnsureDefaultProfiles(); err != nil {
		slog.Warn("seed default profiles", "err", err)
	}
	defaults := config.DefaultProcess()
	return &Root{
		viewport:         NewViewport(),
		opener:           opener,
		saver:            saver,
		initialPath:      initialPath,
		infillPattern:    defaults.InfillPattern,
		infillDensityPct: int(defaults.InfillDensity * 100),
		sliceCh:          make(chan sliceResult, 1),
	}
}

func (r *Root) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	adder.AddWidget(&r.background)
	adder.AddWidget(&r.openButton)
	adder.AddWidget(&r.resetButton)
	adder.AddWidget(&r.dropButton)
	adder.AddWidget(&r.saveProjectButton)
	adder.AddWidget(&r.sliceButton)
	adder.AddWidget(&r.meshButton)
	adder.AddWidget(&r.saveButton)
	adder.AddWidget(&r.patternLabel)
	adder.AddWidget(&r.patternSelect)
	adder.AddWidget(&r.densityLabel)
	adder.AddWidget(&r.densityInput)
	adder.AddWidget(&r.supportLabel)
	adder.AddWidget(&r.supportToggle)
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

	r.supportLabel.SetValue("Support")
	r.supportToggle.SetValue(r.supportEnabled)
	r.supportToggle.OnValueChanged(func(context *guigui.Context, value bool) {
		r.supportEnabled = value
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

	r.dropButton.SetText("Drop to Bed")
	r.dropButton.OnUp(func(context *guigui.Context) {
		r.viewport.DropSelectedToBed()
	})
	context.SetEnabled(&r.dropButton, r.viewport.Mode() == ViewMesh && r.viewport.scene != nil)

	r.saveProjectButton.SetText("Save Project…")
	r.saveProjectButton.OnUp(func(context *guigui.Context) {
		r.saveProject()
	})
	context.SetEnabled(&r.saveProjectButton, r.saver != nil && r.viewport.scene != nil)

	r.sliceButton.SetText("Slice")
	r.sliceButton.OnUp(func(context *guigui.Context) {
		r.runSlice()
	})
	// Disable while a slice is in flight so a second click can't queue
	// up behind the first. The background goroutine flips this off via
	// the sliceCh drain below.
	context.SetEnabled(&r.sliceButton, !r.slicing && (r.project != nil || r.viewport.scene != nil))

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
	r.drainSliceResult()

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
		guigui.LinearLayoutItem{Widget: &r.dropButton, Size: guigui.FixedSize(6 * u)},
		guigui.LinearLayoutItem{Widget: &r.saveProjectButton, Size: guigui.FixedSize(7 * u)},
		guigui.LinearLayoutItem{Widget: &r.sliceButton, Size: guigui.FixedSize(5 * u)},
		guigui.LinearLayoutItem{Widget: &r.meshButton, Size: guigui.FixedSize(6 * u)},
		guigui.LinearLayoutItem{Widget: &r.saveButton, Size: guigui.FixedSize(7 * u)},
		guigui.LinearLayoutItem{Widget: &r.patternLabel, Size: guigui.FixedSize(3 * u)},
		guigui.LinearLayoutItem{Widget: &r.patternSelect, Size: guigui.FixedSize(7 * u)},
		guigui.LinearLayoutItem{Widget: &r.densityLabel, Size: guigui.FixedSize(3 * u)},
		guigui.LinearLayoutItem{Widget: &r.densityInput, Size: guigui.FixedSize(4 * u)},
		guigui.LinearLayoutItem{Widget: &r.supportLabel, Size: guigui.FixedSize(4 * u)},
		guigui.LinearLayoutItem{Widget: &r.supportToggle, Size: guigui.FixedSize(3 * u)},
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
	// A .3mf may be one of our saved projects (geometry + embedded settings);
	// load it through the project reader so the profiles come back too. Any
	// other file (STL, or a plain 3MF) loads as bare geometry with defaults.
	var scene *mesh.Scene
	r.loadedPlate = nil
	if strings.EqualFold(filepath.Ext(path), ".3mf") {
		s, plate, err := project.LoadProjectFile(path)
		if err != nil {
			slog.Error("load failed", "path", path, "err", err)
			return
		}
		scene = s
		r.loadedPlate = plate
		r.infillPattern = plate.Process.InfillPattern
		r.infillDensityPct = int(plate.Process.InfillDensity*100 + 0.5)
	} else {
		s, err := mesh.LoadFile(path)
		if err != nil {
			slog.Error("load failed", "path", path, "err", err)
			return
		}
		scene = s
	}
	// Place the scene on the build plate up-front so the viewer and the
	// slicer share one coordinate system. Without this the viewer shows
	// the model in its file-native location while the slicer (via
	// [project.Project.AutoArrange]) operates on a bed-centred copy, and
	// the toolpath preview ends up offset from the visible mesh.
	profile := config.DefaultPrinter()
	if r.loadedPlate != nil {
		profile = r.loadedPlate.Printer
	}
	printer, err := profile.Resolve()
	if err != nil {
		slog.Error("printer profile", "path", path, "err", err)
		return
	}
	placeSceneOnBed(scene, printer)
	r.viewport.SetBedSize(printer.BedSizeX, printer.BedSizeY)
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
func placeSceneOnBed(s *mesh.Scene, printer config.ResolvedPrinter) {
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

// sliceResult is the payload the background slice goroutine hands back
// to the UI thread via Root.sliceCh. The scene pointer is the one the
// goroutine sliced from: if a fresh mesh has been loaded in the
// meantime drainSliceResult uses it to discard the stale output rather
// than overwriting the new scene's state.
type sliceResult struct {
	scene   *mesh.Scene
	project *project.Project
	layers  []slice.Layer
}

// runSlice kicks off a background slice of the currently-loaded scene
// and returns immediately. The actual slicing — perimeters, infill,
// the lot — runs on a goroutine so the UI keeps rendering and the user
// can still orbit the camera while a big model crunches. Subsequent
// clicks are dropped while a slice is in flight; the Slice button is
// also disabled in Build() so this should never trigger in normal use.
//
// The goroutine doesn't touch any Root fields directly — it ships the
// finished slice through r.sliceCh, and Build()'s drainSliceResult
// applies the result on the UI thread on the next frame. That keeps
// guigui's widget state owned by a single goroutine.
func (r *Root) runSlice() {
	if r.slicing || r.viewport.scene == nil {
		return
	}
	// Snapshot the inputs at click time so a later toolbar tweak
	// doesn't change what gets sliced mid-run.
	scene := r.viewport.scene
	cp := r.currentPlate() // loaded/default profiles + live infill overrides
	r.slicing = true
	go func() {
		proj := project.NewFromScene(scene)
		plate := &proj.Plates[0]
		plate.Printer = cp.Printer
		plate.Filament = cp.Filament
		plate.Process = cp.Process
		// Re-arrange for the (possibly non-default) bed now that the printer
		// is the one we'll actually slice with.
		if err := proj.AutoArrange(0); err != nil {
			slog.Error("arrange plate", "err", err)
			r.sliceCh <- sliceResult{scene: scene}
			guigui.RequestRebuild(r)
			return
		}
		resolved, err := plate.Resolve()
		if err != nil {
			slog.Error("plate profiles", "err", err)
			r.sliceCh <- sliceResult{scene: scene}
			guigui.RequestRebuild(r)
			return
		}
		m := proj.PlateMesh(0)
		layers := slice.Slice(&m, &resolved.Printer, &resolved.Process)
		// Buffered, 1-slot, and slicing gate prevents concurrent
		// senders — this send is non-blocking in practice.
		r.sliceCh <- sliceResult{scene: scene, project: proj, layers: layers}
		guigui.RequestRebuild(r)
	}()
}

// drainSliceResult picks up the output of any background slice that
// has finished since the last Build pass and applies it to the widget
// tree. Called from Build so all widget mutation happens on the UI
// thread.
func (r *Root) drainSliceResult() {
	select {
	case res := <-r.sliceCh:
		r.slicing = false
		if res.scene != r.viewport.scene {
			// A new mesh was loaded between the click and the slice
			// completing — the result belongs to a scene that is no
			// longer on screen. Drop it.
			slog.Info("discarding slice result for stale scene")
			return
		}
		if len(res.layers) == 0 {
			slog.Warn("slicer produced no layers")
			return
		}
		// Re-slices keep the user's current layer-range selection so
		// hitting Slice after a parameter tweak doesn't yank them back
		// to a full-stack view. First-time slices fall through to the
		// SetLayers default (show everything).
		prevLo, prevHi := r.layerSlider.Lower(), r.layerSlider.Upper()
		reslice := len(r.lastLayers) > 0

		r.project = res.project
		r.lastLayers = res.layers
		r.viewport.SetLayers(res.layers)
		r.layerSlider.SetRange(0, len(res.layers)-1)
		if reslice {
			r.layerSlider.SetValues(prevLo, prevHi)
			r.viewport.SetLayerRange(r.layerSlider.Lower(), r.layerSlider.Upper())
		} else {
			r.layerSlider.SetValues(0, len(res.layers)-1)
		}
		var paths int
		for _, l := range res.layers {
			paths += len(l.Paths)
		}
		slog.Info("sliced", "layers", len(res.layers), "paths", paths)
		// The enabled-state of the layer slider, Save and Show Mesh buttons
		// is decided earlier in this same Build pass — before this drain ran
		// — so they were still evaluated against the pre-slice state (no
		// layers, mesh mode). Request another Build so they re-enable on the
		// next frame; without it they stay dead until some unrelated event
		// (an orbit/pan) triggers the next rebuild.
		guigui.RequestRebuild(r)
	default:
	}
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
	resolved, err := plate.Resolve()
	if err != nil {
		slog.Error("plate profiles", "path", path, "err", err)
		return
	}
	if err := gcode.Write(bw, r.lastLayers, &resolved.Printer, &resolved.Filament, &resolved.Process); err != nil {
		slog.Error("write gcode", "path", path, "err", err)
		return
	}
	slog.Info("saved gcode", "path", path, "layers", len(r.lastLayers))
}

// currentPlate is the plate used for slicing and project saving: the profiles
// loaded from an opened project (or the built-in defaults), with the toolbar's
// live infill pattern / density applied on top.
func (r *Root) currentPlate() *project.Plate {
	plate := project.Plate{
		Name:     "Plate 1",
		Printer:  config.DefaultPrinter(),
		Filament: config.DefaultFilament(),
		Process:  config.DefaultProcess(),
	}
	if r.loadedPlate != nil {
		plate = *r.loadedPlate
	}
	plate.Process.InfillPattern = r.infillPattern
	plate.Process.InfillDensity = float64(r.infillDensityPct) / 100.0
	plate.Process.SupportEnable = r.supportEnabled
	return &plate
}

// saveProject writes the current scene + settings to a .3mf project chosen via
// the save dialog (appending the extension if the user omitted it).
func (r *Root) saveProject() {
	if r.saver == nil || r.viewport.scene == nil {
		return
	}
	path, ok := r.saver.PickSave("project.3mf")
	if !ok {
		return
	}
	if !strings.EqualFold(filepath.Ext(path), ".3mf") {
		path += ".3mf"
	}
	if err := project.SaveProjectFile(path, r.viewport.scene, r.currentPlate()); err != nil {
		slog.Error("save project", "path", path, "err", err)
		return
	}
	slog.Info("saved project", "path", path, "objects", len(r.viewport.scene.Objects))
}
