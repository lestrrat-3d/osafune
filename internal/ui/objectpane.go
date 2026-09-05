package ui

import (
	"slices"

	"github.com/guigui-gui/guigui"
	"github.com/guigui-gui/guigui/basicwidget"

	"github.com/lestrrat-3d/osafune/internal/mesh"
)

// ObjectPane is the left-hand side panel that lists the objects in the
// currently loaded [mesh.Scene]. Each row has a checkbox that toggles the
// object's visibility in the viewport.
type ObjectPane struct {
	guigui.DefaultWidget

	panel basicwidget.Panel
	body  guigui.WidgetWithSize[*objectPaneBody]
}

// SetScene refreshes the pane to reflect scene. onChange is invoked whenever
// the user toggles an object's visibility checkbox so the owner (Root) can
// redraw the viewport. Either argument may be nil.
func (p *ObjectPane) SetScene(scene *mesh.Scene, onChange func()) {
	body := p.body.Widget()
	body.scene = scene
	body.onChange = onChange
}

func (p *ObjectPane) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	adder.AddWidget(&p.panel)
	p.panel.SetStyle(basicwidget.PanelStyleSide)
	p.panel.SetBorders(basicwidget.PanelBorders{End: true})
	p.panel.SetContent(&p.body)
	return nil
}

func (p *ObjectPane) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	p.body.SetFixedSize(widgetBounds.Bounds().Size())
	layouter.LayoutWidget(&p.panel, widgetBounds.Bounds())
}

// objectPaneBody is the panel interior: a "Objects" header label followed by
// one row per object. Rows are recycled across rebuilds via [guigui.WidgetSlice]
// so per-row state (e.g. a checkbox press animation) survives a scene
// change as long as the row count doesn't shrink.
type objectPaneBody struct {
	guigui.DefaultWidget

	header   basicwidget.Text
	rows     guigui.WidgetSlice[*objectRow]
	scene    *mesh.Scene
	onChange func()

	layoutItems []guigui.LinearLayoutItem
}

func (b *objectPaneBody) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	adder.AddWidget(&b.header)
	b.header.SetValue("Objects")
	b.header.SetBold(true)

	n := 0
	if b.scene != nil {
		n = len(b.scene.Objects)
	}
	b.rows.SetLen(n)
	for i := 0; i < n; i++ {
		row := b.rows.At(i)
		row.bind(b.scene, i, b.onChange)
		adder.AddWidget(row)
	}
	return nil
}

func (b *objectPaneBody) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	u := basicwidget.UnitSize(context)
	bounds := widgetBounds.Bounds()
	bounds.Min.X += u / 2
	bounds.Max.X -= u / 2
	bounds.Min.Y += u / 2
	bounds.Max.Y -= u / 2

	b.layoutItems = slices.Delete(b.layoutItems, 0, len(b.layoutItems))
	b.layoutItems = append(b.layoutItems, guigui.LinearLayoutItem{
		Widget: &b.header,
		Size:   guigui.FixedSize(u + u/2),
	})
	for i := 0; i < b.rows.Len(); i++ {
		b.layoutItems = append(b.layoutItems, guigui.LinearLayoutItem{
			Widget: b.rows.At(i),
			Size:   guigui.FixedSize(u + u/4),
		})
	}
	(guigui.LinearLayout{
		Direction: guigui.LayoutDirectionVertical,
		Items:     b.layoutItems,
		Gap:       u / 4,
	}).LayoutWidgets(context, bounds, layouter)
}

// objectRow is one object's visibility checkbox + name label.
type objectRow struct {
	guigui.DefaultWidget

	check basicwidget.Checkbox
	label basicwidget.Text

	scene    *mesh.Scene
	index    int
	onChange func()

	items []guigui.LinearLayoutItem
}

func (r *objectRow) bind(scene *mesh.Scene, index int, onChange func()) {
	r.scene = scene
	r.index = index
	r.onChange = onChange
}

func (r *objectRow) Build(context *guigui.Context, adder *guigui.ChildAdder) error {
	adder.AddWidget(&r.check)
	adder.AddWidget(&r.label)

	if r.scene != nil && r.index < len(r.scene.Objects) {
		obj := &r.scene.Objects[r.index]
		r.check.SetValue(!obj.Hidden)
		r.label.SetValue(obj.Name)
	}
	r.check.OnValueChanged(func(context *guigui.Context, value bool) {
		if r.scene == nil || r.index >= len(r.scene.Objects) {
			return
		}
		r.scene.Objects[r.index].Hidden = !value
		if r.onChange != nil {
			r.onChange()
		}
	})
	return nil
}

func (r *objectRow) Layout(context *guigui.Context, widgetBounds *guigui.WidgetBounds, layouter *guigui.ChildLayouter) {
	u := basicwidget.UnitSize(context)
	r.items = slices.Delete(r.items, 0, len(r.items))
	r.items = append(r.items,
		guigui.LinearLayoutItem{Widget: &r.check, Size: guigui.FixedSize(u + u/4)},
		guigui.LinearLayoutItem{Widget: &r.label, Size: guigui.FlexibleSize(1)},
	)
	(guigui.LinearLayout{
		Direction: guigui.LayoutDirectionHorizontal,
		Items:     r.items,
		Gap:       u / 4,
	}).LayoutWidgets(context, widgetBounds.Bounds(), layouter)
}
