package ui

import (
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/guigui-gui/guigui"
	"github.com/guigui-gui/guigui/basicwidget"

	"github.com/lestrrat-go/makislicer/internal/mesh"
)

// FileOpener selects a path interactively. The Root widget calls it from the
// Open button so we can swap implementations (native dialog, mock for tests,
// drag-and-drop later) without touching the widget tree.
type FileOpener interface {
	Pick() (path string, ok bool)
}

// Root is the top-level guigui widget: a toolbar above a (left-pane + 3D
// viewport) row.
type Root struct {
	guigui.DefaultWidget

	background  basicwidget.Background
	openButton  basicwidget.Button
	resetButton basicwidget.Button
	objectPane  ObjectPane
	viewport    *Viewport
	opener      FileOpener

	// pendingPath, when non-empty, is a path queued for loading on the next
	// Build pass. Setting it from Build (e.g. from the Open button's OnUp)
	// would cause the load to run during widget construction, which both
	// blocks UI updates and risks reentrancy; instead we capture the path
	// here and pull it through Build.
	pendingMu   sync.Mutex
	pendingPath string

	toolbarItems []guigui.LinearLayoutItem
	bodyItems    []guigui.LinearLayoutItem
	rootItems    []guigui.LinearLayoutItem

	// initialPath is the file given on the command line. It is consumed by
	// the first Build call.
	initialPath string
}

// NewRoot constructs the application root. opener is invoked when the user
// clicks "Open"; pass nil to hide the Open button.
func NewRoot(opener FileOpener, initialPath string) *Root {
	return &Root{
		viewport:    NewViewport(),
		opener:      opener,
		initialPath: initialPath,
	}
}

func (r *Root) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	adder.AddWidget(&r.background)
	adder.AddWidget(&r.openButton)
	adder.AddWidget(&r.resetButton)
	adder.AddWidget(&r.objectPane)
	adder.AddWidget(r.viewport)

	r.openButton.SetText("Open…")
	r.openButton.OnUp(func(context *guigui.Context) {
		if r.opener == nil {
			return
		}
		path, ok := r.opener.Pick()
		if !ok {
			return
		}
		r.queueLoad(path)
	})
	context.SetEnabled(&r.openButton, r.opener != nil)

	r.resetButton.SetText("Reset View")
	r.resetButton.OnUp(func(context *guigui.Context) {
		r.viewport.ResetView()
	})

	// Consume any pending path queued by OnUp / startup.
	if r.initialPath != "" {
		r.queueLoad(r.initialPath)
		r.initialPath = ""
	}
	r.flushPending()

	return nil
}

func (r *Root) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	bounds := widgetBounds.Bounds()
	layouter.LayoutWidget(&r.background, bounds)

	u := basicwidget.UnitSize(context)

	r.toolbarItems = slices.Delete(r.toolbarItems, 0, len(r.toolbarItems))
	r.toolbarItems = append(r.toolbarItems,
		guigui.LinearLayoutItem{Widget: &r.openButton, Size: guigui.FixedSize(5 * u)},
		guigui.LinearLayoutItem{Widget: &r.resetButton, Size: guigui.FixedSize(6 * u)},
		guigui.LinearLayoutItem{Size: guigui.FlexibleSize(1)},
	)
	toolbar := guigui.LinearLayout{
		Direction: guigui.LayoutDirectionHorizontal,
		Items:     r.toolbarItems,
		Gap:       u / 2,
	}

	r.bodyItems = slices.Delete(r.bodyItems, 0, len(r.bodyItems))
	r.bodyItems = append(r.bodyItems,
		guigui.LinearLayoutItem{Widget: &r.objectPane, Size: guigui.FixedSize(10 * u)},
		guigui.LinearLayoutItem{Widget: r.viewport, Size: guigui.FlexibleSize(1)},
	)
	body := guigui.LinearLayout{
		Direction: guigui.LayoutDirectionHorizontal,
		Items:     r.bodyItems,
	}

	r.rootItems = slices.Delete(r.rootItems, 0, len(r.rootItems))
	r.rootItems = append(r.rootItems,
		guigui.LinearLayoutItem{Size: guigui.FixedSize(2 * u), Layout: &toolbar},
		guigui.LinearLayoutItem{Size: guigui.FlexibleSize(1), Layout: &body},
	)
	(guigui.LinearLayout{
		Direction: guigui.LayoutDirectionVertical,
		Items:     r.rootItems,
		Gap:       u / 2,
		Padding:   guigui.Padding{Start: u / 2, Top: u / 2, End: u / 2, Bottom: u / 2},
	}).LayoutWidgets(context, bounds, layouter)
}

func (r *Root) queueLoad(path string) {
	r.pendingMu.Lock()
	r.pendingPath = path
	r.pendingMu.Unlock()
	guigui.RequestRebuild(r)
}

func (r *Root) flushPending() {
	r.pendingMu.Lock()
	path := r.pendingPath
	r.pendingPath = ""
	r.pendingMu.Unlock()
	if path == "" {
		return
	}
	scene, err := mesh.LoadFile(path)
	if err != nil {
		slog.Error("load failed", "path", path, "err", err)
		return
	}
	b := scene.Bounds()
	slog.Info("loaded scene",
		"path", path,
		"objects", len(scene.Objects),
		"triangles", scene.TriangleCount(),
		"bounds_min", fmt.Sprint(b.Min),
		"bounds_max", fmt.Sprint(b.Max),
	)
	r.viewport.SetScene(scene)
	r.objectPane.SetScene(scene, func() {
		// Object visibility toggled — viewport has a stale frame.
		guigui.RequestRedraw(r.viewport)
	})
}
