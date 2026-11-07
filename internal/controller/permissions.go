package controller

import (
	"fmt"
	"os"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

func (c *Controller) PermissionID() string {
	if id, ok := c.turnPermissions[c.State.Selected]; c.Running[c.State.Selected] && ok {
		return id
	}
	return c.NextPermissionID()
}

// NextPermissionID is the choice for future turns, not an active-turn override.
func (c *Controller) NextPermissionID() string {
	if t := c.State.Thread(c.State.Selected); t != nil {
		if t.Driver() != "codex" {
			return t.AgentAccess
		}
		if t.PermissionProfile != "" {
			return t.PermissionProfile
		}
	}
	return c.State.Settings.DefaultPermissionProfile
}

func (c *Controller) PermissionUpdating() bool {
	return c.permissionUpdates[c.State.Selected]
}

// Refresh on each opening: profiles may differ between project directories.
func (c *Controller) LoadPermissions() {
	c.permissionsTarget = c.State.Selected
	if c.client == nil {
		return
	}
	c.permissionsRequest++
	request, epoch, target := c.permissionsRequest, c.epoch, c.State.Selected
	c.Permissions, c.PermissionsError = nil, ""
	c.PermissionsLoading = true
	c.permissionsTarget = target
	cwd, _ := os.Getwd()
	if t := c.State.Thread(target); t != nil {
		cwd = t.Cwd
	}
	client := c.client
	go func() {
		ctx, cancel := codex.Timeout()
		defer cancel()
		profiles, err := client.PermissionProfiles(ctx, cwd)
		c.post(func() {
			if request != c.permissionsRequest || epoch != c.epoch {
				return
			}
			c.PermissionsLoading = false
			if c.State.Selected != target {
				return
			}
			if err != nil {
				c.PermissionsError = err.Error()
				return
			}
			c.Permissions = profiles
		})
	}()
}

func (c *Controller) SetPermission(id string) {
	if c.PermissionsLoading || c.permissionsTarget != c.State.Selected || c.PermissionUpdating() {
		return
	}
	for _, profile := range c.Permissions {
		if profile.ID != id || !profile.Allowed {
			continue
		}
		if t := c.State.Thread(c.State.Selected); t != nil {
			if id == c.NextPermissionID() {
				return
			}
			if c.Running[t.ID] && (!c.loaded[t.ID] || len(t.Turns) == 0 || t.Turns[len(t.Turns)-1].ID == "") {
				c.ReportError("The turn is starting. Try changing permissions again in a moment.")
				return
			}
			if !c.loaded[t.ID] {
				t.PermissionProfile = id
				c.Save()
				return
			}
			if c.client == nil || c.Connection != "Connected" {
				c.ReportError("Connect Codex before changing permissions.")
				return
			}
			client, epoch, target, serverID := c.client, c.epoch, t.ID, t.ServerID
			updates := c.permissionUpdates
			updates[target] = true
			go func() {
				ctx, cancel := codex.Timeout()
				defer cancel()
				// This endpoint queues future settings; it does not alter the active turn.
				err := client.Call(ctx, "thread/settings/update", map[string]any{
					"threadId": serverID, "permissions": profile.WireID(),
					"approvalPolicy": profile.ApprovalPolicy(), "approvalsReviewer": profile.Reviewer(),
				}, nil)
				c.post(func() {
					delete(updates, target)
					if epoch != c.epoch || c.State.Thread(target) != t {
						return
					}
					if err != nil {
						c.ReportError(fmt.Sprintf("Permission update was not confirmed; the previous selection is retained: %v", err))
						return
					}
					t.PermissionProfile = id
					c.Save()
				})
			}()
		}
		return
	}
}
