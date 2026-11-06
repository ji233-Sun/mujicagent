// Package view contains only native MyGo widgets and transient presentation state.
package view

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type View struct {
	C                 *controller.Controller
	Window            *mygo.Window
	Settings          bool
	section           string
	draft             string
	shelf             bool
	settledList       ui.ListState
	modelPicker       bool
	modelSearch       string
	modelList         ui.ListState
	permissionPicker  bool
	permissionTarget  string
	confirmFullAccess bool
	deleteID          string
	confirmDelete     bool
	expanded          map[string]*bool
	answers           map[string]map[string]string
	scroll            map[string]*ui.ScrollState
	md                markdown.Options
}

func New(c *controller.Controller) *View {
	return &View{C: c, section: "Agents", expanded: map[string]*bool{}, answers: map[string]map[string]string{}, scroll: map[string]*ui.ScrollState{}, md: markdown.Options{Theme: &markdown.Theme{FontSize: 14, BlockSpacing: 0.9}}}
}

func (v *View) theme(c *ui.Context) {
	t := ui.LightTheme()
	t.Background = ui.Hex("#f5f8f7")
	t.Surface = ui.Hex("#e9efed")
	t.SurfaceHover = ui.Hex("#dce7e3")
	t.Border = ui.Hex("#d5dfdb")
	t.Text = ui.Hex("#28413a")
	t.TextMuted = ui.Hex("#74877f")
	t.Accent = ui.Hex("#07896d")
	t.AccentText = ui.Hex("#ffffff")
	t.AccentHover = t.Accent
	t.AccentPressed = t.Accent
	t.Radius = 8
	t.FontSize = 13
	c.SetTheme(t)
}

func (v *View) Render(c *ui.Context) {
	tx := v.texts()
	v.C.Drain()
	for key := range v.answers {
		pending := false
		for _, r := range v.C.Requests {
			if string(r.ID) == key {
				pending = true
				break
			}
		}
		if !pending {
			delete(v.answers, key)
		}
	}
	v.theme(c)
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	}
	c.OnShortcut(ui.Cmd, ui.KeyN, func() { v.Settings = false; v.C.NewThread("") })
	c.OnShortcut(ui.Ctrl, ui.KeyN, func() { v.Settings = false; v.C.NewThread("") })
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		side := ui.Column(c).Width(246).Shrink(0).Padding(0, 12, 12)
		if !c.Vibrancy() {
			side.Background(c.Theme().Surface)
		}
		side.Children(func() {
			ui.Row(c).Height(54).Shrink(0).Padding(0, 4, 0, max(c.TitleBar().Left-12, 76)).AlignItems(ui.Center).Justify(ui.End).DragWindow().Children(func() {
				iconButton(c, tx.NewConversation(), plus, func() { v.Settings = false; v.C.NewThread("") })
			})
			v.sidebar(c)
		})
		ui.Column(c).Grow(1).MinWidth(0).Margin(8, 8, 8, 0).Radius(16).Border(1, c.Theme().Border).Background(c.Theme().Background).Clip().Children(func() {
			v.conversation(c)
		})
	})
	v.errorAlert(c)
	v.settingsDialog(c)
	if ui.AlertDialog(c, &v.confirmFullAccess, tx.FullAccessQuestion(), tx.FullAccessWarning(), tx.Cancel(), tx.EnableFullAccess()) == 1 && v.permissionTarget == v.C.State.Selected {
		v.C.SetPermission(":danger-full-access")
	}
	if t := v.C.State.Thread(v.deleteID); t != nil {
		if ui.AlertDialog(c, &v.confirmDelete, tx.DeleteConversation(), tx.DeleteDescription(v.threadTitle(t)), tx.Cancel(), tx.Delete()) == 1 {
			v.C.DeleteThread(t.ID)
		}
	}
}

func (v *View) sidebar(c *ui.Context) {
	tx := v.texts()
	ui.Text(c, "MujicAgent").Bold().FontSize(14).Padding(10, 10, 18)
	ui.Scroll(c.Key("active-threads")).Grow(1).MinHeight(0).Gap(12).Children(func() {
		v.group(c, "", "")
		for _, p := range v.C.State.Projects {
			v.group(c, p.ID, p.Name)
		}
	})
	v.settledShelf(c)
	ui.Row(c).Height(40).Shrink(0).AlignItems(ui.Center).Padding(8, 4, 0).Children(func() {
		iconButton(c, tx.Settings(), gear, func() { v.Settings = true })
		if v.C.AppUpdates.AvailableVersion != "" {
			iconButton(c, tx.UpdateAvailable(), downloadIcon, func() { v.section = "Updates"; v.Settings = true })
		}
		ui.Box(c).Grow(1)
		dot(c, v.C.Connection == "Connected")
		iconButton(c, tx.AddProject(), folderPlus, func() { v.pickProject() })
	})
}
func (v *View) group(c *ui.Context, id, title string) {
	ui.Column(c.Key("group-" + id)).Gap(2).Transition(ui.ElementTransition{Position: true, Duration: 220 * time.Millisecond}).Children(func() {
		if id != "" {
			ui.Row(c).Height(27).Padding(0, 8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(11).TextColor(c.Theme().TextMuted).Grow(1).SingleLine()
				iconButton(c, v.texts().NewInProject(title), plus, func() { v.C.NewThread(id) })
			})
		}
		for _, t := range v.C.State.Threads {
			if !t.Settled && t.ProjectID == id {
				v.threadRow(c, t, true)
			}
		}
	})
}
func (v *View) threadRow(c *ui.Context, t *model.Thread, animate bool) {
	tx := v.texts()
	title := v.threadTitle(t)
	row := ui.Row(c.Key(t.ID)).Height(34).Shrink(0).Radius(9).Padding(0, 4).Gap(3).AlignItems(ui.Center)
	if animate {
		row.Transition(ui.ElementTransition{Duration: 220 * time.Millisecond, Position: true, Colors: true,
			Enter: &ui.Motion{X: -24, Collapse: true}, Exit: &ui.Motion{X: -16}})
	} else {
		row.Transition(hoverTransition)
	}
	row.ContextMenu(func(m *ui.Menu) {
		label := tx.Settle()
		if t.Settled {
			label = tx.RestoreConversation()
		}
		if m.Item(label).Disabled(v.C.Running[t.ID]).Chosen() {
			v.C.Settle(t.ID, !t.Settled)
		}
		m.Separator()
		if m.Item(tx.Delete()).Disabled(v.C.Running[t.ID]).Chosen() {
			v.deleteID = t.ID
			v.confirmDelete = true
		}
	})
	selected := v.C.State.Selected == t.ID
	if selected {
		row.Background(c.Theme().SurfaceHover)
	} else if row.Hovered() {
		row.Background(c.Theme().Surface)
	}
	show := row.Hovered() || row.FocusWithin()
	row.Children(func() {
		button := ui.ButtonBase(c.Key("select")).Grow(1).MinWidth(0).Height(32).Gap(8).Padding(0, 6).AlignItems(ui.Center).Label(title).Tooltip(title)
		button.Children(func() {
			if v.C.Running[t.ID] {
				ui.Spinner(c).Size(14, 14)
			} else {
				ui.Icon(c, chat).Size(16, 16).TextColor(c.Theme().TextMuted)
			}
			ui.Text(c, title).SingleLine().Grow(1).MinWidth(0).FontSize(12)
		})
		button.OnClick(func() { v.C.Select(t.ID) })
		if show && !v.C.Running[t.ID] {
			label := tx.Settle()
			icon := settle
			if t.Settled {
				label = tx.RestoreConversation()
				icon = restore
			}
			iconButton(c, label, icon, func() { v.C.Settle(t.ID, !t.Settled) })
		}
	})
}

func (v *View) conversation(c *ui.Context) {
	tx := v.texts()
	t := v.C.State.Thread(v.C.State.Selected)
	title := v.threadTitle(t)
	ui.Row(c).Height(50).Shrink(0).Padding(0, 18).Gap(9).AlignItems(ui.Center).DragWindow().Children(func() {
		ui.Icon(c, chat).Size(18, 18).TextColor(c.Theme().TextMuted)
		ui.Text(c, title).Bold().SingleLine().Grow(1).MinWidth(0)
		if t != nil && t.Settled {
			ui.Button(c, tx.Restore()).OnClick(func() { v.C.Settle(t.ID, false) })
		}
	})
	ui.Divider(c)
	if t == nil || len(t.Turns) == 0 {
		ui.Column(c).Grow(1).Center().Gap(14).Children(func() {
			ui.Icon(c, spark).Size(36, 36).TextColor(c.Theme().TextMuted)
			ui.Text(c, tx.StartIdea()).FontSize(22).Bold()
			if v.C.Connection != "Connected" {
				ui.Button(c, tx.ConnectCodex()).OnClick(func() { v.Settings = true; v.section = "Agents" })
			}
		})
	} else {
		v.timeline(c, t)
	}
	v.composer(c, t)
}

func (v *View) timeline(c *ui.Context, t *model.Thread) {
	s := v.scroll[t.ID]
	if s == nil {
		s = &ui.ScrollState{Y: math.MaxFloat32}
		v.scroll[t.ID] = s
	}
	if s.Y >= s.MaxY-2 {
		s.Y = math.MaxFloat32
	}
	ui.Scroll(c.Key("timeline-"+t.ID)).Grow(1).TrackScroll(s).Padding(26, 28).Gap(30).Children(func() {
		for i := range t.Turns {
			turn := &t.Turns[i]
			ui.Column(c.Key(i)).Gap(20).MaxWidth(900).FillWidth().AlignSelf(ui.Center).Children(func() {
				ui.Column(c).AlignSelf(ui.End).MaxWidth(740).Radius(14).Background(c.Theme().Surface).Padding(12, 16).Children(func() { ui.Text(c, turn.Prompt).FontSize(14).Selectable() })
				ui.Column(c).Gap(14).Children(func() {
					for j := 0; j < len(turn.Items); {
						key := fmt.Sprintf("%s-%d-%d", t.ID, i, j)
						if turn.Items[j].Kind == "agentMessage" {
							v.item(c, &turn.Items[j], key, turn.Status == "inProgress")
							j++
							continue
						}
						start := j
						for j < len(turn.Items) && turn.Items[j].Kind != "agentMessage" {
							j++
						}
						v.activityGroup(c, turn.Items[start:j], key, turn.Status == "inProgress")
					}
					if turn.Status == "inProgress" {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() { ui.Spinner(c).Size(13, 13); ui.Text(c, "Codex").FontSize(12).TextColor(c.Theme().TextMuted) })
					}
					if turn.Error != "" {
						ui.Text(c, v.statusText(turn.Error)).FontSize(12).TextColor(c.Theme().Danger).Selectable()
					}
					if turn.Status == "interrupted" {
						ui.Text(c, v.texts().Stopped()).FontSize(11).TextColor(c.Theme().TextMuted)
					}
				})
			})
		}
	})
}
func (v *View) item(c *ui.Context, item *model.Item, key string, running bool) {
	tx := v.texts()
	if item.Kind == "agentMessage" {
		if running && item.Status != "completed" {
			markdown.Stream(c.Key(key), item.Text, &v.md)
		} else {
			markdown.View(c.Key(key), item.Text, &v.md)
		}
		return
	}
	label, icon := v.activityKind(item.Kind)
	if (item.Kind == "commandExecution" || item.Kind == "fileChange") && item.Text != "" {
		label = strings.SplitN(item.Text, "\n", 2)[0]
	}
	ui.Column(c.Key(key)).Children(func() {
		p := ui.CollapsibleBase(c, v.openState(key))
		p.Trigger.Padding(8, 10).Gap(8).AlignItems(ui.Center).Radius(7).Label(label).Tooltip(label).Transition(hoverTransition)
		if item.Kind == "reasoning" {
			p.Trigger.Label(tx.ReasoningSummary())
		}
		if p.Trigger.Hovered() {
			p.Trigger.Background(c.Theme().SurfaceHover)
		}
		p.Trigger.Children(func() {
			ui.Icon(c, chevron).Size(12, 12).Rotate(90 * p.Progress()).Shrink(0)
			ui.Icon(c, icon).Size(15, 15).Shrink(0)
			ui.Text(c, label).FontSize(12).SingleLine().Grow(1).MinWidth(0)
			if item.Status == "failed" {
				ui.Icon(c, failureIcon).Size(14, 14).TextColor(c.Theme().Danger).Tooltip(tx.Failed())
			}
		})
		p.Panel(func() {
			if item.Kind == "reasoning" && strings.TrimSpace(item.Text) == "" {
				label := tx.NoReasoningSummary()
				if running && item.Status != "completed" {
					label = tx.WaitingForReasoning()
				}
				ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
			} else if item.Kind == "commandExecution" || item.Kind == "fileChange" {
				ui.Text(c, item.Text).Font("monospace").FontSize(12).Selectable().Padding(10, 12).Background(c.Theme().Background).Radius(8)
			} else {
				markdown.View(c, item.Text, &v.md)
			}
		}).Padding(4, 12, 12)
	})
}

func (v *View) composer(c *ui.Context, t *model.Thread) {
	tx := v.texts()
	ui.Column(c).Shrink(0).Padding(8, 24, 16).Gap(8).Children(func() {
		for _, r := range v.C.Requests {
			if t != nil && r.ThreadID == t.ServerID {
				v.request(c, r)
			}
		}
		draft := &v.draft
		if t != nil {
			draft = &t.Draft
		}
		busy := t != nil && v.C.Running[t.ID]
		ui.Column(c).Radius(14).Border(1, c.Theme().Border).Background(c.Theme().Surface).Padding(12, 14, 8).Gap(8).Children(func() {
			ui.TextAreaBase(c.Key(v.C.State.Selected), draft).Lines(2, 7).Label(tx.Message()).Placeholder(tx.MessagePlaceholder()).FontSize(14).OnTextKey(func(k ui.TextKey) bool {
				if k.Key == ui.KeyEnter && k.Mods == 0 {
					if v.C.Send(k.Text(), false) {
						k.SetText("")
						v.draft = ""
					}
					return true
				}
				return false
			})
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				v.modelSelector(c)
				v.permissionSelector(c)
				ui.Box(c).Grow(1)
				if busy {
					iconButton(c, tx.Stop(), stopIcon, v.C.Interrupt)
				} else {
					ui.PrimaryButton(c, "").Label(tx.Send()).Tooltip(tx.SendHint()).Size(30, 30).Padding(0).Gap(0).Center().Radius(15).Disabled(strings.TrimSpace(*draft) == "" || v.C.Connection != "Connected").Children(func() { ui.Icon(c, arrow).Size(16, 16).Shrink(0) }).OnClick(func() {
						if v.C.Send(*draft, false) {
							v.draft = ""
						}
					})
				}
			})
		})
		ui.Row(c).Padding(0, 3).AlignItems(ui.Center).Children(func() {
			label := tx.NoProject()
			if t != nil && t.ProjectID != "" {
				for _, p := range v.C.State.Projects {
					if p.ID == t.ProjectID {
						label = p.Name
					}
				}
			}
			ui.Icon(c, folder).Size(14, 14).TextColor(c.Theme().TextMuted)
			ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted).Margin(0, 0, 0, 5)
			ui.Box(c).Grow(1)
			ui.Text(c, tx.NewlineHint()).FontSize(10).TextColor(c.Theme().TextMuted)
		})
	})
}

func (v *View) request(c *ui.Context, r *codex.Request) {
	tx := v.texts()
	key := string(r.ID)
	values := v.answers[key]
	if values == nil {
		values = map[string]string{}
		v.answers[key] = values
	}
	ui.Scroll(c.Key("request-"+key)).MaxHeight(300).Padding(14).Radius(12).Border(1, c.Theme().Accent).Gap(10).Children(func() {
		if r.Method == "item/tool/requestUserInput" {
			for _, q := range r.Questions {
				ui.Column(c.Key(q.ID)).Gap(6).Children(func() {
					ui.Text(c, q.Question).Bold()
					for _, o := range q.Options {
						b := ui.Button(c, o.Label).Tooltip(o.Description).Disabled(r.Responding)
						if values[q.ID] == o.Label {
							b.Background(c.Theme().SurfaceHover).Border(1, c.Theme().Accent)
						}
						b.OnClick(func() { values[q.ID] = o.Label })
					}
					value := values[q.ID]
					in := ui.TextInput(c, &value).Placeholder(tx.AnswerPlaceholder()).Label(q.ID).Disabled(r.Responding)
					if q.IsSecret {
						in.Password()
					}
					if in.Changed() {
						values[q.ID] = value
					}
				})
			}
			ui.PrimaryButton(c, tx.Continue()).Disabled(r.Responding).OnClick(func() { v.C.AnswerQuestions(r, values) })
		} else {
			label := tx.AllowCommand()
			if r.Method == "item/fileChange/requestApproval" {
				label = tx.AllowFileChanges()
			}
			ui.Text(c, label).Bold()
			if r.Command != "" {
				ui.Text(c, r.Command).Font("monospace").Selectable()
			}
			if r.GrantRoot != "" {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, folder).Size(15, 15)
					ui.Text(c, r.GrantRoot).Font("monospace").FontSize(12).Selectable().Grow(1)
				})
			}
			if r.Reason != "" {
				ui.Text(c, r.Reason).FontSize(12)
			}
			v.approvalOptions(c, r)
		}
	})
}

func (v *View) pickProject() {
	title := v.texts().AddProject()
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: v.Window, Title: title, Directory: true})
		if v.Window != nil {
			v.Window.Update(func() {
				if err != nil {
					v.C.ReportError(err.Error())
				} else if len(paths) > 0 {
					v.C.AddProject(paths[0])
					v.Settings = false
				}
			})
		}
	}()
}
func dot(c *ui.Context, connected bool) {
	color := c.Theme().TextMuted
	if connected {
		color = c.Theme().Accent
	}
	ui.Box(c).Size(5, 5).Radius(3).Background(color).Margin(0, 8)
}

var hoverTransition = ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond}

func iconButton(c *ui.Context, label string, icon *ui.SVG, fn func()) {
	b := ui.ButtonBase(c.Key(label)).Label(label).Tooltip(label).Size(26, 26).Radius(7).Center().TextColor(c.Theme().TextMuted)
	b.Transition(hoverTransition)
	if b.Hovered() {
		b.Background(c.Theme().SurfaceHover).TextColor(c.Theme().Text)
	}
	b.Children(func() { ui.Icon(c, icon).Size(16, 16) }).OnClick(fn)
}
