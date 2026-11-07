package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

// Interaction uses the same request presentation as Codex. Only the adapter
// translates the selected response into a protocol-specific decision.
type Interaction struct {
	Request *codex.Request
	Reply   chan any
}

func ask(ctx context.Context, emit func(AgentEvent), r *codex.Request) (json.RawMessage, error) {
	i := &Interaction{Request: r, Reply: make(chan any, 1)}
	emit(AgentEvent{Interaction: i})
	defer emit(AgentEvent{Resolved: r})
	select {
	case value := <-i.Reply:
		return json.Marshal(value)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Control correlates asynchronous RPC acknowledgments. A successful write is
// not acceptance: queued messages are removed only after a positive response.
type Control struct {
	mu      sync.Mutex
	in      *json.Encoder
	next    int
	pending map[string]chan error
	closed  bool
	driver  string
}

func (c *Control) send(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.in == nil {
		return fmt.Errorf("agent is not running")
	}
	if c.driver == "droid" {
		droidEnvelope(v.(map[string]any))
	}
	return c.in.Encode(v)
}

func (c *Control) Steer(ctx context.Context, text string, images []string) error {
	parts, imageErr := droidPrompt(RunOptions{Prompt: text, Images: images})
	if imageErr != nil {
		return imageErr
	}

	c.mu.Lock()
	if c.closed || c.in == nil {
		c.mu.Unlock()
		return fmt.Errorf("agent is not running")
	}
	c.next++
	id := fmt.Sprintf("steer-%d", c.next)
	ch := make(chan error, 1)
	if c.pending == nil {
		c.pending = map[string]chan error{}
	}
	c.pending[id] = ch
	build := agentAdapters[c.driver].steer
	if build == nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("agent does not support steering")
	}
	message := build(id, text, parts)

	err := c.in.Encode(message)
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err != nil {
		return err
	}
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Control) response(id string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch := c.pending[id]; ch != nil {
		select {
		case ch <- err:
		default:
		}
	}
}

func (c *Control) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for _, ch := range c.pending {
		select {
		case ch <- fmt.Errorf("agent stopped before accepting message"):
		default:
		}
	}
}

// The full Factory protocol requires versioned request/response envelopes.
func droidEnvelope(v map[string]any) {
	v["factoryApiVersion"] = "1.0.0"
	v["factoryProtocolVersion"] = "1.269.0"
	v["type"] = "request"
	if _, ok := v["method"]; !ok {
		v["type"] = "response"
	}
}

func piSteer(id, text string, parts []any) map[string]any {
	v := map[string]any{"id": id, "type": "steer", "message": text}
	if len(parts) > 1 {
		v["images"] = parts[1:]
	}
	return v
}
func droidSteer(id, text string, parts []any) map[string]any {
	params := map[string]any{"text": text, "queuePlacement": "end_of_turn"}
	if len(parts) > 1 {
		values := []any{}
		for _, part := range parts[1:] {
			v := part.(map[string]string)
			values = append(values, map[string]string{"type": "base64", "data": v["data"], "mediaType": v["mimeType"]})
		}
		params["images"] = values
	}
	v := map[string]any{"jsonrpc": "2.0", "id": id, "method": "droid.add_user_message", "params": params}
	droidEnvelope(v)
	return v
}
