package gcode_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lestrrat-go/osafune/internal/config"
	"github.com/lestrrat-go/osafune/internal/slice"
)

// TestBridgeFanBoost checks the fan ramps up over a bridge and back down
// after. FanSpeed is set below BridgeFanSpeed so the boost is observable.
func TestBridgeFanBoost(t *testing.T) {
	t.Parallel()
	fil := config.DefaultFilament()
	fil.FanSpeed = 128
	fil.BridgeFanSpeed = 255

	// Layer 1 (fan already on): an inner-wall path, then a bridge, then more
	// wall — so the fan must boost for the bridge and restore afterwards.
	layers := []slice.Layer{
		{Index: 0, Z: 0.2, Height: 0.2, Paths: []slice.Path{wall(slice.RolePerimeter, slice.Point2{X: 0, Y: 0}, slice.Point2{X: 10, Y: 0})}},
		{Index: 1, Z: 0.4, Height: 0.2, Paths: []slice.Path{
			wall(slice.RolePerimeter, slice.Point2{X: 0, Y: 0}, slice.Point2{X: 10, Y: 0}),
			wall(slice.RoleBridge, slice.Point2{X: 0, Y: 1}, slice.Point2{X: 10, Y: 1}),
			wall(slice.RolePerimeter, slice.Point2{X: 0, Y: 2}, slice.Point2{X: 10, Y: 2}),
		}},
	}
	out := emit(t, config.DefaultPrinter(), fil, config.DefaultProcess(), layers)
	body := printBody(out)

	boost := strings.Index(body, "M106 S255 ; fan")
	bridge := strings.Index(body, ";TYPE:Bridge infill")
	restore := strings.LastIndex(body, "M106 S128 ; fan")
	require.GreaterOrEqual(t, boost, 0, "fan boosts to BridgeFanSpeed over the bridge")
	require.GreaterOrEqual(t, bridge, 0, "bridge type marker present")
	require.Greater(t, boost, bridge-40, "boost emitted around the bridge block")
	require.Greater(t, restore, boost, "fan restored to layer speed after the bridge")
}
