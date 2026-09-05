package render

import (
	"image"
	"image/color"
	"sort"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/lestrrat-3d/osafune/internal/mesh"
)

// whiteImage is the 1×1 texture every triangle samples from. DrawTriangles
// requires a source image, so we point at this and modulate per-vertex with
// the Color* fields.
var (
	whiteOnce  sync.Once
	whiteImage *ebiten.Image
)

func getWhite() *ebiten.Image {
	whiteOnce.Do(func() {
		whiteImage = ebiten.NewImage(1, 1)
		whiteImage.Fill(color.White)
	})
	return whiteImage
}

// Rasterizer projects a mesh through a camera and draws it onto an
// ebiten.Image with a painter's algorithm. State (sorted indices, vertex
// buffers) is kept on the struct so a re-render does not reallocate.
type Rasterizer struct {
	// LightDir is the unit-length direction the light shines TOWARD. The
	// shading term is max(0, n · -LightDir) which matches the intuition
	// "the side facing the light is bright."
	LightDir mesh.Vec3
	// BaseColor is the diffuse colour of the mesh before shading. Values
	// are in 0..1 (sRGB).
	BaseColor [3]float32
	// AmbientFactor sits between 0 and 1; it keeps the unlit side of the
	// model from being totally black.
	AmbientFactor float32

	// Scratch buffers reused across frames.
	verts   []ebiten.Vertex
	indices []uint16
	sorted  []sortedTri
}

// sortedTri carries everything we need to draw one triangle in painter's
// order: indices into the vertex buffer and the depth key.
type sortedTri struct {
	v0, v1, v2 ebiten.Vertex
	avgZ       float32 // average view-space Z; more-negative = further away.
}

// New returns a Rasterizer pre-configured for the viewer. The default light
// is a generic "key light" coming from over the user's left shoulder. The
// ambient floor is fairly high so the shadow side of the model still shows
// geometry — for a viewer you almost always want readability over drama.
func New() *Rasterizer {
	return &Rasterizer{
		LightDir:      normalize(mesh.Vec3{-0.4, -0.5, -0.8}),
		BaseColor:     [3]float32{0.78, 0.80, 0.85},
		AmbientFactor: 0.6,
	}
}

// Draw projects every object in scene through cam into dstBounds and
// renders into dst. dst is expected to be a SubImage of the parent screen,
// but Ebitengine's DrawTriangles always interprets DstX/DstY in the atlas
// coordinate system, which is the same coordinate system dstBounds is in —
// so the projection step lays vertices out directly in screen pixels.
//
// scene may be nil (renders nothing). All objects share the same backface
// cull, painter sort and material; the painter pass runs over the union of
// every object's triangles so adjacent objects depth-sort against each
// other.
func (r *Rasterizer) Draw(dst *ebiten.Image, dstBounds image.Rectangle, scene *mesh.Scene, cam *Camera) {
	if scene == nil || scene.TriangleCount() == 0 {
		return
	}
	w := dstBounds.Dx()
	h := dstBounds.Dy()
	if w <= 0 || h <= 0 {
		return
	}
	aspect := float32(w) / float32(h)
	x0 := float32(dstBounds.Min.X)
	y0 := float32(dstBounds.Min.Y)
	fw := float32(w)
	fh := float32(h)

	r.sorted = r.sorted[:0]

	// Camera-space basis is the same for every triangle this frame; reuse it
	// for backface culling without recomputing inside the inner loop.
	eye, _, _, _ := cam.Basis()

	for oi := range scene.Objects {
		if scene.Objects[oi].Hidden {
			continue
		}
		for _, t := range scene.Objects[oi].Mesh.Triangles {
		// Backface cull in world space: a face is visible when its normal
		// points toward the eye. Use the midpoint as the surface reference;
		// for the small triangles in a typical mesh this is exact enough.
		mid := mesh.Vec3{
			(t.Vertices[0][0] + t.Vertices[1][0] + t.Vertices[2][0]) / 3,
			(t.Vertices[0][1] + t.Vertices[1][1] + t.Vertices[2][1]) / 3,
			(t.Vertices[0][2] + t.Vertices[1][2] + t.Vertices[2][2]) / 3,
		}
		toEye := mesh.Vec3{eye[0] - mid[0], eye[1] - mid[1], eye[2] - mid[2]}
		dotN := t.Normal[0]*toEye[0] + t.Normal[1]*toEye[1] + t.Normal[2]*toEye[2]
		if dotN <= 0 {
			continue
		}

		p0 := cam.Project(t.Vertices[0], aspect)
		p1 := cam.Project(t.Vertices[1], aspect)
		p2 := cam.Project(t.Vertices[2], aspect)
		// Any vertex behind the near plane: skip. A robust impl would clip
		// the triangle against the near plane; v1 trades that off because
		// the bounding-box fit keeps everything in front by default.
		if !p0.InFront || !p1.InFront || !p2.InFront {
			continue
		}

		// Lambertian shading: light from world toward LightDir, surface
		// normal in world space, with an ambient floor so the dark side is
		// readable.
		ndotL := -(t.Normal[0]*r.LightDir[0] + t.Normal[1]*r.LightDir[1] + t.Normal[2]*r.LightDir[2])
		if ndotL < 0 {
			ndotL = 0
		}
		shade := r.AmbientFactor + (1-r.AmbientFactor)*ndotL
		cr := r.BaseColor[0] * shade
		cg := r.BaseColor[1] * shade
		cb := r.BaseColor[2] * shade

		st := sortedTri{
			v0:   makeVertex(p0, x0, y0, fw, fh, cr, cg, cb),
			v1:   makeVertex(p1, x0, y0, fw, fh, cr, cg, cb),
			v2:   makeVertex(p2, x0, y0, fw, fh, cr, cg, cb),
			avgZ: (p0.ViewZ + p1.ViewZ + p2.ViewZ) / 3,
		}
		r.sorted = append(r.sorted, st)
		}
	}

	if len(r.sorted) == 0 {
		return
	}

	// Painter's algorithm: most-negative ViewZ first (furthest from camera).
	sort.Slice(r.sorted, func(i, j int) bool {
		return r.sorted[i].avgZ < r.sorted[j].avgZ
	})

	// Build packed vertex/index buffers. Each triangle contributes three
	// fresh vertices (no de-dup); index buffer is 0,1,2,3,4,5,...
	need := len(r.sorted) * 3
	if cap(r.verts) < need {
		r.verts = make([]ebiten.Vertex, need)
		r.indices = make([]uint16, need)
		for i := 0; i < need; i++ {
			r.indices[i] = uint16(i)
		}
	} else {
		r.verts = r.verts[:need]
		r.indices = r.indices[:need]
	}
	// Ensure indices are filled even on resize (only the prefix was set on
	// the previous shorter call).
	for i := range r.indices {
		r.indices[i] = uint16(i)
	}
	// uint16 indices put a hard cap at 65535/3 ≈ 21845 visible triangles per
	// DrawTriangles call; bigger meshes need batching. v1 ships without.
	if need > 65535 {
		need = (65535 / 3) * 3
		r.verts = r.verts[:need]
		r.indices = r.indices[:need]
		r.sorted = r.sorted[:need/3]
	}
	for i, t := range r.sorted {
		base := i * 3
		r.verts[base] = t.v0
		r.verts[base+1] = t.v1
		r.verts[base+2] = t.v2
	}

	op := &ebiten.DrawTrianglesOptions{}
	dst.DrawTriangles(r.verts, r.indices, getWhite(), op)
}

// makeVertex turns an NDC projection into an ebiten.Vertex anchored at the
// viewport's top-left corner. NDC is OpenGL-style (Y up); screen Y goes
// down, so we flip the Y axis here.
func makeVertex(p Projected, x0, y0, fw, fh, cr, cg, cb float32) ebiten.Vertex {
	sx := x0 + (p.X+1)*0.5*fw
	sy := y0 + (1-(p.Y+1)*0.5)*fh
	return ebiten.Vertex{
		DstX:   sx,
		DstY:   sy,
		SrcX:   0,
		SrcY:   0,
		ColorR: cr,
		ColorG: cg,
		ColorB: cb,
		ColorA: 1,
	}
}

