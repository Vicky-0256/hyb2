package views

import "github.com/egoist/mygo/ui"

// The spacing, type and radius scale every page uses. Use these instead of
// literals so cards, gaps and titles line up from page to page.
const (
	Space1 float32 = 4
	Space2 float32 = 8
	Space3 float32 = 12
	Space4 float32 = 16
	Space6 float32 = 24

	FontSmall float32 = 12 // captions, labels, table cells
	FontBody  float32 = 14
	FontTitle float32 = 16 // card titles
	FontPage  float32 = 24 // page titles

	RadiusSmall float32 = 6  // tiles, notices, inline boxes
	RadiusLarge float32 = 10 // cards

	// FieldMinWidth is the narrowest a control in a FieldGrid gets before
	// the grid drops a column.
	FieldMinWidth float32 = 160
)

// PageRoot is the outer column of a page: the same padding and section gap
// everywhere.
func PageRoot(c *ui.Context) ui.Element {
	return ui.Column(c).Fill().Padding(Space6).Gap(Space4)
}

// FieldGrid lays form controls out in equal columns, as many as fit at
// FieldMinWidth, so the controls of every row line up and fill the width.
// Give each child FillWidth (or no Width) so it fills its cell.
func FieldGrid(c *ui.Context, width float32, body func()) ui.Element {
	cols := int(width / (FieldMinWidth + Space3))
	cols = max(1, min(cols, 6))
	return ui.Grid(c).Columns(cols).Gap(Space3).Children(body)
}

// StatTile is a label over a value, on a tinted box. Put tiles in a
// TileGrid so they share one width.
func StatTile(c *ui.Context, label, value string, color ui.Color) ui.Element {
	t := c.Theme()
	return ui.Column(c).Gap(2).Padding(Space2, Space3).Radius(RadiusSmall).Background(t.Surface).MinWidth(0).Children(func() {
		ui.Text(c, label).FontSize(FontSmall).TextColor(t.TextMuted).SingleLine()
		ui.Text(c, value).FontSize(FontTitle).Bold().TextColor(color)
	})
}

// TileGrid lays StatTiles out in n equal columns.
func TileGrid(c *ui.Context, n int, body func()) ui.Element {
	return ui.Grid(c).Columns(max(n, 1)).Gap(Space2).Children(body)
}

// Notice is a short message on a tinted box: tone is t.Warning, t.Accent,
// t.Danger or similar.
func Notice(c *ui.Context, text string, tone ui.Color) ui.Element {
	return ui.Text(c, text).FontSize(FontSmall).Padding(Space2, Space3).Radius(RadiusSmall).Background(tone.Alpha(0.10))
}
