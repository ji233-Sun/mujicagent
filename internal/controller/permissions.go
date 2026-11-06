package controller

import (
	"os"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

func (c *Controller) PermissionID() string {
	if t := c.State.Thread(c.State.Selected); t != nil && t.PermissionProfile != "" {
		return t.PermissionProfile
	}
	return c.State.Settings.DefaultPermissionProfile
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
	if c.PermissionsLoading || c.permissionsTarget != c.State.Selected || c.Running[c.State.Selected] {
		return
	}
	for _, profile := range c.Permissions {
		if profile.ID != id || !profile.Allowed {
			continue
		}
		if t := c.State.Thread(c.State.Selected); t != nil {
			t.PermissionProfile = id
			c.Save()
		}
		return
	}
}
