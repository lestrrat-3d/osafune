package slice_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-go/osafune/internal/config"
	"github.com/lestrrat-go/osafune/internal/slice"
)

func rectEx(x0, y0, x1, y1 float64) slice.ExPolygon {
	return slice.ExPolygon{Outer: slice.Polygon{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}}}
}

// overhangStack: a 10×10 column (layers 0-2) with a wide 0..30 × 0..10 slab
// stacked on top (layers 3-5). The slab's x>10 portion overhangs air and
// needs support beneath it; the column itself is self-supporting.
func overhangStack() []slice.Layer {
	const h = 1.0
	var ls []slice.Layer
	for i := 0; i < 3; i++ {
		ls = append(ls, slice.Layer{Index: i, Z: float64(i+1) * h, Height: h, Contours: []slice.ExPolygon{rectEx(0, 0, 10, 10)}})
	}
	for i := 3; i < 6; i++ {
		ls = append(ls, slice.Layer{Index: i, Z: float64(i+1) * h, Height: h, Contours: []slice.ExPolygon{rectEx(0, 0, 30, 10)}})
	}
	return ls
}

func countSupport(paths []slice.Path) int {
	n := 0
	for _, p := range paths {
		if p.Role == slice.RoleSupport {
			n++
		}
	}
	return n
}

func TestGenerateSupportsUnderOverhang(t *testing.T) {
	t.Parallel()
	proc := config.DefaultProcess()
	proc.SupportEnable = true
	proc.SupportThreshold = 50
	proc.SupportBranchDiameter = 2

	layers := overhangStack()
	out := slice.GenerateSupports(layers, &proc)
	require.Len(t, out, len(layers))

	// Support pillars hold up the overhang from the layers below it.
	total := 0
	for _, ps := range out {
		total += countSupport(ps)
	}
	require.Greater(t, total, 0, "overhang must generate support")
	require.Greater(t, countSupport(out[0])+countSupport(out[1])+countSupport(out[2]), 0,
		"support exists in the layers beneath the overhang")

	// Every support pillar sits under the overhanging part (x>10), never under
	// the self-supporting column (x in 0..10).
	for li, ps := range out {
		for _, p := range ps {
			var cx float64
			for _, pt := range p.Points {
				cx += pt.X
			}
			cx /= float64(len(p.Points))
			require.Greater(t, cx, 10.0, "support on layer %d must be under the overhang, not the column", li)
		}
	}
}

func TestGenerateSupportsNoneWithoutOverhang(t *testing.T) {
	t.Parallel()
	proc := config.DefaultProcess()
	proc.SupportEnable = true
	// A plain vertical column overhangs nothing.
	var layers []slice.Layer
	for i := 0; i < 6; i++ {
		layers = append(layers, slice.Layer{Index: i, Z: float64(i+1) * 0.2, Height: 0.2, Contours: []slice.ExPolygon{rectEx(0, 0, 10, 10)}})
	}
	out := slice.GenerateSupports(layers, &proc)
	total := 0
	for _, ps := range out {
		total += countSupport(ps)
	}
	require.Equal(t, 0, total, "a vertical column needs no support")
}
