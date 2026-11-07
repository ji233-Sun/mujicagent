package view

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type projectFlight struct {
	from                       ui.Rect
	projectID, threadID, title string
	started                    time.Time
}

func (v *View) sidebarHandle(key string) *ui.Handle {
	if v.sidebarHandles == nil {
		v.sidebarHandles = make(map[string]*ui.Handle)
	}
	if v.sidebarHandles[key] == nil {
		v.sidebarHandles[key] = &ui.Handle{}
	}
	return v.sidebarHandles[key]
}

func (v *View) projectAnchor(c *ui.Context, projectID, threadID string) ui.Rect {
	expanded := v.C.State.NoProjectExpanded
	for _, p := range v.C.State.Projects {
		if p.ID == projectID {
			expanded = p.Expanded
		}
	}
	if expanded {
		r := v.sidebarHandle("thread-" + threadID).Bounds(c)
		header := v.sidebarHandle("project-" + projectID).Bounds(c)
		if r.W > 0 && r.Y >= header.Y+header.H && r.Y < header.Y+header.H+10*threadHeight {
			return r
		}
	}
	return v.sidebarHandle("project-" + projectID).Bounds(c)
}

func (v *View) projectSelector(c *ui.Context, t *model.Thread) {
	tx := v.texts()
	id, label := "", tx.NoProject()
	if t != nil {
		id = t.ProjectID
	}
	for _, p := range v.C.State.Projects {
		if p.ID == id {
			label = p.Name
		}
	}
	button := ui.ButtonBase(c.Key("project-selector")).Height(26).MaxWidth(300).AlignSelf(ui.Start).
		Padding(0, 6).Gap(6).AlignItems(ui.Center).Radius(7).Label(tx.SwitchProject()).Tooltip(label).
		Disabled(t != nil && v.C.QueueEdit(t.ID) != nil).Transition(hoverTransition)
	if button.Hovered() || v.projectPicker {
		button.Background(c.Theme().SurfaceHover)
	}
	button.Children(func() {
		ui.Icon(c, folder).Size(14, 14).Shrink(0)
		ui.Text(c, label).FontSize(11).SingleLine().MinWidth(0).Shrink(1)
		ui.Icon(c, chevron).Size(12, 12).Rotate(90)
	})
	button.OnClick(func() {
		v.projectPicker = !v.projectPicker
		v.projectSearch = ""
		v.modelPicker = false
		v.permissionPicker = false
	})
	pickerPopover(c.Key("project-picker"), button, &v.projectPicker, tx.SwitchProject(), 300, func() {
		ui.TextInputBase(c, &v.projectSearch).Height(28).FontSize(12).Padding(0, 8).Placeholder(tx.SearchProjects()).Label(tx.SearchProjects())
		projects := []model.Project{{Name: tx.NoProject()}}
		projects = append(projects, v.C.State.Projects...)
		matches := make([]model.Project, 0, len(projects))
		query := strings.ToLower(strings.TrimSpace(v.projectSearch))
		for _, p := range projects {
			if strings.Contains(strings.ToLower(p.Name+" "+p.Path), query) {
				matches = append(matches, p)
			}
		}
		ui.List(c.Key("project-options"), &v.projectList, len(matches)).Label(tx.SearchProjects() + " · " + tx.SwitchProject()).Height(float32(min(6, max(1, len(projects))) * 32)).
			ItemKey(func(i int) any { return matches[i].ID }).Rows(func(row ui.ListRow) {
			p := matches[row.Index]
			b := pickerOption(row.Context, tx.SwitchProject()+" · "+p.Name, p.ID == id).Height(32).Tooltip(p.Path)
			b.Children(func() {
				ui.Icon(row.Context, folder).Size(14, 14).Shrink(0)
				ui.Text(row.Context, p.Name).FontSize(12).SingleLine().Grow(1).MinWidth(0)
				if p.ID == id {
					ui.Icon(row.Context, checkIcon).Size(14, 14)
				}
			})
			b.OnClick(func() {
				from := ui.Rect{}
				if t != nil {
					from = v.projectAnchor(c, t.ProjectID, t.ID)
				}
				if v.C.SwitchProject(p.ID) {
					next := v.C.State.Thread(v.C.State.Selected)
					if t == nil {
						next.Draft = v.draft
						v.draft = ""
						v.C.Save()
					}
					if list := v.projectLists[p.ID]; list != nil {
						list.ScrollTo(0, ui.Start)
					}
					if !c.Preferences().ReduceMotion && from.W > 0 {
						v.projectFlight = &projectFlight{from: from, projectID: p.ID, threadID: next.ID, title: v.threadTitle(next), started: c.Now()}
					}
					v.editorHandle.Focus(c)
				}
				v.projectPicker = false
			})
		}).Children(func() {
			if len(matches) == 0 {
				ui.Text(c, tx.NoProjects()).FontSize(12).Padding(8)
			}
		})
	})
}

func (v *View) drawProjectFlight(c *ui.Context) {
	f := v.projectFlight
	if f == nil {
		return
	}
	progress := float32(c.Now().Sub(f.started)) / float32(420*time.Millisecond)
	if progress >= 1 || c.Preferences().ReduceMotion {
		v.projectFlight = nil
		return
	}
	c.AnimationFrame()
	to := v.projectAnchor(c, f.projectID, f.threadID)
	if to.W <= 0 {
		return
	}
	p := progress * progress * (3 - 2*progress)
	ui.Overlay(c, func() {
		ui.Row(c.Key("project-flight")).Absolute().Left(f.from.X+(to.X-f.from.X)*p).Top(f.from.Y+(to.Y-f.from.Y)*p).
			Width(f.from.W+(to.W-f.from.W)*p).Height(32).Padding(0, 8).Gap(6).AlignItems(ui.Center).
			Radius(8).Background(c.Theme().SurfaceHover).Border(1, c.Theme().Accent).Opacity(1 - progress*0.5).Disabled(true).
			Children(func() {
				ui.Icon(c, chat).Size(14, 14)
				ui.Text(c, f.title).FontSize(12).SingleLine().MinWidth(0).Grow(1)
			})
	})
}
