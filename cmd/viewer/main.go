// Command viewer is the osafune GUI mesh viewer. It loads STL or 3MF
// from a path supplied on the command line or via the Open dialog and
// displays it in an orbit-camera viewport.
package main

import (
	"flag"
	"fmt"
	"image"
	"log/slog"
	"os"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/guigui-gui/guigui"
	// Pull in the bundled CJK font so object names and any other UTF-8
	// strings containing CJK characters (3MF files from Chinese/Japanese/
	// Korean slicer software routinely have these) render properly. The
	// package self-registers via init().
	_ "github.com/guigui-gui/guigui/basicwidget/cjkfont"

	"github.com/lestrrat-go/osafune/internal/ui"
)

func main() {
	flag.Parse()
	var initial string
	if flag.NArg() > 0 {
		initial = flag.Arg(0)
	}

	root := ui.NewRoot(ui.NativeOpener{}, ui.NativeSaver{}, initial)

	opts := &guigui.RunOptions{
		Title:         "osafune viewer",
		WindowSize:    image.Pt(1280, 800),
		WindowMinSize: image.Pt(640, 480),
		RunGameOptions: &ebiten.RunGameOptions{
			ApplePressAndHoldEnabled: true,
		},
	}
	if err := guigui.Run(root, opts); err != nil {
		slog.Error("guigui exited with error", "err", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
