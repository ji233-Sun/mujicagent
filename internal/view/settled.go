package view

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

const threadHeight = 34
const settleDuration = 220 * time.Millisecond

// Keep outgoing rows in the virtual list until their visual exit completes.
func (v *View) settleProgress(c *ui.Context, id string) float32 {
	end, ok := v.settling[id]
	if !ok {
		return 1
	}
	remaining := max(0, min(1, float32(end.Sub(c.Now()))/float32(settleDuration)))
	return remaining * remaining * remaining
}

func (v *View) settledShelf(c *ui.Context) {
	tx := v.texts()
	threads := make([]*model.Thread, 0)
	for _, t := range v.C.State.Threads {
		if t.Settled {
			threads = append(threads, t)
		}
	}
	// Only the visible rows construct text, menus and controls. The model stays unchanged.
	height := float32(min(5, len(threads)) * threadHeight)
	target := float32(0)
	if v.shelf {
		target = height
	}
	container := ui.Column(c.Key("settled-shelf")).Shrink(0)
	shown := container.Animate("height", target, 240*time.Millisecond)
	container.Height(36 + shown).Children(func() {
		ui.Box(c).Height(1).Shrink(0).Margin(0, 8, 3).Background(c.Theme().Border)
		header := ui.ButtonBase(c.Key("settled-toggle")).Height(32).Shrink(0).Padding(0, 8).Gap(7).
			AlignItems(ui.Center).Justify(ui.Start).Label(tx.Settled()).Tooltip(tx.SettledConversations()).Transition(hoverTransition)
		if header.Hovered() {
			header.Background(c.Theme().SurfaceHover).Radius(7)
		}
		header.Children(func() {
			angle := float32(-90)
			if v.shelf {
				angle = 90
			}
			icon := ui.Icon(c, chevron).Size(14, 14).TextColor(c.Theme().TextMuted)
			icon.Rotate(icon.Animate("rotation", angle, 200*time.Millisecond))
			ui.Text(c, tx.Settled()).FontSize(12).TextColor(c.Theme().TextMuted).Grow(1)
			ui.Textf(c, "%d", len(threads)).FontSize(11).TextColor(c.Theme().TextMuted).ContentTransition(ui.ContentTransition{Numeric: true})
		})
		header.OnClick(func() { v.shelf = !v.shelf })
		ui.Column(c.Key("settled-clip")).Height(shown).Shrink(0).Clip().Children(func() {
			if shown > 0 && len(threads) > 0 {
				ui.List(c.Key("settled-threads"), &v.settledList, len(threads)).Height(height).Shrink(0).Label(tx.SettledConversations()).
					ItemKey(func(i int) any { return threads[i].ID }).Rows(func(row ui.ListRow) { v.threadRow(row.Context, threads[row.Index], false) })
			}
		})
	})
}
