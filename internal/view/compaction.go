package view

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func timelineStandalone(kind string) bool {
	return kind == "agentMessage" || kind == "steerMessage" || kind == "contextCompaction"
}

func compactionRunning(turn *model.Turn) bool {
	for _, item := range turn.Items {
		if item.Kind == "contextCompaction" && (item.Status == "inProgress" || item.Status == "") {
			return true
		}
	}
	return false
}

func compactDecimal(value float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(value, 'f', 1, 64), ".0")
}

func compactTokens(value int64) string {
	if value < 1000 {
		return strconv.FormatInt(value, 10)
	}
	return compactDecimal(float64(value)/1000) + "k"
}

func (v *View) compactionLabel(item *model.Item, running bool) (label, hint string, active bool) {
	tx := v.texts()
	reason := item.Text
	if reason == "" && item.MCP != nil {
		reason = item.MCP.Error
	}
	switch item.Status {
	case "completed":
		s := item.Compaction
		if s.BeforeKnown && s.AfterKnown && s.Before > 0 && s.After >= 0 {
			percent := compactDecimal((float64(s.Before) - float64(s.After)) / float64(s.Before) * 100)
			return tx.CompactionResult(compactTokens(s.Before), compactTokens(s.After), percent), tx.CompactionExact(strconv.FormatInt(s.Before, 10), strconv.FormatInt(s.After, 10)), false
		}
		return tx.CompactionCompleted(), tx.CompactionUnavailable(), false
	case "failed":
		return tx.CompactionFailed(), reason, false
	case "interrupted", "cancelled":
		return tx.CompactionCancelled(), reason, false
	case "", "inProgress":
		if running {
			return tx.CompactionRunning(), "", true
		}
	}
	return tx.CompactionUnknown(), item.Text, false
}

func (v *View) compactionDivider(c *ui.Context, item *model.Item, key string, running bool) {
	label, hint, active := v.compactionLabel(item, running)
	ui.Row(c.Key(key)).FillWidth().MinWidth(0).Gap(10).Padding(8, 0).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Height(1).Grow(1).MinWidth(8).Background(c.Theme().Border)
		ui.Row(c).MinWidth(0).Shrink(1).Gap(6).AlignItems(ui.Center).Tooltip(hint).Children(func() {
			if active {
				ui.Spinner(c).Size(12, 12).Shrink(0)
			}
			ui.Text(c, label).MinWidth(0).Shrink(1).FontSize(12).TextAlign(ui.Center).TextColor(c.Theme().TextMuted)
		})
		ui.Box(c).Height(1).Grow(1).MinWidth(8).Background(c.Theme().Border)
	})
}
