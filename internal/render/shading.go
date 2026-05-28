package render

import "github.com/lestrrat-go/osafune/internal/mesh"

// shadeFactor returns the lighting multiplier for a surface with world normal
// n under a key light and a dimmer fill light from another direction, with an
// ambient floor amb. key/fill are unit directions the light shines *toward*
// (so the lit side is -dot(n, dir)).
//
// The key term uses a half-Lambert wrap (kd*0.5+0.5, squared) instead of a
// hard max(0, kd): it never drops fully to the ambient floor, so curved
// surfaces shade gradually rather than snapping to flat-dark on the terminator
// — the soft, "solid" look OrcaSlicer's GL shading has versus the previous
// hard Lambert. The fill light lifts the side facing away from the key so the
// shadowed half stays legible without washing the whole model out.
func shadeFactor(n, key, fill mesh.Vec3, amb float32) float32 {
	kd := -dot3(n, key)
	wrap := kd*0.5 + 0.5
	if wrap < 0 {
		wrap = 0
	}
	keyTerm := wrap * wrap

	fd := -dot3(n, fill)
	if fd < 0 {
		fd = 0
	}

	return amb + (1-amb)*keyTerm + 0.18*fd
}

// shadePacked applies shadeFactor to a base colour (channels in 0..255) and
// returns it packed as 0xRRGGBB, the form both the cached wall shell and the
// per-bead boxes store in their screen triangles.
func shadePacked(r, g, b float32, n, key, fill mesh.Vec3, amb float32) uint32 {
	s := shadeFactor(n, key, fill, amb)
	return clampByte(r*s)<<16 | clampByte(g*s)<<8 | clampByte(b*s)
}

// clampByte rounds v into the 0..255 range as a uint32 channel.
func clampByte(v float32) uint32 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint32(v)
}
