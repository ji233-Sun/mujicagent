package harness

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Input preserves text/image ordering until a protocol adapter encodes it.
// Files are represented by their visible path instructions, never private URIs.
type Input []InputPart

type InputPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Path string `json:"path,omitempty"`
}

func (in Input) codex() []any {
	parts := make([]any, 0, len(in))
	for _, part := range in {
		if part.Type == "text" {
			parts = append(parts, map[string]any{"type": "text", "text": part.Text, "text_elements": []any{}})
		} else if part.Type == "localImage" {
			parts = append(parts, map[string]any{"type": "localImage", "path": part.Path})
		}
	}
	return parts
}

func (in Input) TextAndImages() (string, []string) {
	var text strings.Builder
	var images []string
	for _, part := range in {
		if part.Type == "text" {
			text.WriteString(part.Text)
		} else if part.Type == "localImage" {
			images = append(images, part.Path)
		}
	}
	return text.String(), images
}

// TurnTarget always belongs to this driver's native session, never another CLI.
type TurnTarget struct{ Session, Turn string }

type TurnResult struct{ Status, Error string }

var ErrInterrupted = errors.New("agent interrupted")

// Session owns one submitted turn and its controls. Run publishes acceptance
// separately from completion. Codex completion arrives through its persistent
// connection and DecodeCodexEvent; process drivers publish it when RPC settles.
// All callbacks are serialized by the controller before touching app state.
type Session struct {
	driver Driver
	opts   RunOptions
	ctx    context.Context
	cancel context.CancelFunc
}

func NewSession(ctx context.Context, o RunOptions) (*Session, error) {
	if _, err := agentArgs(o); err != nil {
		return nil, err
	}
	d, _ := Lookup(o.Driver)
	if d.Capabilities.PersistentConnection && o.Client == nil {
		return nil, fmt.Errorf("%s is disconnected", d.Name)
	}
	if o.Control == nil {
		o.Control = &Control{}
	}
	if o.Input != nil {
		o.Prompt, o.Images = o.Input.TextAndImages()
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Session{driver: d, opts: o, ctx: ctx, cancel: cancel}, nil
}

func (s *Session) Run(emit func(AgentEvent)) {
	accepted := false
	err := Run(s.ctx, s.opts, func(e AgentEvent) {
		accepted = accepted || e.Accepted
		emit(e)
	})
	if err == nil && s.driver.Capabilities.PersistentConnection {
		return // This connection publishes turn/completed independently.
	}
	if err == nil && !accepted {
		err = fmt.Errorf("%s settled without accepting the message", s.driver.Name)
	}
	result := &TurnResult{Status: "completed"}
	if s.ctx.Err() != nil || errors.Is(err, ErrInterrupted) {
		result.Status = "interrupted"
	} else if err != nil {
		result.Status, result.Error = "failed", err.Error()
	}
	emit(AgentEvent{End: result})
}

func (s *Session) Steer(ctx context.Context, target TurnTarget, input Input) error {
	if !CanSteer(s.driver.ID) {
		return fmt.Errorf("%s does not support steering", s.driver.Name)
	}
	if _, err := agentArgs(RunOptions{Driver: s.driver.ID, Input: input}); err != nil {
		return err
	}
	if s.driver.steerTurn != nil {
		return s.driver.steerTurn(ctx, s.opts, target, input)
	}
	text, images := input.TextAndImages()
	return s.opts.Control.Steer(ctx, text, images)
}

func (s *Session) Interrupt(ctx context.Context, target TurnTarget) error {
	if s.driver.interrupt != nil {
		return s.driver.interrupt(ctx, s.opts, target)
	}
	s.cancel()
	return nil
}

func (s *Session) Close() { s.cancel() }
