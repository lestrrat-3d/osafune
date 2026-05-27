package mesh

import "math"

// Axis identifies a world axis for rotation. The viewport's gizmo rings map
// to these directly.
type Axis int

const (
	AxisX Axis = iota
	AxisY
	AxisZ
)

// Rotate spins every vertex (and normal) of the mesh by angle radians about
// the line through center parallel to axis, then refreshes the bounds. Used
// by the viewport's rotate gizmo, which applies small incremental angles as
// the user drags. Normals rotate too (rotation preserves their length).
func (m *Mesh) Rotate(center Vec3, axis Axis, angle float64) {
	if angle == 0 || len(m.Triangles) == 0 {
		return
	}
	sin := float32(math.Sin(angle))
	cos := float32(math.Cos(angle))
	// The two axes that move under a rotation about `axis`, in the order that
	// makes (a,b) → (a·cos − b·sin, a·sin + b·cos) a positive (CCW) turn.
	a, b := rotPlane(axis)
	for ti := range m.Triangles {
		t := &m.Triangles[ti]
		for vi := range t.Vertices {
			rotateInPlane(&t.Vertices[vi], center, a, b, sin, cos)
		}
		// Normals carry no translation, so rotate them about a zero center.
		rotateInPlane(&t.Normal, Vec3{}, a, b, sin, cos)
	}
	m.recomputeBounds()
}

// ScaleUniform scales every vertex about center by f (1.0 = no change), then
// refreshes the bounds. Normals are unaffected by a uniform scale, so they
// are left as-is.
func (m *Mesh) ScaleUniform(center Vec3, f float64) {
	if f == 1 || f <= 0 || len(m.Triangles) == 0 {
		return
	}
	s := float32(f)
	for ti := range m.Triangles {
		for vi := range m.Triangles[ti].Vertices {
			v := &m.Triangles[ti].Vertices[vi]
			v[0] = center[0] + (v[0]-center[0])*s
			v[1] = center[1] + (v[1]-center[1])*s
			v[2] = center[2] + (v[2]-center[2])*s
		}
	}
	m.recomputeBounds()
}

// Translate shifts every vertex by v and refreshes the bounds.
func (m *Mesh) Translate(v Vec3) {
	if len(m.Triangles) == 0 {
		return
	}
	for ti := range m.Triangles {
		for vi := range m.Triangles[ti].Vertices {
			m.Triangles[ti].Vertices[vi][0] += v[0]
			m.Triangles[ti].Vertices[vi][1] += v[1]
			m.Triangles[ti].Vertices[vi][2] += v[2]
		}
	}
	m.Bounds.Min[0] += v[0]
	m.Bounds.Min[1] += v[1]
	m.Bounds.Min[2] += v[2]
	m.Bounds.Max[0] += v[0]
	m.Bounds.Max[1] += v[1]
	m.Bounds.Max[2] += v[2]
}

// DropToBed translates the mesh straight down (or up) so its lowest point
// sits on Z=0, the way a part rests on the build plate after being rotated.
func (m *Mesh) DropToBed() {
	if m.Bounds.Empty() {
		return
	}
	m.Translate(Vec3{0, 0, -m.Bounds.Min[2]})
}

// rotPlane returns the two axis indices that move under a rotation about the
// given axis, ordered so a positive angle is a right-handed (CCW) turn.
func rotPlane(axis Axis) (int, int) {
	switch axis {
	case AxisX:
		return 1, 2 // Y,Z
	case AxisY:
		return 2, 0 // Z,X
	default: // AxisZ
		return 0, 1 // X,Y
	}
}

// rotateInPlane rotates the (a,b) components of p about center by (sin,cos).
func rotateInPlane(p *Vec3, center Vec3, a, b int, sin, cos float32) {
	da := p[a] - center[a]
	db := p[b] - center[b]
	p[a] = center[a] + da*cos - db*sin
	p[b] = center[b] + da*sin + db*cos
}

// recomputeBounds rebuilds the mesh AABB from its vertices.
func (m *Mesh) recomputeBounds() {
	var bb AABB
	for ti := range m.Triangles {
		for vi := range m.Triangles[ti].Vertices {
			bb.Extend(m.Triangles[ti].Vertices[vi])
		}
	}
	m.Bounds = bb
}
