// Package ui glues guigui widgets to the mesh loader, camera and CPU
// rasterizer. It exposes a Root widget suitable for guigui.Run.
package ui

import (
	"image"
	"image/color"
	"log/slog"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/guigui-gui/guigui"

	"github.com/lestrrat-3d/units"

	"github.com/lestrrat-3d/osafune/internal/config"
	"github.com/lestrrat-3d/osafune/internal/mesh"
	"github.com/lestrrat-3d/osafune/internal/render"
	"github.com/lestrrat-3d/osafune/internal/slice"
)

// ViewMode selects whether the viewport shows the source mesh or the
// sliced toolpaths. The mesh and toolpath views share a camera so the
// user keeps their orbit / pan when switching.
type ViewMode int

const (
	ViewMesh ViewMode = iota
	ViewToolpaths
)

// dragMode is the kind of camera manipulation in progress.
type dragMode int

const (
	dragNone dragMode = iota
	dragOrbit
	dragPan
	dragGizmo
)

// clickThresholdPx is the cursor travel below which a left press+release is
// treated as a click (select) rather than a drag (orbit).
const clickThresholdPx = 4

// Viewport is the 3D viewport widget. It owns the camera and rasterizer and
// renders the current scene on every Draw. Mouse input is translated into
// camera updates inside HandlePointingInput.
type Viewport struct {
	guigui.DefaultWidget

	scene     *mesh.Scene
	layers    []slice.Layer
	mode      ViewMode
	cam       render.Camera
	raster    *render.Rasterizer
	toolpaths *render.ToolpathDrawer
	env       *render.Environment
	dirty     bool // becomes true when the scene changes; Layout fits the camera on the next pass.

	// bedX/bedY are the build-plate dimensions (mm) the environment draws.
	// Defaulted from config.DefaultPrinter; SetBedSize updates them when a
	// project with a specific printer is loaded.
	bedX, bedY float64

	// layerLo/layerHi clip which sliced layers the toolpath previewer
	// renders. Inclusive bounds; both -1 means "show every layer". The
	// values are mirrored from the layer-range slider in the toolbar.
	layerLo, layerHi int

	// geomGen is bumped whenever the sliced layers or the visible layer
	// range change. It keys the toolpath drawer's geometry cache so a
	// static view re-issues prebuilt draw batches instead of reprojecting
	// every segment each frame.
	geomGen int

	drag       dragMode
	dragPrev   image.Point
	dragButton ebiten.MouseButton
	pressPt    image.Point // where the active left press began (click vs drag)

	// Object editing. selected indexes scene.Objects (-1 = none). gizmo
	// draws/hit-tests the transform handles; gizmoElem is the handle being
	// dragged and gizmoCenter the object centre captured at drag start (held
	// fixed so the rotation/scale pivot doesn't drift mid-drag).
	gizmo       *render.Gizmo
	selected    int
	gizmoElem   render.GizmoElement
	gizmoCenter mesh.Vec3
	gizmoRadius float64
}

// NewViewport returns a Viewport with default camera/rasterizer. Set a mesh
// later via SetMesh.
func NewViewport() *Viewport {
	// The built-in default profile is built from the units package's own
	// constructors, so resolving it cannot fail; a zero bed just draws no
	// plate, which is the same thing the zero Viewport would have shown.
	dp, _ := config.DefaultPrinter().Resolve()
	return &Viewport{
		cam:       render.Defaults(),
		raster:    render.New(),
		toolpaths: render.NewToolpathDrawer(),
		env:       render.NewEnvironment(),
		gizmo:     render.NewGizmo(),
		selected:  -1,
		bedX:      dp.BedSizeX,
		bedY:      dp.BedSizeY,
		layerLo:   -1,
		layerHi:   -1,
	}
}

// selectedObject returns the currently selected, visible object, or nil. It
// guards the index against scene changes that may have invalidated it.
func (v *Viewport) selectedObject() *mesh.Object {
	if v.scene == nil || v.selected < 0 || v.selected >= len(v.scene.Objects) {
		return nil
	}
	if v.scene.Objects[v.selected].Hidden {
		return nil
	}
	return &v.scene.Objects[v.selected]
}

// gizmoCenterRadius is the world centre and ring radius of the selection's
// gizmo, or ok=false when nothing manipulable is selected.
func (v *Viewport) gizmoCenterRadius() (mesh.Vec3, float64, bool) {
	obj := v.selectedObject()
	if obj == nil || obj.Mesh.Bounds.Empty() {
		return mesh.Vec3{}, 0, false
	}
	return obj.Mesh.Bounds.Center(), render.GizmoRadius(obj.Mesh.Bounds), true
}

// SetBedSize updates the build-plate dimensions (mm) the environment draws.
// Called when a project selects a printer with a bed other than the default.
func (v *Viewport) SetBedSize(x, y float64) {
	v.bedX, v.bedY = x, y
	guigui.RequestRedraw(v)
}

// SetLayers swaps to toolpath-preview mode showing layers. The mesh
// stays loaded so [Viewport.SetMode] can toggle back without reslicing.
// The layer-range clip is reset to "show all" so a freshly sliced model
// is fully visible.
func (v *Viewport) SetLayers(layers []slice.Layer) {
	v.layers = layers
	v.layerLo = 0
	v.layerHi = len(layers) - 1
	v.mode = ViewToolpaths
	v.geomGen++
	guigui.RequestRedraw(v)
}

// SetLayerRange clips toolpath rendering to layers [lo, hi] inclusive.
// Out-of-bounds values are clamped to the available layer count.
func (v *Viewport) SetLayerRange(lo, hi int) {
	if len(v.layers) == 0 {
		return
	}
	if lo < 0 {
		lo = 0
	}
	if hi >= len(v.layers) {
		hi = len(v.layers) - 1
	}
	if hi < lo {
		hi = lo
	}
	if lo == v.layerLo && hi == v.layerHi {
		return // no change — keep the cached batches valid
	}
	v.layerLo, v.layerHi = lo, hi
	v.geomGen++
	guigui.RequestRedraw(v)
}

// LayerCount returns the number of sliced layers currently held by the
// viewport. Returns 0 when nothing has been sliced yet.
func (v *Viewport) LayerCount() int { return len(v.layers) }

// SetMode switches between mesh and toolpath rendering. Toolpath mode
// is a no-op until [Viewport.SetLayers] has been called.
func (v *Viewport) SetMode(m ViewMode) {
	v.mode = m
	guigui.RequestRedraw(v)
}

// Mode returns the currently displayed view.
func (v *Viewport) Mode() ViewMode { return v.mode }

// SetScene replaces the displayed scene and asks for a fresh fit on the
// next layout pass. The fit is deferred to Layout because that's when the
// viewport's screen size is known.
func (v *Viewport) SetScene(s *mesh.Scene) {
	v.scene = s
	v.dirty = true
	v.selected = -1 // new geometry — drop any stale selection
	guigui.RequestRedraw(v)
}

// DropSelectedToBed reseats the selected object (or the whole scene when
// nothing is selected) so its lowest point rests on Z=0.
func (v *Viewport) DropSelectedToBed() {
	if obj := v.selectedObject(); obj != nil {
		obj.Mesh.DropToBed()
		guigui.RequestRedraw(v)
		return
	}
	if v.scene != nil && !v.scene.Bounds().Empty() {
		v.scene.Translate(mesh.Vec3{0, 0, -v.scene.Bounds().Min[2]})
		guigui.RequestRedraw(v)
	}
}

// ResetView reframes the camera around the current scene.
func (v *Viewport) ResetView() {
	v.cam = render.Defaults()
	if v.scene != nil {
		v.cam.Fit(v.scene.Bounds())
	}
	guigui.RequestRedraw(v)
}

// Layout is where we fit the camera the first time after a scene load.
// Layout always runs after Build, so by the time we get here the viewport
// has its real bounds and the camera can use them for the aspect.
func (v *Viewport) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	if v.dirty && v.scene != nil {
		v.cam.Fit(v.scene.Bounds())
		v.dirty = false
		guigui.RequestRedraw(v)
	}
}

// Draw clears the viewport background and renders either the mesh or
// the sliced toolpaths depending on the active [ViewMode].
func (v *Viewport) Draw(context *guigui.Context, widgetBounds *guigui.WidgetBounds, dst *ebiten.Image) {
	b := widgetBounds.Bounds()
	sub, ok := dst.SubImage(b).(*ebiten.Image)
	if !ok {
		return
	}
	// Gradient backdrop + build plate are drawn first; the mesh raster and the
	// toolpath drawer both leave their output transparent where no geometry
	// covers, so they composite cleanly on top of the environment.
	v.env.DrawBackground(sub, b)
	v.env.DrawPlate(sub, b, &v.cam, v.bedX, v.bedY)
	switch v.mode {
	case ViewMesh:
		v.raster.Draw(dst, b, v.scene, &v.cam)
		// Transform gizmo for the selected object sits on top of the mesh.
		if c, r, ok := v.gizmoCenterRadius(); ok {
			v.gizmo.Draw(dst, b, &v.cam, c, r, v.gizmoElem)
		}
	case ViewToolpaths:
		layers := v.layers
		// The body always renders as a solid wall shell. topCut/botCut mark
		// where the layer slider has been narrowed below the model's true
		// top/bottom — at each such cut the exposed cross-section is drawn as
		// toolpaths while the sides stay walls.
		var topCut, botCut bool
		if v.layerLo >= 0 && v.layerHi >= 0 && v.layerHi >= v.layerLo && v.layerHi < len(layers) {
			topCut = v.layerHi < len(layers)-1
			botCut = v.layerLo > 0
			layers = layers[v.layerLo : v.layerHi+1]
		}
		// v.drag != dragNone tells the drawer the camera is moving, so it can
		// render at reduced resolution for a responsive orbit.
		v.toolpaths.Draw(dst, b, layers, &v.cam, v.geomGen, v.drag != dragNone, topCut, botCut)
		v.drawLegend(dst, b)
	}
}

// drawLegend stamps a small colour key in the top-left of the viewport
// so the user can decode the toolpath palette without consulting docs.
// Only invoked from toolpath mode — the mesh view has nothing to label.
func (v *Viewport) drawLegend(dst *ebiten.Image, viewportBounds image.Rectangle) {
	entries := render.LegendEntries()
	const (
		pad    = 8
		swatch = 12
		row    = 16
	)
	bgW := 130
	bgH := pad*2 + row*len(entries)
	x := viewportBounds.Min.X + pad
	y := viewportBounds.Min.Y + pad
	// ebitenutil.DebugPrintAt draws white text and has no colour
	// parameter; a dark translucent background is the simplest way to
	// keep the labels readable without pulling in a real font face.
	vector.DrawFilledRect(dst, float32(x), float32(y), float32(bgW), float32(bgH),
		color.NRGBA{0x20, 0x20, 0x20, 0xd0}, false)
	for i, e := range entries {
		sy := y + pad + i*row
		vector.DrawFilledRect(dst,
			float32(x+pad), float32(sy+2),
			float32(swatch), float32(swatch),
			e.Color, false)
		ebitenutil.DebugPrintAt(dst, e.Label, x+pad+swatch+6, sy)
	}
}

// HandlePointingInput translates mouse input into camera changes. Guigui
// only calls this when pointing input is active, so the no-op cost when the
// user isn't moving the mouse is zero.
func (v *Viewport) HandlePointingInput(context *guigui.Context, widgetBounds *guigui.WidgetBounds) guigui.HandleInputResult {
	bounds := widgetBounds.Bounds()
	cx, cy := ebiten.CursorPosition()
	inside := image.Pt(cx, cy).In(bounds)

	var changed bool

	// Wheel: zoom about the centre. Only fires when the cursor is inside the
	// viewport so it doesn't fight with a scrollable toolbar above.
	if inside {
		_, wy := ebiten.Wheel()
		if wy != 0 {
			// 1.1^(-wheelY) gives the conventional "scroll up = zoom in".
			factor := float32(math.Pow(1.1, -wy))
			v.cam.Zoom(factor)
			changed = true
		}
	}

	// Drag bookkeeping. We start a drag only when the press happens inside
	// the viewport, but the drag continues even if the cursor leaves: that
	// matches how every CAD tool behaves.
	switch {
	case v.drag == dragNone && inside:
		switch {
		case inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft):
			v.dragButton = ebiten.MouseButtonLeft
			v.pressPt = image.Pt(cx, cy)
			v.dragPrev = v.pressPt
			switch {
			case v.gizmoHitAt(bounds, cx, cy) != render.GizmoNone:
				// Gizmo grabs first: a press on a handle manipulates the
				// selected object instead of orbiting.
				v.startGizmoDrag(v.gizmoHitAt(bounds, cx, cy))
			case ebiten.IsKeyPressed(ebiten.KeyShift) || ebiten.IsKeyPressed(ebiten.KeyShiftLeft) || ebiten.IsKeyPressed(ebiten.KeyShiftRight):
				v.drag = dragPan
			default:
				v.drag = dragOrbit
			}
		case inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonMiddle):
			v.drag = dragPan
			v.dragButton = ebiten.MouseButtonMiddle
			v.dragPrev = image.Pt(cx, cy)
		case inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonRight):
			// Right-drag pans, matching most CAD tools. Middle-drag
			// kept as an alias for users on a trackball/laptop.
			v.drag = dragPan
			v.dragButton = ebiten.MouseButtonRight
			v.dragPrev = image.Pt(cx, cy)
		}
	case v.drag != dragNone:
		// End the drag when the originating button is released. Checking
		// the specific button (not "any button") matters when a user
		// presses left + middle and releases one — only the active drag
		// should stop.
		if !ebiten.IsMouseButtonPressed(v.dragButton) {
			switch {
			case v.drag == dragGizmo:
				// A rotated/scaled part may now sink into or float above the
				// plate; reseat it so it rests on the bed.
				if obj := v.selectedObject(); obj != nil {
					obj.Mesh.DropToBed()
				}
				v.gizmoElem = render.GizmoNone
			case v.dragButton == ebiten.MouseButtonLeft &&
				absInt(cx-v.pressPt.X) <= clickThresholdPx && absInt(cy-v.pressPt.Y) <= clickThresholdPx:
				// A left press that barely moved is a click → (de)select the
				// object under the cursor.
				v.handleClickSelect(bounds, cx, cy)
			}
			v.drag = dragNone
			// Drag just ended: request one more redraw so the viewport
			// re-renders at full detail (the draft drawn during the drag
			// was walls-only).
			changed = true
		} else {
			dx := float32(cx - v.dragPrev.X)
			dy := float32(cy - v.dragPrev.Y)
			if dx != 0 || dy != 0 {
				switch v.drag {
				case dragOrbit:
					// Drag follows the model: pulling the cursor left
					// turns the model left. The camera orbits in the
					// opposite direction, hence the leading minus signs.
					vh := float32(bounds.Dy())
					if vh < 1 {
						vh = 1
					}
					v.cam.Orbit(-dx*math.Pi/vh, dy*math.Pi/vh)
				case dragPan:
					v.cam.PanScreen(dx, dy, bounds.Dy())
				case dragGizmo:
					v.applyGizmoDrag(bounds, cx, cy)
				}
				v.dragPrev = image.Pt(cx, cy)
				changed = true
			}
		}
	}

	if changed {
		guigui.RequestRedraw(v)
		return guigui.HandleInputByWidget(v)
	}
	return guigui.HandleInputResult{}
}

// gizmoHitAt returns the gizmo handle under window-pixel (cx,cy), or
// GizmoNone. Only the mesh view (where editing happens) has a gizmo.
func (v *Viewport) gizmoHitAt(bounds image.Rectangle, cx, cy int) render.GizmoElement {
	if v.mode != ViewMesh {
		return render.GizmoNone
	}
	c, r, ok := v.gizmoCenterRadius()
	if !ok {
		return render.GizmoNone
	}
	return v.gizmo.Hit(bounds, &v.cam, c, r, float32(cx), float32(cy))
}

// startGizmoDrag captures the pivot (object centre) and ring radius so the
// rotation/scale stays anchored for the duration of the drag.
func (v *Viewport) startGizmoDrag(elem render.GizmoElement) {
	c, r, ok := v.gizmoCenterRadius()
	if !ok {
		return
	}
	v.drag = dragGizmo
	v.gizmoElem = elem
	v.gizmoCenter = c
	v.gizmoRadius = r
}

// handleClickSelect picks the object under the cursor (window pixels) and
// makes it the selection, or clears the selection when clicking empty space.
func (v *Viewport) handleClickSelect(bounds image.Rectangle, cx, cy int) {
	if v.mode != ViewMesh {
		return
	}
	// PickRay works in viewport-local pixels, so offset by the widget origin.
	lx := float32(cx - bounds.Min.X)
	ly := float32(cy - bounds.Min.Y)
	v.selected = render.PickObject(v.scene, &v.cam, lx, ly, float32(bounds.Dx()), float32(bounds.Dy()))
}

// applyGizmoDrag maps the cursor motion since the last frame to a rotation
// (angle swept about the gizmo centre) or a uniform scale (radial-distance
// ratio), applied about the captured pivot.
func (v *Viewport) applyGizmoDrag(bounds image.Rectangle, cx, cy int) {
	obj := v.selectedObject()
	if obj == nil {
		return
	}
	gcx, gcy, ok := v.gizmo.ScreenCenter(bounds, &v.cam, v.gizmoCenter)
	if !ok {
		return
	}
	prevX, prevY := float32(v.dragPrev.X), float32(v.dragPrev.Y)
	nowX, nowY := float32(cx), float32(cy)

	if v.gizmoElem == render.GizmoScale {
		prevD := math.Hypot(float64(prevX-gcx), float64(prevY-gcy))
		nowD := math.Hypot(float64(nowX-gcx), float64(nowY-gcy))
		if prevD > 1e-3 {
			obj.Mesh.ScaleUniform(v.gizmoCenter, nowD/prevD)
		}
		return
	}

	prevA := math.Atan2(float64(prevY-gcy), float64(prevX-gcx))
	nowA := math.Atan2(float64(nowY-gcy), float64(nowX-gcx))
	delta := nowA - prevA
	for delta > math.Pi {
		delta -= 2 * math.Pi
	}
	for delta < -math.Pi {
		delta += 2 * math.Pi
	}
	// Screen Y points down, so atan2 sweeps clockwise-positive; negate so
	// dragging a ring counter-clockwise turns the part counter-clockwise.
	// delta came out of atan2, so radians is what it is; naming that here is
	// the whole point of the typed angle.
	if err := obj.Mesh.Rotate(v.gizmoCenter, gizmoAxis(v.gizmoElem), units.Radians(-delta)); err != nil {
		slog.Error("rotate object", "err", err)
	}
}

func gizmoAxis(e render.GizmoElement) mesh.Axis {
	switch e {
	case render.GizmoRotateX:
		return mesh.AxisX
	case render.GizmoRotateY:
		return mesh.AxisY
	default:
		return mesh.AxisZ
	}
}

func absInt(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
