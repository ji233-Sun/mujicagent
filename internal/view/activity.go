package view

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func (v *View) openState(key string) *bool {
	if v.expanded[key] == nil {
		v.expanded[key] = new(bool)
	}
	return v.expanded[key]
}

func (v *View) activityKind(kind string) (string, *ui.SVG) {
	tx := v.texts()
	switch kind {
	case "commandExecution":
		return tx.Commands(), terminalIcon
	case "fileChange":
		return tx.FileChanges(), fileEditIcon
	case "reasoning":
		return tx.Reasoning(), thinkingIcon
	case "webSearch":
		return tx.Search(), searchIcon
	case "plan":
		return tx.Plan(), planIcon
	case "contextCompaction":
		return tx.Compaction(), activityIcon
	case "mcpToolCall":
		return tx.McpToolCall(), activityIcon
	default:
		return kind, activityIcon
	}
}

// Adjacent process items share one disclosure; assistant messages remain outside it.
func (v *View) activityGroup(c *ui.Context, items []model.Item, key string, running bool) {
	counts := map[string]int{}
	var kinds, labels []string
	failed, active := 0, false
	for _, item := range items {
		if counts[item.Kind] == 0 {
			kinds = append(kinds, item.Kind)
		}
		counts[item.Kind]++
		if item.Status == "failed" {
			failed++
		}
		if item.Status == "inProgress" {
			active = running
		}
	}
	for _, kind := range kinds {
		label, _ := v.activityKind(kind)
		labels = append(labels, fmt.Sprintf("%s × %d", label, counts[kind]))
	}
	summary := strings.Join(labels, " · ")
	ui.Column(c.Key("activity-"+key)).Radius(10).Border(1, c.Theme().Border).Background(c.Theme().Surface).Children(func() {
		p := ui.CollapsibleBase(c, v.openState("activity-"+key))
		p.Trigger.Padding(10, 12).Gap(10).AlignItems(ui.Center).Radius(10).Label(summary).Tooltip(summary).Transition(hoverTransition)
		if p.Trigger.Hovered() {
			p.Trigger.Background(c.Theme().SurfaceHover)
		}
		p.Trigger.Children(func() {
			ui.Icon(c, chevron).Size(14, 14).Rotate(90 * p.Progress()).Shrink(0).TextColor(c.Theme().TextMuted)
			ui.Row(c).Grow(1).MinWidth(0).Gap(12).Wrap().Children(func() {
				for _, kind := range kinds {
					label, icon := v.activityKind(kind)
					ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icon).Size(14, 14).TextColor(c.Theme().TextMuted).Shrink(0)
						ui.Text(c, label).FontSize(12)
						ui.Textf(c, "× %d", counts[kind]).FontSize(12).ContentTransition(ui.ContentTransition{Numeric: true})
					})
				}
			})
			if failed > 0 {
				ui.Row(c).Gap(4).AlignItems(ui.Center).TextColor(c.Theme().Danger).Tooltip(v.texts().Failed()).Children(func() {
					ui.Icon(c, failureIcon).Size(14, 14)
					ui.Textf(c, "%d", failed).FontSize(11)
				})
			}
			if active {
				ui.Spinner(c).Size(13, 13)
			}
		})
		p.Panel(func() {
			ui.Divider(c).Margin(0, 8, 4)
			for i := range items {
				v.item(c, &items[i], fmt.Sprintf("%s-%d", key, i), running)
			}
		}).Padding(0, 4, 4)
	})
}
