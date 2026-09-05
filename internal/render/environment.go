package render

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/lestrrat-3d/osafune/internal/mesh"
)

// Environment draws the non-model framing of the viewport: a vertical
// gradient background and an OrcaSlicer-style build plate with a measurement
// grid and origin axes. It carries no per-frame state, so a single instance
// is shared by the viewport across mesh and toolpath modes; both the mesh
// rasterizer and the toolpath drawer composite their (transparent-where-empty)
// output on top of whatever Environment has already drawn.
type Environment struct {
	// TopColor/BottomColor are the vertical background gradient stops.
	TopColor, BottomColor color.NRGBA
	// PlateColor fills the build surface; GridColor / MajorColor draw the
	// minor and every-fifth grid lines on top of it.
	PlateColor color.NRGBA
	GridColor  color.NRGBA
	MajorColor color.NRGBA
	// GridStep is the minor grid spacing in millimetres; every fifth line is
	// drawn in MajorColor.
	GridStep float64
}

// NewEnvironment returns an Environment with an OrcaSlicer-like cool-gray
// gradient and a dark plate with light grid lines.
func NewEnvironment() *Environment {
	return &Environment{
		TopColor:    color.NRGBA{0xe9, 0xec, 0xf1, 0xff},
		BottomColor: color.NRGBA{0xbf, 0xc5, 0xcd, 0xff},
		PlateColor:  color.NRGBA{0x36, 0x3b, 0x42, 0xff},
		GridColor:   color.NRGBA{0x5a, 0x60, 0x69, 0xff},
		MajorColor:  color.NRGBA{0x82, 0x8a, 0x95, 0xff},
		GridStep:    10,
	}
}

// DrawBackground fills bounds with a top-to-bottom vertical gradient using a
// two-triangle quad whose vertex colours Ebitengine interpolates. Drawn
// before any model so it reads as the scene backdrop.
func (e *Environment) DrawBackground(dst *ebiten.Image, bounds image.Rectangle) {
	x0, y0 := float32(bounds.Min.X), float32(bounds.Min.Y)
	x1, y1 := float32(bounds.Max.X), float32(bounds.Max.Y)
	tr, tg, tb, ta := nrgbaToScale(e.TopColor)
	br, bg, bb, ba := nrgbaToScale(e.BottomColor)
	verts := []ebiten.Vertex{
		{DstX: x0, DstY: y0, ColorR: tr, ColorG: tg, ColorB: tb, ColorA: ta},
		{DstX: x1, DstY: y0, ColorR: tr, ColorG: tg, ColorB: tb, ColorA: ta},
		{DstX: x1, DstY: y1, ColorR: br, ColorG: bg, ColorB: bb, ColorA: ba},
		{DstX: x0, DstY: y1, ColorR: br, ColorG: bg, ColorB: bb, ColorA: ba},
	}
	indices := []uint16{0, 1, 2, 0, 2, 3}
	dst.DrawTriangles(verts, indices, getWhite(), &ebiten.DrawTrianglesOptions{})
}

// DrawPlate projects the build surface ([0,bedX]×[0,bedY] at z=0) for cam and
// draws it into bounds: a filled plate quad, a minor/major measurement grid,
// and red/green X/Y origin axes — the way OrcaSlicer grounds the model on the
// bed. bedX/bedY are in millimetres; a zero or negative bed draws nothing.
func (e *Environment) DrawPlate(dst *ebiten.Image, bounds image.Rectangle, cam *Camera, bedX, bedY float64) {
	if bedX <= 0 || bedY <= 0 {
		return
	}
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return
	}
	vp := cam.ViewProj(float32(w) / float32(h))
	fw, fh := float32(w), float32(h)
	ox, oy := float32(bounds.Min.X), float32(bounds.Min.Y)

	// toScreen projects a world point to viewport pixels; ok is false when the
	// point is behind the near plane (its projection is meaningless).
	toScreen := func(p mesh.Vec3) (sx, sy float32, ok bool) {
		pr := vp.Project(p)
		if !pr.InFront {
			return 0, 0, false
		}
		return ox + (pr.X+1)*0.5*fw, oy + (1-(pr.Y+1)*0.5)*fh, true
	}

	// Filled plate quad. Only drawn when all four corners are in front; a
	// partial clip would need near-plane clipping the previewer doesn't do, so
	// we skip the fill in that rare grazing case and still draw the grid lines
	// segment-by-segment below.
	c00x, c00y, ok0 := toScreen(mesh.Vec3{0, 0, 0})
	c10x, c10y, ok1 := toScreen(mesh.Vec3{float32(bedX), 0, 0})
	c11x, c11y, ok2 := toScreen(mesh.Vec3{float32(bedX), float32(bedY), 0})
	c01x, c01y, ok3 := toScreen(mesh.Vec3{0, float32(bedY), 0})
	if ok0 && ok1 && ok2 && ok3 {
		pr, pg, pb, pa := nrgbaToScale(e.PlateColor)
		verts := []ebiten.Vertex{
			{DstX: c00x, DstY: c00y, ColorR: pr, ColorG: pg, ColorB: pb, ColorA: pa},
			{DstX: c10x, DstY: c10y, ColorR: pr, ColorG: pg, ColorB: pb, ColorA: pa},
			{DstX: c11x, DstY: c11y, ColorR: pr, ColorG: pg, ColorB: pb, ColorA: pa},
			{DstX: c01x, DstY: c01y, ColorR: pr, ColorG: pg, ColorB: pb, ColorA: pa},
		}
		dst.DrawTriangles(verts, []uint16{0, 1, 2, 0, 2, 3}, getWhite(), &ebiten.DrawTrianglesOptions{})
	}

	// gridLine draws one bed-spanning line if both ends project in front.
	gridLine := func(ax, ay, bx, by float64, col color.NRGBA, width float32) {
		sax, say, oka := toScreen(mesh.Vec3{float32(ax), float32(ay), 0})
		sbx, sby, okb := toScreen(mesh.Vec3{float32(bx), float32(by), 0})
		if !oka || !okb {
			return
		}
		vector.StrokeLine(dst, sax, say, sbx, sby, width, col, true)
	}

	step := e.GridStep
	if step <= 0 {
		step = 10
	}
	// Lines parallel to Y (varying X), then parallel to X (varying Y). Every
	// fifth line is a major line: bolder and lighter, matching OrcaSlicer's
	// 50mm emphasis at a 10mm step.
	for i := 0; ; i++ {
		x := float64(i) * step
		if x > bedX {
			break
		}
		col, wdt := e.GridColor, float32(1)
		if i%5 == 0 {
			col, wdt = e.MajorColor, 1.4
		}
		gridLine(x, 0, x, bedY, col, wdt)
	}
	for j := 0; ; j++ {
		y := float64(j) * step
		if y > bedY {
			break
		}
		col, wdt := e.GridColor, float32(1)
		if j%5 == 0 {
			col, wdt = e.MajorColor, 1.4
		}
		gridLine(0, y, bedX, y, col, wdt)
	}

	// Origin axes drawn last so they sit on top of the grid: X red, Y green.
	gridLine(0, 0, bedX, 0, color.NRGBA{0xd0, 0x42, 0x42, 0xff}, 2)
	gridLine(0, 0, 0, bedY, color.NRGBA{0x46, 0xae, 0x57, 0xff}, 2)
}

// nrgbaToScale converts a straight-alpha NRGBA to the 0..1 per-channel scales
// ebiten.Vertex expects (it modulates the white source texture).
func nrgbaToScale(c color.NRGBA) (r, g, b, a float32) {
	return float32(c.R) / 255, float32(c.G) / 255, float32(c.B) / 255, float32(c.A) / 255
}
