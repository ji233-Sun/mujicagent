// Package view contains only native MyGo widgets and transient presentation state.
package view

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/richtext"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type View struct {
	C                           *controller.Controller
	Window                      *mygo.Window
	Settings                    bool
	section                     string
	agentSection, pickerHarness string
	draft                       string
	shelf                       bool
	settledList                 ui.ListState
	settling                    map[string]time.Time
	projectLists                map[string]*ui.ListState
	projectPicker               bool
	projectSearch               string
	projectList                 ui.ListState
	sidebarHandles              map[string]*ui.Handle
	projectFlight               *projectFlight
	modelPicker                 bool
	modelSearch                 string
	modelList                   ui.ListState
	permissionPicker            bool
	permissionTarget            string
	confirmFullAccess           bool
	defaultPermissionHarness    string
	defaultPermissionChoices    map[string]*string
	deleteID                    string
	confirmDelete               bool
	harnessDialog               bool
	harnessTarget               string
	harnessSource               string
	expanded                    map[string]*bool
	answers                     map[string]map[string]string
	scroll                      map[string]*ui.ScrollState
	md                          markdown.Options
	attachments                 map[string]*attachmentPreview
	attachmentOpen              bool
	attachment                  model.Attachment
	attachmentThread            string
	annotation                  *annotationEditor
	attachmentQueue             string
	editor                      *richtext.Document
	editorHandle                ui.Handle
	composerMenu                *mygo.Menu
	composerMenuLanguage        string
	editorSource, editorThread  string
	editorQueue                 string
}

func New(c *controller.Controller) *View {
	return &View{C: c, section: "Agents", projectLists: map[string]*ui.ListState{}, expanded: map[string]*bool{}, answers: map[string]map[string]string{}, scroll: map[string]*ui.ScrollState{}, md: markdown.Options{Theme: &markdown.Theme{FontSize: 14, BlockSpacing: 0.9}}}
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
	for id, end := range v.settling {
		thread := v.C.State.Thread(id)
		if thread == nil || !thread.Settled || !c.Now().Before(end) || c.Preferences().ReduceMotion {
			delete(v.settling, id)
		} else {
			c.AnimationFrame()
		}
	}
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
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		side := ui.Column(c).Width(246).Shrink(0).Padding(0, 12, 12)
		if !c.Vibrancy() {
			side.Background(c.Theme().Surface)
		}
		side.Children(func() {
			ui.Row(c).Height(54).Shrink(0).Padding(0, 4, 0, max(c.TitleBar().Left-12, 76)).AlignItems(ui.Center).Justify(ui.End).DragWindow()
			v.sidebar(c)
		})
		ui.Column(c).Grow(1).MinWidth(0).Margin(8, 8, 8, 0).Radius(16).Border(1, c.Theme().Border).Background(c.Theme().Background).Clip().Children(func() {
			v.conversation(c)
		})
	})
	v.drawProjectFlight(c)
	v.errorAlert(c)
	v.settingsDialog(c)
	v.harnessInstallDialog(c)
	v.attachmentDialog(c)
	warning := tx.FullAccessWarning()
	if v.defaultPermissionHarness != "" {
		warning = tx.FullAccessDefaultWarning()
	}
	fullAccessResult := ui.AlertDialog(c, &v.confirmFullAccess, tx.FullAccessQuestion(), warning, tx.Cancel(), tx.EnableFullAccess())
	if fullAccessResult == 1 && v.defaultPermissionHarness != "" {
		*v.agentDefaultAccess(v.defaultPermissionHarness) = agentFullAccess(v.defaultPermissionHarness)
		if choice := v.defaultPermissionChoices[v.defaultPermissionHarness]; choice != nil {
			*choice = agentFullAccess(v.defaultPermissionHarness)
		}
		v.C.Save()
	} else if fullAccessResult == 1 && v.permissionTarget == v.C.State.Selected {
		if t := v.C.State.Thread(v.permissionTarget); t != nil && t.Driver() != "codex" {
			t.AgentAccess = agentFullAccess(t.Driver())
			v.C.Save()
		} else {
			v.C.SetPermission(":danger-full-access")
		}
	}
	if fullAccessResult != -1 {
		v.defaultPermissionHarness = ""
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
		v.group(c, model.Project{Name: tx.NoProject(), Expanded: v.C.State.NoProjectExpanded})
		for _, p := range v.C.State.Projects {
			v.group(c, p)
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
func (v *View) group(c *ui.Context, p model.Project) {
	ui.Column(c.Key("group-" + p.ID)).Shrink(0).Gap(2).Children(func() {
		header := ui.Row(c.Key("header")).Bind(v.sidebarHandle("project-" + p.ID)).Height(32).Shrink(0).AlignItems(ui.Center).Radius(7).Transition(hoverTransition)
		hovered := header.Hovered()
		if hovered {
			header.Background(c.Theme().SurfaceHover)
		}
		header.Children(func() {
			ui.ButtonBase(c.Key("toggle")).Fill().Padding(0, 8).Gap(8).AlignItems(ui.Center).
				Label(p.Name).Tooltip(p.Path).Expanded(p.Expanded).Children(func() {
				icon := folder
				if p.ID == "" {
					icon = noProjectFolder
				}
				ui.Icon(c, icon).Size(16, 16).Shrink(0).TextColor(c.Theme().TextMuted)
				ui.Text(c, p.Name).FontSize(12).Grow(1).MinWidth(0).SingleLine()
				ui.Box(c).Width(26).Shrink(0)
				angle := float32(0)
				if p.Expanded {
					angle = 90
				}
				arrow := ui.Icon(c, chevron).Size(14, 14).Shrink(0).TextColor(c.Theme().TextMuted)
				arrow.Rotate(arrow.Animate("rotation", angle, 180*time.Millisecond))
			}).OnClick(func() { v.C.SetProjectExpanded(p.ID, !p.Expanded) })
			if hovered {
				iconButton(c, v.texts().NewInProject(p.Name), plus, func() {
					v.C.NewThread(p.ID)
					v.C.SetProjectExpanded(p.ID, true)
					if list := v.projectLists[p.ID]; list != nil {
						list.ScrollTo(0, ui.Start)
					}
				}).Absolute().Top(3).Right(28)
			}
		})
		if !p.Expanded {
			return
		}
		threads := make([]*model.Thread, 0)
		height := float32(0)
		for _, t := range v.C.State.Threads {
			_, leaving := v.settling[t.ID]
			if (!t.Settled || leaving) && t.ProjectID == p.ID {
				threads = append(threads, t)
				height += threadHeight * v.settleProgress(c, t.ID)
			}
		}
		if len(threads) == 0 {
			return
		}
		// Keep blank conversations first, then drafts, preserving order within each group.
		rank := func(t *model.Thread) int {
			if len(t.Turns) != 0 {
				return 2
			}
			if strings.TrimSpace(t.Draft) != "" {
				return 1
			}
			return 0
		}
		sort.SliceStable(threads, func(i, j int) bool { return rank(threads[i]) < rank(threads[j]) })
		list := v.projectLists[p.ID]
		if list == nil {
			list = &ui.ListState{}
			v.projectLists[p.ID] = list
		}
		ui.List(c.Key("threads"), list, len(threads)).Height(min(10*threadHeight, height)).Shrink(0).Margin(0, 0, 0, 14).Label(p.Name).
			ItemKey(func(i int) any { return threads[i].ID }).Rows(func(row ui.ListRow) { v.threadRow(row.Context, threads[row.Index], true) })
	})
}
func (v *View) threadRow(c *ui.Context, t *model.Thread, activeList bool) {
	tx := v.texts()
	title := v.threadTitle(t)
	row := ui.Row(c.Key(t.ID)).Height(34).Shrink(0).Radius(9).Padding(0, 4).Gap(3).AlignItems(ui.Center)
	row.Transition(hoverTransition)
	if activeList {
		row.Bind(v.sidebarHandle("thread-" + t.ID))
	}
	if activeList {
		progress := v.settleProgress(c, t.ID)
		row.Height(threadHeight*progress).Opacity(progress).Translate(-16*(1-progress), 0).Clip().Disabled(t.Settled)
	}
	now, reducedMotion := c.Now(), c.Preferences().ReduceMotion
	toggleSettled := func() {
		wasSettled := t.Settled
		v.C.Settle(t.ID, !wasSettled)
		if activeList && !wasSettled && t.Settled && !reducedMotion {
			if v.settling == nil {
				v.settling = make(map[string]time.Time)
			}
			v.settling[t.ID] = now.Add(settleDuration)
		} else {
			delete(v.settling, t.ID)
		}
	}
	row.ContextMenu(func(m *ui.Menu) {
		label := tx.Settle()
		if t.Settled {
			label = tx.RestoreConversation()
		}
		if m.Item(label).Disabled(v.C.Running[t.ID]).Chosen() {
			toggleSettled()
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
				icon := chat
				if len(t.Turns) == 0 {
					icon = unsentChat
				}
				ui.Icon(c, icon).Size(16, 16).TextColor(c.Theme().TextMuted)
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
			iconButton(c, label, icon, toggleSettled)
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
		if t != nil && t.HandoffFrom != "" {
			ui.Button(c, tx.HandoffSource()).Height(26).FontSize(11).OnClick(func() { v.C.Select(t.HandoffFrom) })
		}
		if t != nil && t.Settled {
			ui.Button(c, tx.Restore()).OnClick(func() { v.C.Settle(t.ID, false) })
		}
	})
	ui.Divider(c)
	if t == nil || len(t.Turns) == 0 {
		ui.Column(c).Grow(1).Center().Gap(14).Children(func() {
			ui.Icon(c, spark).Size(36, 36).TextColor(c.Theme().TextMuted)
			ui.Text(c, tx.StartIdea()).FontSize(22).Bold()
			if t.Driver() == "codex" && v.C.Connection != "Connected" {
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
			ui.Column(c.Key(i)).Gap(20).FillWidth().Children(func() {
				ui.Column(c).AlignSelf(ui.End).MaxWidth(740).Radius(14).Background(c.Theme().Surface).Padding(12, 16).Children(func() { v.message(c, turn.Prompt, t.ID) })
				ui.Column(c).Gap(14).Children(func() {
					for j := 0; j < len(turn.Items); {
						key := fmt.Sprintf("%s-%d-%d", t.ID, i, j)
						if timelineStandalone(turn.Items[j].Kind) {
							v.item(c, &turn.Items[j], key, turn.Status == "inProgress")
							j++
							continue
						}
						start := j
						for j < len(turn.Items) && !timelineStandalone(turn.Items[j].Kind) {
							j++
						}
						v.activityGroup(c, turn.Items[start:j], key, turn.Status == "inProgress")
					}
					if turn.Status == "inProgress" && !compactionRunning(turn) {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Spinner(c).Size(13, 13)
							ui.Text(c, agentName(t.Driver())).FontSize(12).TextColor(c.Theme().TextMuted)
						})
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
	if item.Kind == "contextCompaction" {
		v.compactionDivider(c, item, key, running)
		return
	}
	if item.Kind == "mcpToolCall" || item.Kind == "toolCall" && item.MCP != nil && !nativeTool(item) {
		v.mcpItem(c, item, key, running)
		return
	}
	if item.MCP != nil && item.Kind != "plan" {
		display := *item
		display.Text = toolActivityText(item, *v.openState(key))
		item = &display
	}
	if item.Kind == "steerMessage" {
		ui.Column(c.Key(key)).AlignSelf(ui.End).MaxWidth(740).Radius(12).Background(c.Theme().Surface).Padding(10, 14).Children(func() {
			v.message(c, item.Text, v.C.State.Selected)
		})
		return
	}
	if item.Kind == "agentMessage" {
		if running && item.Status != "completed" {
			markdown.Stream(c.Key(key), item.Text, &v.md)
		} else {
			markdown.View(c.Key(key), item.Text, &v.md)
		}
		return
	}
	label, icon := v.activityKind(item.Kind)
	if (item.Kind == "commandExecution" || item.Kind == "fileChange" || item.MCP != nil) && item.Text != "" {
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
			} else if item.Kind == "commandExecution" || item.Kind == "fileChange" || item.MCP != nil && item.Kind != "plan" {
				v.activityOutput(c, item, key)
			} else if item.Kind == "plan" {
				markdown.View(c, strings.ReplaceAll(item.Text, "\n", "  \n"), &v.md)
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
			if t != nil && (r.ThreadID == t.ServerID || r.ThreadID == t.ID) {
				v.request(c, r)
			}
		}
		draft := &v.draft
		if t != nil {
			draft = v.C.ComposerDraft(t)
		}
		busy := t != nil && v.C.Running[t.ID]
		v.messageQueue(c, t)
		if busy {
			v.projectPicker = false
		} else {
			v.projectSelector(c, t)
		}
		ui.Column(c).Radius(14).Border(1, c.Theme().Border).Background(c.Theme().Surface).Padding(12, 14, 8).Gap(8).Children(func() {
			editing := t != nil && v.C.QueueEdit(t.ID) != nil
			if editing {
				ui.Row(c.Key("queue-edit-banner")).AlignItems(ui.Center).Gap(8).Transition(hoverTransition).Children(func() {
					ui.Box(c).Grow(1)
					ui.Button(c, tx.Cancel()).OnClick(func() { v.C.CancelQueueEdit(t.ID) })
				})
			}
			// Clip the editor's custom painting at the input viewport.
			ui.Box(c).Height(90).Shrink(0).Clip().Children(func() {
				v.composerInput(c, draft)
			})
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				v.modelSelector(c)
				if t.Driver() == "codex" {
					v.permissionSelector(c)
				} else if t != nil {
					v.agentPermissions(c, t)
				}
				ui.Box(c).Grow(1)
				iconButton(c, tx.AttachFile(), plus, v.pickAttachments).Disabled(t == nil)
				if busy {
					iconButton(c, tx.Stop(), stopIcon, v.C.Interrupt)
				}
				{
					label := tx.Send()
					if busy {
						label = tx.QueueAdd()
					}
					if editing {
						label = tx.QueueSave()
					}
					ui.PrimaryButton(c, "").Label(label).Tooltip(label).Size(30, 30).Padding(0).Gap(0).Center().Radius(15).Disabled(t == nil || strings.TrimSpace(*draft) == "" || (!editing && t.Driver() == "codex" && v.C.Connection != "Connected")).Children(func() { ui.Icon(c, arrow).Size(16, 16).Shrink(0) }).OnClick(func() {
						if v.C.Send(*draft, false) {
							v.draft = ""
						}
					})
				}
			})
		})
	})
}

func (v *View) request(c *ui.Context, r *codex.Request) {
	tx := v.texts()
	key := string(r.ID)
	values := v.answers[key]
	if values == nil {
		values = map[string]string{}
		for k, value := range r.InitialValues {
			values[k] = value
		}
		v.answers[key] = values
	}
	ui.Scroll(c.Key("request-"+key)).MaxHeight(300).Padding(14).Radius(12).Border(1, c.Theme().Accent).Gap(10).Children(func() {
		if r.Method == "mcpServer/elicitation/request" || r.Method == "client/form" {
			v.elicitation(c, r, values)
		} else if r.Method == "item/tool/requestUserInput" {
			for _, q := range r.Questions {
				ui.Column(c.Key(q.ID)).Gap(6).Children(func() {
					ui.Text(c, q.Question).Bold()
					for _, o := range q.Options {
						b := ui.Button(c, o.Label).Tooltip(o.Description).Disabled(r.Responding)
						selected := values[q.ID] == o.Label
						var choices []string
						if q.MultiSelect {
							_ = json.Unmarshal([]byte(values[q.ID]), &choices)
							selected = slices.Contains(choices, o.Label)
						}
						if selected {
							b.Background(c.Theme().SurfaceHover).Border(1, c.Theme().Accent)
						}
						b.OnClick(func() {
							if !q.MultiSelect {
								values[q.ID] = o.Label
								return
							}
							if selected {
								choices = slices.DeleteFunc(choices, func(s string) bool { return s == o.Label })
							} else {
								choices = append(choices, o.Label)
							}
							if choices == nil {
								choices = []string{}
							}
							data, _ := json.Marshal(choices)
							values[q.ID] = string(data)
						})
					}
					inputKey := q.ID
					if q.MultiSelect {
						inputKey += ":other"
					}
					value := values[inputKey]
					in := ui.TextInput(c, &value).Placeholder(tx.AnswerPlaceholder()).Label(q.ID).Disabled(r.Responding)
					if q.IsSecret {
						in.Password()
					}
					if in.Changed() {
						values[inputKey] = value
					}
				})
			}
			ui.PrimaryButton(c, tx.Continue()).Disabled(r.Responding).OnClick(func() { v.C.AnswerQuestions(r, values) })
			if r.Cancellable {
				ui.Button(c, tx.Cancel()).Disabled(r.Responding).OnClick(func() { v.C.CancelQuestions(r) })
			}
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

func iconButton(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	b := ui.ButtonBase(c.Key(label)).Label(label).Tooltip(label).Size(26, 26).Radius(7).Center().TextColor(c.Theme().TextMuted)
	b.Transition(hoverTransition)
	if b.Hovered() {
		b.Background(c.Theme().SurfaceHover).TextColor(c.Theme().Text)
	}
	return b.Children(func() { ui.Icon(c, icon).Size(16, 16) }).OnClick(fn)
}
