package controller

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type HarnessState struct {
	Checking, Installing bool
	CheckedAt            time.Time
	Installation         harness.Installation
	Release              harness.Release
	Error                string
	Source, Binary       string
	cancel               context.CancelFunc
}

func (c *Controller) StartHarnessUpdates() {
	go func() {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-timer.C:
				c.post(func() {
					if !c.State.Settings.DisableHarnessAutoCheck {
						c.CheckHarness()
					}
				})
				timer.Reset(24 * time.Hour)
			}
		}
	}()
}
func (c *Controller) CheckHarness() {
	u := &c.Harness
	if u.Checking || u.Installing {
		return
	}
	binary, source := c.State.Settings.CodexPath, c.State.Settings.HarnessSource
	u.Checking, u.Error = true, ""
	u.Release = harness.Release{}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	u.cancel = cancel
	root := filepath.Join(c.Dir, "tools", "codex")
	go func() {
		defer cancel()
		current, err := harness.Inspect(ctx, binary, root)
		// Only a missing default executable is a fresh install. Invalid custom paths stay visible as errors.
		if errors.Is(err, exec.ErrNotFound) && binary == "codex" {
			err = nil
		}
		var release harness.Release
		if err == nil {
			lookupSource := source
			if lookupSource == "cn-native" {
				lookupSource = "cn"
			}
			release, err = harness.Latest(ctx, lookupSource)
		}
		c.post(func() {
			u.Checking = false
			u.cancel = nil
			if binary != c.State.Settings.CodexPath || source != c.State.Settings.HarnessSource {
				return
			}
			u.Installation, u.Release, u.Binary, u.Source = current, release, binary, source
			u.CheckedAt = time.Now()
			if err != nil {
				u.Error = err.Error()
			}
		})
	}()
}
func (c *Controller) CancelHarness() {
	if c.Harness.cancel != nil {
		c.Harness.cancel()
	}
}
func (c *Controller) InstallHarness() {
	u := &c.Harness
	if u.Checking || u.Installing || c.Connection == "Connecting" || c.AnyRunning() || u.Release.Version == "" || u.Binary != c.State.Settings.CodexPath || u.Source != c.State.Settings.HarnessSource {
		return
	}
	u.Installing, u.Error = true, ""
	release, current, source := u.Release, u.Installation, u.Source
	root := filepath.Join(c.Dir, "tools", "codex")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	u.cancel = cancel
	// Release executable handles before an external package manager upgrades files on Windows.
	c.epoch++
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}
	c.Connection = "Disconnected"
	go func() {
		defer cancel()
		installSource := source
		if source == "cn-native" {
			installSource = "native-mirror"
		}
		path, err := harness.Install(ctx, root, installSource, release, current)
		c.post(func() {
			u.Installing = false
			u.cancel = nil
			if err != nil {
				u.Error = err.Error()
				return
			}
			previous := c.State.Settings.CodexPath
			c.State.Settings.CodexPath = path
			if err := model.Save(filepath.Join(c.Dir, "state.json"), c.State); err != nil {
				c.State.Settings.CodexPath = previous
				u.Error = err.Error()
				return
			}
			c.Connect()
			c.CheckHarness()
		})
	}()
}
