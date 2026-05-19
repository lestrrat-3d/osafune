package ui

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/guigui-gui/guigui"
	"github.com/guigui-gui/guigui/basicwidget"
)

// activeThumb tags which handle of [LayerRangeSlider] the user is
// currently dragging. The "none" case is the resting state; the other
// two are the lower- and upper-bound thumbs.
type activeThumb int

const (
	thumbNone activeThumb = iota
	thumbLower
	thumbUpper
)

// LayerRangeSlider is a vertical dual-handle slider used to clip which
// layers of a sliced model the toolpath previewer renders. The lower
// thumb at the bottom sets the first visible layer (typically layer 0 =
// the bed); the upper thumb at the top sets the last visible one.
//
// guigui's bundled basicwidget.Slider is horizontal-only and
// single-handle, so this is a hand-rolled widget that follows the same
// Build/Layout/Draw/HandlePointingInput protocol but draws a vertical
// track and two thumbs with [ebiten/vector] primitives.
type LayerRangeSlider struct {
	guigui.DefaultWidget

	// Inclusive bounds available; clamps the user's selection.
	rangeLo, rangeHi int
	// Current selection. lower <= upper, both inside [rangeLo, rangeHi].
	lower, upper int

	onChanged func(lower, upper int)
	drag      activeThumb
}

// SetRange sets the available layer interval. If the new interval no
// longer contains the current selection, the selection is clamped to
// the full new range — typical when a fresh slice produces a different
// number of layers.
func (s *LayerRangeSlider) SetRange(lo, hi int) {
	if hi < lo {
		hi = lo
	}
	s.rangeLo = lo
	s.rangeHi = hi
	if s.lower < lo || s.upper > hi || s.upper-s.lower < 0 {
		s.lower = lo
		s.upper = hi
	}
}

// SetValues programmatically sets both thumbs. Out-of-range values are
// clamped, and lower is forced <= upper.
func (s *LayerRangeSlider) SetValues(lower, upper int) {
	if lower < s.rangeLo {
		lower = s.rangeLo
	}
	if upper > s.rangeHi {
		upper = s.rangeHi
	}
	if upper < lower {
		upper = lower
	}
	s.lower, s.upper = lower, upper
}

// Lower returns the lower thumb's value.
func (s *LayerRangeSlider) Lower() int { return s.lower }

// Upper returns the upper thumb's value.
func (s *LayerRangeSlider) Upper() int { return s.upper }

// OnChanged registers a callback fired whenever the user drags either
// thumb. The callback runs with the post-update values.
func (s *LayerRangeSlider) OnChanged(f func(lower, upper int)) { s.onChanged = f }

func (s *LayerRangeSlider) thumbRadius(context *guigui.Context) int {
	return int(basicwidget.UnitSize(context) * 7 / 16)
}

// trackBounds returns the vertical strip the track is drawn in. The
// thumbs slide between trackBounds.Min.Y + radius (= upper end) and
// trackBounds.Max.Y - radius (= lower end). Y-up in user terms, Y-down
// in screen coords, so the upper thumb sits at smaller screen Y.
func (s *LayerRangeSlider) trackBounds(context *guigui.Context, widgetBounds *guigui.WidgetBounds) image.Rectangle {
	b := widgetBounds.Bounds()
	strokeW := int(5 * context.Scale())
	cx := (b.Min.X + b.Max.X) / 2
	return image.Rect(cx-strokeW/2, b.Min.Y, cx+strokeW/2, b.Max.Y)
}

// valueToY maps a layer value to a screen Y. Higher value → smaller
// (upper) Y so the slider reads "bed at the bottom, top of print at the
// top," matching OrcaSlicer.
func (s *LayerRangeSlider) valueToY(context *guigui.Context, widgetBounds *guigui.WidgetBounds, v int) int {
	tb := s.trackBounds(context, widgetBounds)
	r := s.thumbRadius(context)
	y0 := tb.Min.Y + r
	y1 := tb.Max.Y - r
	if s.rangeHi <= s.rangeLo {
		return y1
	}
	rate := float64(v-s.rangeLo) / float64(s.rangeHi-s.rangeLo)
	// Invert rate so high values land at small Y.
	return y1 - int(rate*float64(y1-y0))
}

// yToValue is the inverse of valueToY, snapping to the nearest integer
// layer.
func (s *LayerRangeSlider) yToValue(context *guigui.Context, widgetBounds *guigui.WidgetBounds, y int) int {
	tb := s.trackBounds(context, widgetBounds)
	r := s.thumbRadius(context)
	y0 := tb.Min.Y + r
	y1 := tb.Max.Y - r
	if y1 <= y0 {
		return s.rangeLo
	}
	if y <= y0 {
		return s.rangeHi
	}
	if y >= y1 {
		return s.rangeLo
	}
	rate := float64(y1-y) / float64(y1-y0)
	v := s.rangeLo + int(rate*float64(s.rangeHi-s.rangeLo)+0.5)
	if v < s.rangeLo {
		v = s.rangeLo
	}
	if v > s.rangeHi {
		v = s.rangeHi
	}
	return v
}

func (s *LayerRangeSlider) thumbBounds(context *guigui.Context, widgetBounds *guigui.WidgetBounds, v int) image.Rectangle {
	tb := s.trackBounds(context, widgetBounds)
	r := s.thumbRadius(context)
	cx := (tb.Min.X + tb.Max.X) / 2
	cy := s.valueToY(context, widgetBounds, v)
	return image.Rect(cx-r, cy-r, cx+r, cy+r)
}

// Measure asks for a fixed-width strip that fills the available height.
// 1.5 unit-widths is enough to host the thumb and a comfortable hit area
// without crowding the viewport.
func (s *LayerRangeSlider) Measure(context *guigui.Context, constraints guigui.Constraints) image.Point {
	u := basicwidget.UnitSize(context)
	return image.Pt(3*u/2, u*4)
}

// HandlePointingInput selects whichever thumb is closer to the cursor on
// press, then drags it on subsequent moves until release. Clicking on
// the track jumps the nearer thumb.
func (s *LayerRangeSlider) HandlePointingInput(context *guigui.Context, widgetBounds *guigui.WidgetBounds) guigui.HandleInputResult {
	if !context.IsEnabled(s) {
		return guigui.HandleInputResult{}
	}
	cx, cy := ebiten.CursorPosition()
	cursor := image.Pt(cx, cy)
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) && widgetBounds.IsHitAtCursor() {
		lowerB := s.thumbBounds(context, widgetBounds, s.lower)
		upperB := s.thumbBounds(context, widgetBounds, s.upper)
		switch {
		case cursor.In(lowerB):
			s.drag = thumbLower
		case cursor.In(upperB):
			s.drag = thumbUpper
		default:
			// Track click: jump the nearer thumb to the cursor.
			yLow := s.valueToY(context, widgetBounds, s.lower)
			yUp := s.valueToY(context, widgetBounds, s.upper)
			if abs(cy-yLow) < abs(cy-yUp) {
				s.drag = thumbLower
			} else {
				s.drag = thumbUpper
			}
			s.applyDrag(context, widgetBounds, cy)
		}
		return guigui.HandleInputByWidget(s)
	}
	if !ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		s.drag = thumbNone
		return guigui.HandleInputResult{}
	}
	if s.drag != thumbNone {
		s.applyDrag(context, widgetBounds, cy)
		return guigui.HandleInputByWidget(s)
	}
	return guigui.HandleInputResult{}
}

func (s *LayerRangeSlider) applyDrag(context *guigui.Context, widgetBounds *guigui.WidgetBounds, cy int) {
	v := s.yToValue(context, widgetBounds, cy)
	switch s.drag {
	case thumbLower:
		if v > s.upper {
			v = s.upper
		}
		if v == s.lower {
			return
		}
		s.lower = v
	case thumbUpper:
		if v < s.lower {
			v = s.lower
		}
		if v == s.upper {
			return
		}
		s.upper = v
	default:
		return
	}
	if s.onChanged != nil {
		s.onChanged(s.lower, s.upper)
	}
	guigui.RequestRedraw(s)
}

// Draw renders the track, the filled "in-range" segment, and the two
// thumbs. Colors mirror what basicwidget.Slider uses so the widget fits
// the rest of the toolbar visually.
func (s *LayerRangeSlider) Draw(context *guigui.Context, widgetBounds *guigui.WidgetBounds, dst *ebiten.Image) {
	tb := s.trackBounds(context, widgetBounds)
	r := s.thumbRadius(context)
	// Full track in a muted grey.
	trackBG := color.NRGBA{0xc0, 0xc0, 0xc0, 0xff}
	vector.DrawFilledRect(dst,
		float32(tb.Min.X), float32(tb.Min.Y+r),
		float32(tb.Dx()), float32(tb.Dy()-2*r),
		trackBG, false)
	// Filled segment between the two thumbs.
	yLow := s.valueToY(context, widgetBounds, s.lower)
	yUp := s.valueToY(context, widgetBounds, s.upper)
	fill := color.NRGBA{0x40, 0x80, 0xff, 0xff}
	vector.DrawFilledRect(dst,
		float32(tb.Min.X), float32(yUp),
		float32(tb.Dx()), float32(yLow-yUp),
		fill, false)
	// Thumbs.
	thumbColor := color.NRGBA{0xff, 0xff, 0xff, 0xff}
	border := color.NRGBA{0x40, 0x40, 0x40, 0xff}
	for _, v := range [2]int{s.lower, s.upper} {
		tbnd := s.thumbBounds(context, widgetBounds, v)
		cx := float32((tbnd.Min.X + tbnd.Max.X)) / 2
		cy := float32((tbnd.Min.Y + tbnd.Max.Y)) / 2
		vector.DrawFilledCircle(dst, cx, cy, float32(r), thumbColor, true)
		vector.StrokeCircle(dst, cx, cy, float32(r), 1.5, border, true)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
