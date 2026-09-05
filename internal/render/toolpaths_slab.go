package render

import (
	"math"
	"runtime"
	"sync"

	"github.com/lestrrat-go/polyclip"
	"github.com/lestrrat-go/polyclip/geom"

	"github.com/lestrrat-3d/osafune/internal/mesh"
	"github.com/lestrrat-3d/osafune/internal/slice"
)

// wtri is one camera-independent world-space triangle of the solid wall
// shell: three world points, an outward normal (for per-frame back-face
// culling), and a colour already shaded by that normal (shading is
// view-independent, so it is baked once when the shell is built).
type wtri struct {
	a, b, c mesh.Vec3
	n       mesh.Vec3
	col     uint32
}

// buildWorldSlab builds the solid wall shell for layers: each layer's filled
// cross-section ([slice.Layer.Contours]) becomes a prism — triangulated top
// and bottom caps plus swept side walls — in the outer-wall colour. Stacking
// the prisms reproduces the model's exterior; the z-buffer then resolves the
// staircase on curved surfaces (a higher layer's cap occludes the part of
// the layer below it covers, leaving each layer's exposed top ring solid).
// The result is camera-independent and cached, so only projection runs per
// frame.
func (d *ToolpathDrawer) buildWorldSlab(layers []slice.Layer, topCut, botCut bool) {
	wall := RoleColor(slice.RoleExternalPerimeter)
	light := d.LightDir
	fill := d.FillDir
	amb := d.AmbientFactor
	wr, wg, wb := float32(wall.R), float32(wall.G), float32(wall.B)
	shade := func(n mesh.Vec3) uint32 {
		return shadePacked(wr, wg, wb, n, light, fill, amb)
	}
	up := mesh.Vec3{0, 0, 1}
	down := mesh.Vec3{0, 0, -1}
	colUp, colDown := shade(up), shade(down)
	cutaway := topCut || botCut

	out := d.worldSlab[:0]
	for li := range layers {
		l := &layers[li]
		if len(l.Contours) == 0 {
			continue
		}
		layerH := float32(l.Height)
		if layerH < minLayerHeight {
			layerH = minLayerHeight
		}
		z1 := float32(l.Z)      // top of layer
		z0 := z1 - layerH       // bottom of layer

		// Caps fill a layer's whole cross-section solid. That is what makes
		// the closed exterior read as a solid top/bottom — but in a cutaway
		// every layer's cap would stack into a solid slab the beads sit on,
		// so the sparse infill appears to float on a solid wall-coloured
		// floor. So caps are built only for the un-cut (closed) view; in a
		// cutaway the side walls enclose the sides and the per-layer beads
		// (drawn separately) supply the genuine cross-section instead.
		if !cutaway {
			for _, tr := range triangulateContours(l.Contours) {
				ax, ay := float32(tr[0].X), float32(tr[0].Y)
				bx, by := float32(tr[1].X), float32(tr[1].Y)
				cx, cy := float32(tr[2].X), float32(tr[2].Y)
				out = append(out, wtri{mesh.Vec3{ax, ay, z1}, mesh.Vec3{bx, by, z1}, mesh.Vec3{cx, cy, z1}, up, colUp})       // top cap +Z
				out = append(out, wtri{mesh.Vec3{ax, ay, z0}, mesh.Vec3{cx, cy, z0}, mesh.Vec3{bx, by, z0}, down, colDown}) // bottom cap -Z
			}
		}

		// Side walls: sweep every ring edge into a vertical quad. Outer
		// rings are CCW and holes CW, so the right-hand edge normal points
		// out of the solid in both cases.
		for _, ex := range l.Contours {
			out = sweepRing(out, ex.Outer, z0, z1, shade)
			for _, h := range ex.Holes {
				out = sweepRing(out, h, z0, z1, shade)
			}
		}
	}
	d.worldSlab = out
}

// sweepRing appends the side-wall quads (two triangles each) for one ring
// swept from z0 to z1, with the outward-facing normal per edge.
func sweepRing(out []wtri, ring slice.Polygon, z0, z1 float32, shade func(mesh.Vec3) uint32) []wtri {
	n := len(ring)
	if n < 3 {
		return out
	}
	for i := 0; i < n; i++ {
		p := ring[i]
		q := ring[(i+1)%n]
		px, py := float32(p.X), float32(p.Y)
		qx, qy := float32(q.X), float32(q.Y)
		// Right-hand normal of edge p→q, horizontal: (dy, -dx).
		nx, ny := qy-py, -(qx - px)
		l := float32(math.Hypot(float64(nx), float64(ny)))
		if l < 1e-9 {
			continue
		}
		nrm := mesh.Vec3{nx / l, ny / l, 0}
		col := shade(nrm)
		p0 := mesh.Vec3{px, py, z0}
		q0 := mesh.Vec3{qx, qy, z0}
		q1 := mesh.Vec3{qx, qy, z1}
		p1 := mesh.Vec3{px, py, z1}
		out = append(out, wtri{p0, q0, q1, nrm, col}, wtri{p0, q1, p1, nrm, col})
	}
	return out
}

// triangulateContours triangulates a layer's filled cross-section. It is
// guarded against the triangulator panicking on a degenerate contour: on
// failure the layer simply contributes no caps (its side walls still draw),
// which at worst leaves that one layer's top ring see-through rather than
// crashing.
func triangulateContours(cs []slice.ExPolygon) (tris []polyclip.Triangle) {
	defer func() {
		if recover() != nil {
			tris = nil
		}
	}()
	m := make(geom.MultiPolygon, 0, len(cs))
	for _, e := range cs {
		if len(e.Outer) < 3 {
			continue
		}
		ex := geom.ExPolygon{Outer: contourToGeom(e.Outer)}
		for _, h := range e.Holes {
			if len(h) >= 3 {
				ex.Holes = append(ex.Holes, contourToGeom(h))
			}
		}
		m = append(m, ex)
	}
	if len(m) == 0 {
		return nil
	}
	return polyclip.Triangulate(m)
}

func contourToGeom(p slice.Polygon) geom.Polygon {
	out := make(geom.Polygon, len(p))
	for i, pt := range p {
		out[i] = geom.Point{X: pt.X, Y: pt.Y}
	}
	return out
}

// projectSlab projects the cached world-space shell into d.tris for the
// current camera, back-face culling against the eye. Parallelized over the
// triangle list since each triangle is independent.
func (d *ToolpathDrawer) projectSlab(w, h int, vp *ViewProj) {
	eye := vp.Eye()
	fw, fh := float32(w), float32(h)
	src := d.worldSlab
	nw := runtime.NumCPU()
	if nw > 1 && len(src) >= 4096 {
		if len(d.slabBufs) != nw {
			d.slabBufs = make([][]rtri, nw)
		}
		chunk := (len(src) + nw - 1) / nw
		var wg sync.WaitGroup
		for wi := 0; wi < nw; wi++ {
			lo := wi * chunk
			hi := lo + chunk
			if hi > len(src) {
				hi = len(src)
			}
			if lo >= hi {
				d.slabBufs[wi] = d.slabBufs[wi][:0]
				continue
			}
			wg.Add(1)
			go func(wi, lo, hi int) {
				defer wg.Done()
				d.slabBufs[wi] = projectSlabRange(d.slabBufs[wi][:0], src[lo:hi], vp, eye, fw, fh)
			}(wi, lo, hi)
		}
		wg.Wait()
		d.tris = d.tris[:0]
		for wi := 0; wi < nw; wi++ {
			d.tris = append(d.tris, d.slabBufs[wi]...)
		}
		return
	}
	d.tris = projectSlabRange(d.tris[:0], src, vp, eye, fw, fh)
}

func projectSlabRange(dst []rtri, src []wtri, vp *ViewProj, eye mesh.Vec3, fw, fh float32) []rtri {
	for ti := range src {
		t := &src[ti]
		ctr := scale3(add3(add3(t.a, t.b), t.c), 1.0/3)
		if dot3(t.n, sub3(ctr, eye)) > 0 {
			continue // back face
		}
		pa := vp.Project(t.a)
		pb := vp.Project(t.b)
		pc := vp.Project(t.c)
		if !pa.InFront || !pb.InFront || !pc.InFront {
			continue
		}
		dst = append(dst, rtri{
			(pa.X + 1) * 0.5 * fw, (1 - (pa.Y+1)*0.5) * fh, pa.ViewZ,
			(pb.X + 1) * 0.5 * fw, (1 - (pb.Y+1)*0.5) * fh, pb.ViewZ,
			(pc.X + 1) * 0.5 * fw, (1 - (pc.Y+1)*0.5) * fh, pc.ViewZ,
			t.n[0], t.n[1], t.n[2],
			t.col,
		})
	}
	return dst
}
