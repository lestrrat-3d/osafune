// Package ui glues guigui widgets to the mesh loader, camera and CPU
// rasterizer. It exposes a Root widget suitable for guigui.Run.
package ui

import (
	"image"
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/guigui-gui/guigui"

	"github.com/lestrrat-go/makislicer/internal/mesh"
	"github.com/lestrrat-go/makislicer/internal/render"
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
	cam        render.Camera
	raster     *render.Rasterizer
	dirty      bool // becomes true when the scene changes; Layout fits the camera on the next pass.
	background color.NRGBA

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
		background: color.NRGBA{R: 0xdc, G: 0xdc, B: 0xdc, A: 0xff},
	}
}

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

// Draw clears the viewport background and rasterizes the current scene.
func (v *Viewport) Draw(context *guigui.Context, widgetBounds *guigui.WidgetBounds, dst *ebiten.Image) {
	b := widgetBounds.Bounds()
	dst.SubImage(b).(*ebiten.Image).Fill(v.background)
	v.raster.Draw(dst, b, v.scene, &v.cam)
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
