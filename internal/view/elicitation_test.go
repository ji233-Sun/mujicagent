package view

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func TestElicitationCard(t *testing.T) {
	ctrl := controller.New(t.TempDir(), model.New(), nil)
	ctrl.AddProject(t.TempDir())
	thread := ctrl.State.Thread(ctrl.State.Selected)
	thread.ServerID = "t1"
	r := &codex.Request{ID: json.RawMessage(`"mcp-1"`), Method: "mcpServer/elicitation/request", ThreadID: "t1", ServerName: "github", Mode: "form", Message: "Create pull request?", RequestedSchema: json.RawMessage(`{"type":"object","properties":{"decision":{"type":"string","oneOf":[{"const":"once","title":"Allow once"},{"const":"session","title":"This session"}]}},"required":["decision"]}`)}
	ctrl.Requests = []*codex.Request{r}
	v := New(ctrl)
	tt := ui.NewTester(v.Render, 1140, 780)
	tt.Frame()
	for _, label := range []string{"MCP 请求 · github", "Create pull request?", "Allow once", "This session", "同意", "拒绝", "取消"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("missing %q", label)
		}
	}
	if err := tt.Click("This session"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if v.answers[string(r.ID)]["decision"] != "session" {
		t.Fatal("option did not preserve protocol value")
	}
	r.Responding = true
	tt.Frame()
	_ = tt.Click("Allow once")
	tt.Frame()
	if v.answers[string(r.ID)]["decision"] != "session" {
		t.Fatal("responding request was editable")
	}
	ctrl.Requests = nil
	tt.Frame()
	tt.Frame()
	if len(v.answers) != 0 {
		t.Fatal("resolved form input retained")
	}
	r.Responding = false
	r.Mode = "openai/userVerification"
	ctrl.Requests = []*codex.Request{r}
	tt.Frame()
	if _, ok := tt.Find(v.texts().McpUnsupported() + " (" + r.Mode + ")"); !ok {
		t.Fatal("unsupported mode invisible")
	}
}
