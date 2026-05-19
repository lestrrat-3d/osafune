// Package mesh holds the in-memory geometry used by the viewer and any
// downstream slicer stages. The viewer ingests STL and 3MF; both are
// normalized to a flat list of [Triangle]s in millimetres with Z-up.
package mesh

import "math"

// Vec3 is a single-precision 3D vector. STL stores coordinates as float32
// natively, and the rasterizer projects in single precision, so float32 is
// the common currency throughout the viewer.
type Vec3 [3]float32

// Triangle is one face of a [Mesh]. Normal is unit-length and points along
// the outward surface; it is filled in by the loader so the renderer never
// has to recompute it.
type Triangle struct {
	Vertices [3]Vec3
	Normal   Vec3
}

// AABB is an axis-aligned bounding box. It is the empty/unset box when
// Min and Max are both their zero values; use [AABB.Empty] to test.
type AABB struct {
	Min, Max Vec3
}

// Empty reports whether the box has zero or negative extent on any axis.
func (b AABB) Empty() bool {
	return b.Max[0] <= b.Min[0] || b.Max[1] <= b.Min[1] || b.Max[2] <= b.Min[2]
}

// Center returns the geometric centre of the box.
func (b AABB) Center() Vec3 {
	return Vec3{
		(b.Min[0] + b.Max[0]) * 0.5,
		(b.Min[1] + b.Max[1]) * 0.5,
		(b.Min[2] + b.Max[2]) * 0.5,
	}
}

// Diagonal returns the length of the box's diagonal. Useful for fitting a
// camera to a model regardless of its overall scale.
func (b AABB) Diagonal() float32 {
	dx := b.Max[0] - b.Min[0]
	dy := b.Max[1] - b.Min[1]
	dz := b.Max[2] - b.Min[2]
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

// Extend grows b so it contains v. The first call from a zero-value AABB
// initialises it to a degenerate box at v; callers should track whether
// any extension has happened if they need to distinguish "empty" from
// "single-point".
func (b *AABB) Extend(v Vec3) {
	if b.Min == (Vec3{}) && b.Max == (Vec3{}) {
		b.Min = v
		b.Max = v
		return
	}
	for i := 0; i < 3; i++ {
		if v[i] < b.Min[i] {
			b.Min[i] = v[i]
		}
		if v[i] > b.Max[i] {
			b.Max[i] = v[i]
		}
	}
}

// Mesh is a flat list of triangles plus the precomputed bounding box.
//
// The viewer does not deduplicate vertices: STL never shares them and 3MF
// shares them at the file level, but the rasterizer projects per-triangle
// anyway, so the flat layout is what the hot path wants.
type Mesh struct {
	Triangles []Triangle
	Bounds    AABB
}

// Object is a named [Mesh] inside a [Scene]. STL files become a single Object
// named after the file; 3MF build items each become one Object so the viewer
// can list them separately.
//
// Hidden objects are still part of the scene's bounds (the camera frames the
// same way whether or not a part is currently visible) but are skipped by
// the renderer.
type Object struct {
	Name    string
	Mesh    Mesh
	Hidden  bool
}

// Scene is the unit the loader produces and the viewer renders. Objects keep
// their own bounds; SceneBounds returns the union for camera framing.
type Scene struct {
	Objects []Object
}

// Bounds returns the combined AABB of every object in the scene. The empty
// AABB is returned when the scene has no triangles.
func (s *Scene) Bounds() AABB {
	var b AABB
	first := true
	for i := range s.Objects {
		ob := s.Objects[i].Mesh.Bounds
		if ob.Empty() {
			continue
		}
		if first {
			b = ob
			first = false
			continue
		}
		for ax := 0; ax < 3; ax++ {
			if ob.Min[ax] < b.Min[ax] {
				b.Min[ax] = ob.Min[ax]
			}
			if ob.Max[ax] > b.Max[ax] {
				b.Max[ax] = ob.Max[ax]
			}
		}
	}
	return b
}

// TriangleCount returns the total number of triangles across all objects.
func (s *Scene) TriangleCount() int {
	n := 0
	for i := range s.Objects {
		n += len(s.Objects[i].Mesh.Triangles)
	}
	return n
}
