package controller

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type HarnessState struct {
	Models                       []harness.AgentModel
	ModelsLoading, ModelsChecked bool
	ModelsError, ModelsKey       string
	modelsRequest                uint64
	modelsCancel                 context.CancelFunc
	Checking, Installing         bool
	Preparing, Missing           bool
	Plan                         *harness.InstallPlan
	planRequest                  uint64
	CheckedAt                    time.Time
	Installation                 harness.Installation
	Release                      harness.Release
	Error                        string
	Source, Binary               string
	cancel                       context.CancelFunc
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
	u := c.HarnessFor("codex")
	if u.Checking || u.Installing || u.Preparing || u.Plan != nil {
		return
	}
	binary := c.State.Settings.CodexPath
	u.Checking, u.Error = true, ""
	u.Plan = nil
	u.Release = harness.Release{}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	u.cancel = cancel
	root := filepath.Join(c.Dir, "tools", "codex")
	go func() {
		defer cancel()
		current, err := harness.Inspect(ctx, binary, root)
		missing := errors.Is(err, exec.ErrNotFound) && binary == "codex"
		// Only a missing default executable is a fresh install. Invalid custom paths stay visible as errors.
		if missing {
			err = nil
		}
		var release harness.Release
		if err == nil && !missing {
			lookupSource := current.Source
			if lookupSource == "native-mirror" {
				lookupSource = "cn"
			}
			release, err = harness.Latest(ctx, lookupSource)
		}
		c.post(func() {
			u.Checking = false
			u.cancel = nil
			if binary != c.State.Settings.CodexPath {
				return
			}
			u.Installation, u.Release, u.Binary, u.Source = current, release, binary, current.Source
			u.Missing = missing
			u.CheckedAt = time.Now()
			if err != nil {
				u.Error = err.Error()
			}
		})
	}()
}

// PrepareHarness only probes and resolves a plan. It never installs or disconnects.
func (c *Controller) PrepareHarness(source string) {
	u := c.HarnessFor("codex")
	if u.Installing || u.Checking || c.AnyRunning() || c.Connection == "Connecting" {
		return
	}
	c.DismissHarnessPlan()
	u.Preparing, u.Error = true, ""
	request := u.planRequest
	binary := c.State.Settings.CodexPath
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	u.cancel = cancel
	root := filepath.Join(c.Dir, "tools", "codex")
	go func() {
		defer cancel()
		current, err := harness.Inspect(ctx, binary, root)
		if errors.Is(err, exec.ErrNotFound) && binary == "codex" {
			err = nil
		}
		if current.Path != "" {
			source = current.Source
		}
		var plan *harness.InstallPlan
		if err == nil && current.Path != "" && !current.CanUpdate() {
			err = fmt.Errorf("automatic update unavailable for %s; use the original package manager", current.Method)
		}
		if err == nil {
			lookup := source
			if lookup == "native-mirror" {
				lookup = "cn"
			}
			var release harness.Release
			release, err = harness.Latest(ctx, lookup)
			if err == nil {
				plan, err = harness.PrepareInstall(ctx, root, source, release, current)
			}
		}
		c.post(func() {
			if request != u.planRequest {
				return
			}
			u.Preparing, u.cancel = false, nil
			if binary != c.State.Settings.CodexPath {
				return
			}
			u.Plan, u.Binary = plan, binary
			if err != nil {
				u.Error = err.Error()
			}
		})
	}()
}

func (c *Controller) DismissHarnessPlan() {
	u := c.HarnessFor("codex")
	if u.Installing {
		return
	}
	if u.Preparing && u.cancel != nil {
		u.cancel()
		u.cancel = nil
	}
	u.planRequest++
	u.Preparing, u.Plan = false, nil
}
func (c *Controller) CancelHarness() {
	if c.HarnessFor("codex").cancel != nil {
		c.HarnessFor("codex").cancel()
	}
}
func (c *Controller) InstallHarness() {
	u := c.HarnessFor("codex")
	if u.Checking || u.Preparing || u.Installing || c.Connection == "Connecting" || c.AnyRunning() || u.Plan == nil || u.Binary != c.State.Settings.CodexPath {
		return
	}
	u.Installing, u.Error = true, ""
	plan, binary := u.Plan, u.Binary
	u.Plan = nil // Confirmation is single-use.
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
		current, err := harness.Inspect(ctx, binary, root)
		if errors.Is(err, exec.ErrNotFound) && binary == "codex" && plan.Current.Path == "" {
			err = nil
		}
		if err == nil && current != plan.Current {
			err = fmt.Errorf("installation changed; preview the update again")
		}
		var path string
		if err == nil {
			path, err = plan.Execute(ctx)
		}
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
