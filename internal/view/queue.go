package view

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type queueDrag struct{ Thread, ID string }

func (v *View) messageQueue(c *ui.Context, t *model.Thread) {
	if t == nil || len(t.Queue) == 0 {
		return
	}
	tx := v.texts()
	var moved, target string
	ui.Column(c.Key("queue-"+t.ID)).Radius(12).Border(1, c.Theme().Border).Background(ui.Hex("#f0f5f2")).Padding(12).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, spark).Size(14, 14).TextColor(c.Theme().Accent)
			ui.Text(c, tx.QueueTitle()).Bold().FontSize(12)
			ui.Text(c, fmt.Sprint(len(t.Queue))).FontSize(10).TextColor(c.Theme().Accent).Background(ui.Hex("#dcebe3")).Padding(2, 6).Radius(8)
			ui.Box(c).Grow(1)
			if !v.C.Running[t.ID] {
				ui.Button(c, tx.QueueResume()).FontSize(11).OnClick(func() { v.C.ResumeQueue(t.ID) })
			}
		})
		hint := tx.QueueHint()
		if !v.C.Running[t.ID] {
			hint = tx.QueuePaused()
		}
		ui.Text(c, hint).FontSize(10).TextColor(c.Theme().TextMuted)
		ui.Scroll(c.Key("queue-list")).MaxHeight(156).Gap(5).Children(func() {
			for i, q := range t.Queue {
				row := ui.Row(c.Key(q.ID)).Height(44).Shrink(0).Radius(8).Border(1, c.Theme().Border).Background(ui.Hex("#ffffff")).Padding(0, 8).Gap(8).AlignItems(ui.Center)
				if drag, ok := ui.DragOver[queueDrag](row); ok && drag.Thread == t.ID && drag.ID != q.ID {
					row.Border(2, c.Theme().Accent).Background(ui.Hex("#e0efe8"))
				}
				if drag, ok := ui.Drop[queueDrag](row); ok && drag.Thread == t.ID {
					moved, target = drag.ID, q.ID
				}
				row.Children(func() {
					ui.Text(c, "⠿").FontSize(18).TextColor(c.Theme().TextMuted).Drag(queueDrag{t.ID, q.ID}).Tooltip(tx.QueueDrag())
					ui.Text(c, fmt.Sprintf("%02d", i+1)).FontSize(10).TextColor(c.Theme().TextMuted)
					var preview strings.Builder
					for _, part := range model.MessageParts(q.Text) {
						if part.Attachment != nil {
							preview.WriteString(filepath.Base(part.Attachment.Path))
						} else {
							preview.WriteString(part.Text)
						}
					}
					text := strings.Join(strings.Fields(preview.String()), " ")
					ui.Text(c, text).SingleLine().Grow(1).MinWidth(0).FontSize(12).Tooltip(text)
					label := tx.QueueSteer()
					if q.Sending {
						label = tx.QueueSending()
					}
					ui.Button(c, label).FontSize(11).TextColor(c.Theme().Accent).Tooltip(tx.QueueSteerHint()).Disabled(q.Sending || !v.C.CanSteer(t)).OnClick(func() { v.C.Steer(t.ID, q.ID) })
				})
			}
		})
	})
	if moved != "" {
		v.C.MoveQueued(t.ID, moved, target)
	}
}
