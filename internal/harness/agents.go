package harness

import (
	"context"
	"fmt"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/model"
	"os"
	"path/filepath"
)

type RunOptions struct {
	Driver, Binary, Cwd, Session, Model, Access, Prompt, Effort string
	Control                                                     *Control
	Images                                                      []string
	Input                                                       Input
	Client                                                      *codex.Client
	Loaded, Plan                                                bool
	ServiceTier                                                 string
}
type AgentEvent struct {
	Compaction       model.CompactionStats
	Text, Session    string
	ID, Kind, Status string
	Interaction      *Interaction
	Resolved         *codex.Request
	Tool             *ToolEvent
	Delta            bool
	Accepted         bool
	TurnID           string
	Loaded           bool
	PreserveText     bool
	End              *TurnResult
	TurnError        string
}

// Run continuously consumes protocol output; credentials remain owned by the CLI.
func Run(ctx context.Context, o RunOptions, emit func(AgentEvent)) error {
	args, err := agentArgs(o)
	if err != nil {
		return err
	}
	d, _ := Lookup(o.Driver)
	if d.Capabilities.PersistentConnection && o.Client == nil {
		return fmt.Errorf("%s is disconnected", d.Name)
	}
	if o.Input != nil {
		o.Prompt, o.Images = o.Input.TextAndImages()
	}
	if d.sessionPath != nil {
		if err := os.MkdirAll(filepath.Dir(o.Session), 0700); err != nil {
			return err
		}
	}
	return d.run(ctx, o, args, emit)
}
