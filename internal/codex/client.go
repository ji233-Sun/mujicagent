// Package codex implements the local App Server's newline-delimited JSON-RPC transport.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type Message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}
type Client struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	mu      sync.Mutex
	writeMu sync.Mutex
	next    uint64
	pending map[string]chan Message
	done    chan struct{}
	once    sync.Once
	emit    func(Message)
}

func Start(ctx context.Context, binary string, emit func(Message)) (*Client, error) {
	cmd, err := desktopCommand(ctx, binary, "app-server", "--listen", "stdio://")
	if err != nil {
		return nil, err
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	// stderr can contain local paths and diagnostics; never mix it into the protocol.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		in.Close()
		return nil, err
	}
	c := &Client{cmd: cmd, in: in, pending: make(map[string]chan Message), done: make(chan struct{}), emit: emit}
	go c.read(out)
	var result json.RawMessage
	err = c.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "mujicagent", "title": "MujicAgent", "version": "0.1.0"}, "capabilities": map[string]bool{"experimentalApi": true}}, &result)
	if err == nil {
		err = c.write(map[string]any{"method": "initialized"})
	}
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("Codex initialization: %w", err)
	}
	return c, nil
}

// Version uses the exact executable and environment of the connected server.
func (c *Client) Version() string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.cmd.Path, "--version")
	cmd.Env = c.cmd.Env
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return "Not installed"
	}
	return strings.TrimSpace(string(out))
}

func (c *Client) read(out io.Reader) {
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var m Message
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			c.emit(Message{Method: "transport/error", Error: &RPCError{Message: "Invalid JSON from Codex"}})
			break
		}
		if m.Method != "" {
			c.emit(m)
			continue
		}
		c.mu.Lock()
		ch := c.pending[string(m.ID)]
		delete(c.pending, string(m.ID))
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
	err := scanner.Err()
	c.Close()
	waitErr := c.cmd.Wait()
	if err == nil {
		err = waitErr
	}
	if err == nil {
		err = errors.New("Codex connection closed")
	}
	c.emit(Message{Method: "transport/closed", Error: &RPCError{Message: err.Error()}})
}

func (c *Client) write(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return errors.New("Codex is disconnected")
	default:
	}
	return json.NewEncoder(c.in).Encode(v)
}

func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	c.next++
	id := c.next
	key := strconv.FormatUint(id, 10)
	ch := make(chan Message, 1)
	c.pending[key] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, key); c.mu.Unlock() }()
	if err := c.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return fmt.Errorf("%s: %s (%d)", method, m.Error.Message, m.Error.Code)
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(m.Result, result)
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", method, ctx.Err())
	case <-c.done:
		return errors.New("Codex disconnected during " + method)
	}
}
func (c *Client) Reply(id json.RawMessage, result any) error {
	return c.write(map[string]any{"id": id, "result": result})
}
func (c *Client) Reject(id json.RawMessage, text string) error {
	return c.write(map[string]any{"id": id, "error": RPCError{Code: -32601, Message: text}})
}
func (c *Client) Close() {
	c.once.Do(func() {
		close(c.done)
		c.in.Close()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
}
func Timeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 45*time.Second)
}

type Question struct {
	ID       string `json:"id"`
	Header   string `json:"header"`
	Question string `json:"question"`
	IsSecret bool   `json:"isSecret"`
	IsOther  bool   `json:"isOther"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}
type Request struct {
	ServerName                      string          `json:"serverName"`
	Mode                            string          `json:"mode"`
	Message                         string          `json:"message"`
	URL                             string          `json:"url"`
	Title                           string          `json:"title"`
	Description                     string          `json:"description"`
	RequestedSchema                 json.RawMessage `json:"requestedSchema"`
	Meta                            json.RawMessage `json:"_meta"`
	ID                              json.RawMessage
	Method                          string
	ThreadID                        string     `json:"threadId"`
	TurnID                          string     `json:"turnId"`
	ItemID                          string     `json:"itemId"`
	Questions                       []Question `json:"questions"`
	Reason                          string     `json:"reason"`
	Command                         string     `json:"command"`
	Cwd                             string     `json:"cwd"`
	Responding                      bool
	Kind                            string                   `json:"kind"`
	AvailableDecisions              []json.RawMessage        `json:"availableDecisions"`
	ProposedExecpolicyAmendment     []string                 `json:"proposedExecpolicyAmendment"`
	ProposedNetworkPolicyAmendments []NetworkPolicyAmendment `json:"proposedNetworkPolicyAmendments"`
	NetworkApprovalContext          *struct {
		Host string `json:"host"`
	} `json:"networkApprovalContext"`
	AdditionalPermissions json.RawMessage `json:"additionalPermissions"`
	GrantRoot             string          `json:"grantRoot"`
}
type Model struct {
	ID                        string        `json:"id"`
	Model                     string        `json:"model"`
	DisplayName               string        `json:"displayName"`
	IsDefault                 bool          `json:"isDefault"`
	ServiceTiers              []ServiceTier `json:"serviceTiers"`
	DefaultReasoningEffort    string        `json:"defaultReasoningEffort"`
	SupportedReasoningEfforts []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}

type ServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SupportsSpeed only accepts the speed options advertised by this model.
func (m Model) SupportsSpeed(id string) bool {
	if id != "priority" && id != "fast" && id != "ultrafast" {
		return false
	}
	for _, tier := range m.ServiceTiers {
		if tier.ID == id {
			return true
		}
	}
	return false
}
