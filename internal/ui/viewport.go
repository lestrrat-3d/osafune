// Package ui glues guigui widgets to the mesh loader, camera and CPU
// rasterizer. It exposes a Root widget suitable for guigui.Run.
package ui

import (
	"image"
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/guigui-gui/guigui"

	"github.com/lestrrat-go/makislicer/internal/mesh"
	"github.com/lestrrat-go/makislicer/internal/render"
	"github.com/lestrrat-go/makislicer/internal/slice"
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
)

// Viewport is the 3D viewport widget. It owns the camera and rasterizer and
// renders the current scene on every Draw. Mouse input is translated into
// camera updates inside HandlePointingInput.
type Viewport struct {
	guigui.DefaultWidget

	scene      *mesh.Scene
	layers     []slice.Layer
	mode       ViewMode
	cam        render.Camera
	raster     *render.Rasterizer
	toolpaths  *render.ToolpathDrawer
	dirty      bool // becomes true when the scene changes; Layout fits the camera on the next pass.
	background color.NRGBA

	// layerLo/layerHi clip which sliced layers the toolpath previewer
	// renders. Inclusive bounds; both -1 means "show every layer". The
	// values are mirrored from the layer-range slider in the toolbar.
	layerLo, layerHi int

	drag       dragMode
	dragPrev   image.Point
	dragButton ebiten.MouseButton
}

// NewViewport returns a Viewport with default camera/rasterizer. Set a mesh
// later via SetMesh.
func NewViewport() *Viewport {
	return &Viewport{
		cam:        render.Defaults(),
		raster:     render.New(),
		toolpaths:  render.NewToolpathDrawer(),
		background: color.NRGBA{R: 0xdc, G: 0xdc, B: 0xdc, A: 0xff},
		layerLo:    -1,
		layerHi:    -1,
	}
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
	v.layerLo, v.layerHi = lo, hi
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
	guigui.RequestRedraw(v)
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
	dst.SubImage(b).(*ebiten.Image).Fill(v.background)
	switch v.mode {
	case ViewMesh:
		v.raster.Draw(dst, b, v.scene, &v.cam)
	case ViewToolpaths:
		layers := v.layers
		if v.layerLo >= 0 && v.layerHi >= 0 && v.layerHi >= v.layerLo && v.layerHi < len(layers) {
			layers = layers[v.layerLo : v.layerHi+1]
		}
		v.toolpaths.Draw(dst, b, layers, &v.cam)
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
			if ebiten.IsKeyPressed(ebiten.KeyShift) || ebiten.IsKeyPressed(ebiten.KeyShiftLeft) || ebiten.IsKeyPressed(ebiten.KeyShiftRight) {
				v.drag = dragPan
			} else {
				v.drag = dragOrbit
			}
			v.dragButton = ebiten.MouseButtonLeft
			v.dragPrev = image.Pt(cx, cy)
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
			v.drag = dragNone
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
