package render

import (
	"math"
	"runtime"
	"sync"

	"github.com/lestrrat-go/osafune/internal/mesh"
)

// ssaoKernel is a fixed two-ring set of unit-disk offsets the SSAO pass
// scatters around each pixel (scaled by the per-frame screen radius). A fixed
// low-discrepancy set — rather than per-pixel random jitter — keeps the
// occlusion stable so no temporal/de-noise pass is needed.
var ssaoKernel = [12][2]float32{
	{0.5, 0}, {0.25, 0.433}, {-0.25, 0.433}, {-0.5, 0}, {-0.25, -0.433}, {0.25, -0.433},
	{0.866, 0.5}, {0, 1}, {-0.866, 0.5}, {-0.866, -0.5}, {0, -1}, {0.866, -0.5},
}

const (
	// ssaoStrength scales the accumulated occlusion before it darkens a pixel.
	ssaoStrength = 1.3
	// ssaoBias ignores near-coplanar samples so flat surfaces don't self-occlude.
	ssaoBias = 0.03
	// ssaoFloor caps how dark AO can drive a pixel (0.5 = at most halved).
	ssaoFloor = 0.5
	// ssaoRadiusPx is the sampling radius in screen pixels at ss=1; it is
	// scaled by the supersample factor so the world coverage is constant.
	ssaoRadiusPx = 7.0
)

// ssao darkens d.rgba in concave regions (between beads, along the layer
// staircase, inside notches) using the per-pixel depth (d.zbuf) and world
// normal (d.nbuf) the rasterizer wrote. This is the screen-space ambient
// occlusion that gives the preview OrcaSlicer's "solid" look instead of flat
// candy stripes. It only runs on a full-resolution rebuild (skipped while the
// camera is dragging), so its cost is paid once per settle and then cached.
//
// w×h is the buffer resolution (the supersampled size when ss>1); ss scales
// the pixel sampling radius so the occluded world region stays constant
// regardless of supersampling. Bands read shared depth/normal buffers and
// write disjoint rgba pixels, so the parallelism is race-free.
func (d *ToolpathDrawer) ssao(w, h, ss int, vp *ViewProj) {
	radius := float32(ssaoRadiusPx * float64(ss))
	fw, fh := float32(w), float32(h)
	nw := runtime.NumCPU()
	band := (h + nw - 1) / nw
	var wg sync.WaitGroup
	for y0 := 0; y0 < h; y0 += band {
		y1 := y0 + band
		if y1 > h {
			y1 = h
		}
		wg.Add(1)
		go func(y0, y1 int) {
			defer wg.Done()
			d.ssaoBand(w, h, y0, y1, fw, fh, radius, vp)
		}(y0, y1)
	}
	wg.Wait()
}

func (d *ToolpathDrawer) ssaoBand(w, h, y0, y1 int, fw, fh, radius float32, vp *ViewProj) {
	neg := float32(math.Inf(-1))
	inv2fh := 2.0 / fh
	for y := y0; y < y1; y++ {
		row := y * w
		for x := 0; x < w; x++ {
			idx := row + x
			zc := d.zbuf[idx]
			if zc == neg {
				continue // background pixel — nothing to occlude
			}
			cx := float32(x) + 0.5
			cy := float32(y) + 0.5
			p := vp.Unproject(cx, cy, fw, fh, zc)
			ni := idx * 3
			n := mesh.Vec3{d.nbuf[ni], d.nbuf[ni+1], d.nbuf[ni+2]}

			// World size of one pixel at this depth — the occlusion radius
			// tracks screen appearance (closer surfaces get a larger world
			// radius), matching how GPU SSAO behaves.
			wpp := inv2fh * (-zc) / vp.fy
			rWorld := radius * wpp
			if rWorld <= 0 {
				continue
			}

			var occ float32
			for _, k := range ssaoKernel {
				sx := cx + k[0]*radius
				sy := cy + k[1]*radius
				ix := int(sx)
				iy := int(sy)
				if ix < 0 || ix >= w || iy < 0 || iy >= h {
					continue
				}
				sidx := iy*w + ix
				zs := d.zbuf[sidx]
				if zs == neg {
					continue
				}
				q := vp.Unproject(float32(ix)+0.5, float32(iy)+0.5, fw, fh, zs)
				dir := sub3(q, p)
				d2 := dot3(dir, dir)
				if d2 < 1e-10 {
					continue
				}
				dist := float32(math.Sqrt(float64(d2)))
				nd := dot3(n, scale3(dir, 1/dist))
				if nd <= ssaoBias {
					continue // sample is below the surface hemisphere
				}
				fall := 1 - dist/rWorld
				if fall <= 0 {
					continue // outside the occlusion radius
				}
				occ += (nd - ssaoBias) * fall
			}

			ao := 1 - ssaoStrength*occ/float32(len(ssaoKernel))
			if ao >= 1 {
				continue
			}
			if ao < ssaoFloor {
				ao = ssaoFloor
			}
			o := idx * 4
			d.rgba[o] = uint8(float32(d.rgba[o]) * ao)
			d.rgba[o+1] = uint8(float32(d.rgba[o+1]) * ao)
			d.rgba[o+2] = uint8(float32(d.rgba[o+2]) * ao)
		}
	}
}
