package slice

import (
	"math"

	"github.com/lestrrat-go/osafune/internal/config"
)

// GenerateSkirtBrim prepends the first-layer adhesion loops to the layer.
// Both are concentric closed loops obtained by offsetting the layer's filled
// regions outward (a negative offset distance grows the region):
//
//   - Brim: loops fused to the object, starting half a line-width outside the
//     outer wall and stepping outward one line-width at a time until BrimWidth
//     is covered. They add bed-contact area to fight warping.
//   - Skirt: free-standing loops around the whole footprint, beyond any brim,
//     at SkirtDistance. They prime the nozzle and let the user check first-
//     layer flow before the model starts.
//
// The loops are prepended so the whole adhesion group prints before the model
// (the skirt primes; the brim must lay down before the wall it bonds to).
// [OptimizeTravel] later chains them nearest-first within their single
// Skirt/Brim role run, which stays first. lineWidth/speed are the first
// layer's. Only meaningful on layer 0; a layer with no contours is a no-op.
func GenerateSkirtBrim(layer *Layer, process *config.Process, lineWidth, speed float64) {
	if len(layer.Contours) == 0 || lineWidth <= 0 {
		return
	}

	var loops []Path

	// Skirt first so it heads the adhesion group (priming before anything).
	if process.SkirtLoops > 0 {
		base := process.SkirtDistance + process.BrimWidth + lineWidth*0.5
		for i := 0; i < process.SkirtLoops; i++ {
			d := -(base + float64(i)*lineWidth) // negative = outward
			loops = appendOuterLoops(loops, offsetRegions(layer.Contours, d), lineWidth, speed)
		}
	}

	// Brim: hug the object, stepping outward from the wall.
	if process.BrimWidth > 0 {
		n := int(math.Round(process.BrimWidth / lineWidth))
		for i := 0; i < n; i++ {
			d := -(lineWidth*0.5 + float64(i)*lineWidth) // negative = outward
			loops = appendOuterLoops(loops, offsetRegions(layer.Contours, d), lineWidth, speed)
		}
	}

	if len(loops) == 0 {
		return
	}
	layer.Paths = append(loops, layer.Paths...)
}

// appendOuterLoops adds the outer ring of each region as a Skirt/Brim closed
// path. Only outer rings are used: adhesion loops surround the part, they
// don't reach into its internal holes.
func appendOuterLoops(dst []Path, regions []ExPolygon, width, speed float64) []Path {
	for _, r := range regions {
		if len(r.Outer) < 3 {
			continue
		}
		pts := make([]Point2, len(r.Outer))
		copy(pts, r.Outer)
		dst = append(dst, Path{
			Points: pts,
			Role:   RoleSkirtBrim,
			Width:  width,
			Speed:  speed,
			Closed: true,
		})
	}
	return dst
}
