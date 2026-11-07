package harness

import (
	"context"
	"fmt"
	"path/filepath"
)

// Capabilities describe protocol differences, not inferred model capabilities.
// Model effort/speed choices still come from the CLI's model catalog.
type Capabilities struct {
	PersistentConnection, PlanMode, ServiceTier bool
}

// Driver is the single catalog of executable session adapters.
// Installation and authentication remain separate from turn execution.
type Driver struct {
	ID, Name     string
	Capabilities Capabilities
	args         func(RunOptions) ([]string, error)
	run          func(context.Context, RunOptions, []string, func(AgentEvent)) error
	steer        func(string, string, []any) map[string]any
	steerTurn    func(context.Context, RunOptions, TurnTarget, Input) error
	interrupt    func(context.Context, RunOptions, TurnTarget) error
	sessionPath  func(string, string) string
}

var Drivers = []Driver{
	{
		ID: "codex", Name: "Codex",
		Capabilities: Capabilities{PersistentConnection: true, PlanMode: true, ServiceTier: true},
		args:         codexArgs, run: runCodex,
		steerTurn: codexSteerTurn, interrupt: codexInterrupt,
	},
	{
		ID: "pi", Name: "Pi", args: piArgs, run: runPi, steer: piSteer,
		sessionPath: func(dir, id string) string { return filepath.Join(dir, "sessions", "pi", id+".jsonl") },
	},
	{ID: "droid", Name: "Droid", args: droidArgs, run: runDroid, steer: droidSteer},
}

func Lookup(id string) (Driver, bool) {
	for _, d := range Drivers {
		if d.ID == id {
			return d, true
		}
	}
	return Driver{}, false
}

// SessionID keeps native session identities inside their owning driver.
func (d Driver) SessionID(dir, threadID, saved string) string {
	if d.sessionPath != nil {
		return d.sessionPath(dir, threadID)
	}
	return saved
}

func CanSteer(id string) bool {
	d, ok := Lookup(id)
	return ok && (d.steer != nil || d.steerTurn != nil)
}

func agentArgs(o RunOptions) ([]string, error) {
	d, ok := Lookup(o.Driver)
	if !ok || d.args == nil || d.run == nil {
		return nil, fmt.Errorf("unsupported agent: %s", o.Driver)
	}
	if o.Plan && !d.Capabilities.PlanMode {
		return nil, fmt.Errorf("%s does not support plan mode", d.Name)
	}
	if o.ServiceTier != "" && !d.Capabilities.ServiceTier {
		return nil, fmt.Errorf("%s does not support service tiers", d.Name)
	}
	for _, part := range o.Input {
		if part.Type != "text" && part.Type != "localImage" {
			return nil, fmt.Errorf("%s: unsupported input type %q", d.Name, part.Type)
		}
		if part.Type == "localImage" && part.Path == "" {
			return nil, fmt.Errorf("%s: missing image path", d.Name)
		}
	}
	return d.args(o)
}
