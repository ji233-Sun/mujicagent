package view

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func (v *View) viewedImage(item *model.Item) model.Attachment {
	path := item.Text
	if path != "" && !filepath.IsAbs(path) {
		if thread := v.C.State.Thread(v.C.State.Selected); thread != nil && thread.Cwd != "" {
			path = filepath.Join(thread.Cwd, path)
		}
	}
	return model.Attachment{Kind: "image", Path: path}
}

func (v *View) imageView(c *ui.Context, item *model.Item) {
	tx := v.texts()
	a := v.viewedImage(item)
	ui.Column(c).Gap(8).FillWidth().Children(func() {
		if a.Path == "" {
			ui.Text(c, tx.NoImagePreview()).FontSize(12).TextColor(c.Theme().TextMuted)
			return
		}
		ui.Text(c, a.Path).FontSize(12).Selectable()
		p := v.preview(a)
		switch {
		case !p.ready:
			ui.Text(c, tx.AttachmentLoading()).FontSize(12).TextColor(c.Theme().TextMuted)
		case p.err != nil || p.bitmap == nil:
			ui.Text(c, tx.NoImagePreview()).FontSize(12).TextColor(c.Theme().TextMuted)
		default:
			ui.ButtonBase(c.Key("viewed-image")).Width(280).MaxWidthPercent(100).Height(160).
				Radius(8).Border(1, c.Theme().Border).Background(c.Theme().Surface).
				Label(tx.ImagePreview()).Tooltip(a.Path).Children(func() {
				ui.Image(c, p.bitmap).Fill().Fit(ui.Contain)
			}).OnClick(func() { v.openAttachment(a, "") })
		}
	})
}
