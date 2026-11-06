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
		if !v.C.Running[t.ID] {
			ui.Button(c, tx.QueueResume()).AlignSelf(ui.End).FontSize(11).OnClick(func() { v.C.ResumeQueue(t.ID) })
		}
		ui.Scroll(c.Key("queue-list")).MaxHeight(156).Gap(5).Children(func() {
			for i, q := range t.Queue {
				row := ui.Row(c.Key(q.ID)).Height(44).Shrink(0).Radius(8).Border(1, c.Theme().Border).Background(ui.Hex("#ffffff")).Padding(0, 8).Gap(8).AlignItems(ui.Center)
				if !q.Sending {
					row.Drag(queueDrag{t.ID, q.ID})
				}
				if row.Dragging() {
					row.Opacity(0.35)
				}
				edit := v.C.QueueEdit(t.ID)
				editing := edit != nil && edit.ID == q.ID
				if editing {
					row.Border(1, c.Theme().Accent).Background(ui.Hex("#e0efe8")).Transition(hoverTransition)
				}
				var indicator int
				if drag, ok := ui.DragOver[queueDrag](row); ok && drag.Thread == t.ID && drag.ID != q.ID {
					row.Border(2, c.Theme().Accent).Background(ui.Hex("#e0efe8"))
					for j, source := range t.Queue {
						if source.ID == drag.ID {
							if j < i {
								indicator = 1
							} else {
								indicator = -1
							}
						}
					}
				}
				if drag, ok := ui.Drop[queueDrag](row); ok && drag.Thread == t.ID {
					moved, target = drag.ID, q.ID
				}
				row.Children(func() {
					if indicator != 0 {
						line := ui.Box(c).Absolute().Left(0).Right(0).Height(3).Background(c.Theme().Accent)
						if indicator < 0 {
							line.Top(0)
						} else {
							line.Bottom(0)
						}
					}
					ui.Text(c, "⠿").FontSize(18).TextColor(c.Theme().TextMuted).Tooltip(tx.QueueDrag())
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
					iconButton(c, tx.QueueEdit(), fileEditIcon, func() {
						if v.C.EditQueued(t.ID, q.ID) {
							v.editorHandle.Focus(c)
						}
					}).Disabled(q.Sending || edit != nil)
					iconButton(c, tx.Delete(), closeIcon, func() { v.C.DeleteQueued(t.ID, q.ID) }).Disabled(q.Sending)
					ui.Button(c, label).FontSize(11).TextColor(c.Theme().Accent).Tooltip(tx.QueueSteerHint()).Disabled(q.Sending || editing || !v.C.CanSteer(t)).OnClick(func() { v.C.Steer(t.ID, q.ID) })
				})
			}
		})
	})
	if moved != "" {
		v.C.MoveQueued(t.ID, moved, target)
	}
}
