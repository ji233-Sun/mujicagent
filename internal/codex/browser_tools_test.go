package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func browserTestSchemaCommand(t *testing.T) bool {
	t.Helper()
	if !slices.Contains(os.Args, "generate-json-schema") {
		return false
	}
	index := slices.Index(os.Args, "--out")
	if index < 0 || index+1 >= len(os.Args) || !slices.Contains(os.Args, "--experimental") {
		t.Fatal("invalid schema command")
	}
	data, err := os.ReadFile("testdata/browser-protocol-0.163.0.json")
	if err != nil {
		t.Fatal(err)
	}
	var files map[string]json.RawMessage
	if err := json.Unmarshal(data, &files); err != nil {
		t.Fatal(err)
	}
	mode := os.Getenv("MUJICAGENT_BROWSER_REGISTRATION")
	if mode == "old-schema" {
		files["v2/ThreadStartParams.json"] = json.RawMessage(`{"properties":{}}`)
	} else if mode == "no-image" {
		files["DynamicToolCallResponse.json"] = json.RawMessage(`{"required":["contentItems","success"]}`)
	} else if mode == "wrong-field-type" {
		var response map[string]any
		if err := json.Unmarshal(files["DynamicToolCallResponse.json"], &response); err != nil {
			t.Fatal(err)
		}
		variants := response["definitions"].(map[string]any)["DynamicToolCallOutputContentItem"].(map[string]any)["oneOf"].([]any)
		for _, variant := range variants {
			properties := variant.(map[string]any)["properties"].(map[string]any)
			if imageURL, ok := properties["imageUrl"]; ok {
				imageURL.(map[string]any)["type"] = "integer"
			}
		}
		files["DynamicToolCallResponse.json"], _ = json.Marshal(response)
	} else if mode == "no-schema" {
		return true
	}
	for name, schema := range files {
		path := filepath.Join(os.Args[index+1], filepath.FromSlash(name))
		if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, schema, 0600) != nil {
			t.Fatal("could not generate fake CLI schema")
		}
	}
	return true
}

func TestBrowserToolRegistration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test launcher")
	}
	for _, mode := range []string{"supported", "reject", "wrong-resume", "old-schema", "no-image", "wrong-field-type", "no-schema"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(t.TempDir(), "codex")
			launcher := fmt.Sprintf("#!/bin/sh\nexec '%s' -test.run=^TestProtocolChild$ -- \"$@\"\n", strings.ReplaceAll(exe, "'", "'\\''"))
			if err := os.WriteFile(binary, []byte(launcher), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("MUJICAGENT_PROTOCOL_CHILD", "1")
			t.Setenv("MUJICAGENT_BROWSER_REGISTRATION", mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, err := Start(ctx, binary, func(Message) {})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			threads := NewBrowserThreads(client)
			if _, err := threads.Resume(ctx, "old-thread", nil); !errors.Is(err, ErrBrowserResume) {
				t.Fatalf("unverified old thread accepted: %v", err)
			}
			for _, reserved := range []string{"dynamicTools", "threadId", "history", "path"} {
				if _, err := threads.Start(ctx, map[string]any{reserved: "override"}); !errors.Is(err, ErrBrowserRegistration) {
					t.Fatal("caller can override registration or identity", reserved, err)
				}
			}
			params := map[string]any{"cwd": "/project", "model": "fixture-model"}
			id, err := threads.Start(ctx, params)
			unsupported := mode == "reject" || mode == "old-schema" || mode == "no-image" || mode == "wrong-field-type" || mode == "no-schema"
			if unsupported {
				if !errors.Is(err, ErrBrowserRegistration) || strings.Contains(err.Error(), "do-not-log") {
					t.Fatalf("unsafe or missing capability error: %v", err)
				}
			} else {
				if err != nil || id != "browser-thread" {
					t.Fatal(id, err)
				}
				resumed, err := threads.Resume(ctx, id, params)
				if mode == "wrong-resume" {
					if !errors.Is(err, ErrBrowserRegistration) || resumed != "" {
						t.Fatalf("silently replaced existing identity: %q %v", resumed, err)
					}
				} else if err != nil || resumed != id {
					t.Fatal(resumed, err)
				}
				if _, err := NewBrowserThreads(client).Resume(ctx, id, params); !errors.Is(err, ErrBrowserResume) {
					t.Fatal("a new registry guessed persisted tool support", err)
				}
				threads.Forget(id)
				if _, err := threads.Resume(ctx, id, params); !errors.Is(err, ErrBrowserResume) {
					t.Fatal("forgotten thread can still be resumed with assumed tools", err)
				}
			}
			if len(params) != 2 {
				t.Fatal("registration mutated caller parameters")
			}
			var requests []Message
			if err := client.Call(ctx, "browser/audit", nil, &requests); err != nil {
				t.Fatal(err)
			}
			want := 2
			if mode == "reject" {
				want = 1
			} else if unsupported {
				want = 0
			}
			if len(requests) != want {
				t.Fatalf("unexpected fallback or retry: %d requests, want %d", len(requests), want)
			}
			if want == 0 {
				return
			}
			var start struct {
				Cwd, Model   string
				DynamicTools []DynamicToolSpec `json:"dynamicTools"`
			}
			if json.Unmarshal(requests[0].Params, &start) != nil || requests[0].Method != "thread/start" || start.Cwd != "/project" || start.Model != "fixture-model" || len(start.DynamicTools) != 6 {
				t.Fatalf("wrong registration: %s", requests[0].Params)
			}
			for _, spec := range start.DynamicTools {
				if spec.Type != "function" || !IsBrowserTool(spec.Name) || spec.InputSchema["additionalProperties"] != false {
					t.Fatalf("invalid installed-schema tool shape: %+v", spec)
				}
			}
			if want == 2 {
				var resume map[string]any
				json.Unmarshal(requests[1].Params, &resume)
				if requests[1].Method != "thread/resume" || resume["threadId"] != id || resume["dynamicTools"] != nil || resume["history"] != nil || resume["path"] != nil {
					t.Fatalf("resume overwrote tools/history: %s", requests[1].Params)
				}
			}
		})
	}
}

func TestInstalledBrowserToolSchema(t *testing.T) {
	if os.Getenv("MUJICAGENT_BROWSER_SCHEMA_TEST") != "1" {
		t.Skip("opt-in local schema generation; no model requests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd, err := desktopCommand(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{cmd: cmd}
	if err := client.checkBrowserToolProtocol(ctx); err != nil {
		t.Fatal(err)
	}
	t.Log(client.Version())
}

func TestBrowserToolArguments(t *testing.T) {
	for _, tc := range []struct {
		name, tool, args string
		valid            bool
	}{
		{"open", BrowserOpen, `{"pageId":"p","navigation":1,"url":"https://example.com/path?q=private"}`, true},
		{"observe", BrowserObserve, `{"pageId":"p","navigation":1}`, true},
		{"capture", BrowserCapture, `{"pageId":"p","navigation":1}`, true},
		{"click", BrowserClick, `{"pageId":"p","navigation":1,"observationId":"o","elementId":"e"}`, true},
		{"empty fill", BrowserFill, `{"pageId":"p","navigation":1,"observationId":"o","elementId":"e","text":""}`, true},
		{"scroll", BrowserScroll, `{"pageId":"p","navigation":1,"deltaX":0,"deltaY":-50.5}`, true},
		{"actor spoof", BrowserObserve, `{"pageId":"p","navigation":1,"actor":"human"}`, false},
		{"thread spoof", BrowserObserve, `{"pageId":"p","navigation":1,"threadId":"other"}`, false},
		{"selector", BrowserClick, `{"pageId":"p","navigation":1,"observationId":"o","elementId":"e","selector":"#password"}`, false},
		{"missing text", BrowserFill, `{"pageId":"p","navigation":1,"observationId":"o","elementId":"e"}`, false},
		{"null text", BrowserFill, `{"pageId":"p","navigation":1,"observationId":"o","elementId":"e","text":null}`, false},
		{"null scroll", BrowserScroll, `{"pageId":"p","navigation":1,"deltaX":null,"deltaY":5}`, false},
		{"zero scroll", BrowserScroll, `{"pageId":"p","navigation":1,"deltaX":0,"deltaY":0}`, false},
		{"overflow scroll", BrowserScroll, `{"pageId":"p","navigation":1,"deltaX":1e999,"deltaY":0}`, false},
		{"zero generation", BrowserObserve, `{"pageId":"p","navigation":0}`, false},
		{"negative generation", BrowserObserve, `{"pageId":"p","navigation":-1}`, false},
		{"overflow generation", BrowserObserve, `{"pageId":"p","navigation":18446744073709551616}`, false},
		{"fractional generation", BrowserObserve, `{"pageId":"p","navigation":1.5}`, false},
		{"empty page", BrowserObserve, `{"pageId":" ","navigation":1}`, false},
		{"duplicate field", BrowserObserve, `{"pageId":"p","navigation":1,"navigation":2}`, false},
		{"case alias", BrowserObserve, `{"PageId":"p","navigation":1}`, false},
		{"case duplicate", BrowserObserve, `{"pageId":"p","PageId":"other","navigation":1}`, false},
		{"trailing record", BrowserObserve, `{"pageId":"p","navigation":1} {}`, false},
		{"null object", BrowserObserve, `null`, false},
		{"array", BrowserObserve, `[]`, false},
		{"credentials", BrowserOpen, `{"pageId":"p","navigation":1,"url":"https://user:secret@example.com"}`, false},
		{"script", BrowserOpen, `{"pageId":"p","navigation":1,"url":"javascript:alert(1)"}`, false},
		{"file", BrowserOpen, `{"pageId":"p","navigation":1,"url":"file:///tmp/secret"}`, false},
		{"relative URL", BrowserOpen, `{"pageId":"p","navigation":1,"url":"/path"}`, false},
		{"unknown tool", "shell_command", `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseBrowserToolArguments(DynamicToolCall{Tool: tc.tool, Arguments: json.RawMessage(tc.args)})
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
	call := DynamicToolCall{Namespace: "other", Tool: BrowserObserve, Arguments: json.RawMessage(`{"pageId":"p","navigation":1}`)}
	if _, err := ParseBrowserToolArguments(call); !errors.Is(err, ErrBrowserToolUnknown) {
		t.Fatal("namespaced lookalike accepted", err)
	}
	specs := BrowserToolSpecs()
	specs[0].InputSchema["properties"].(map[string]any)["actor"] = map[string]any{"type": "string"}
	if BrowserToolSpecs()[0].InputSchema["properties"].(map[string]any)["actor"] != nil {
		t.Fatal("shared mutable registration schemas")
	}
}

func TestDynamicToolEnvelopeAndID(t *testing.T) {
	for _, raw := range []string{
		`{"threadId":"s","turnId":"t","callId":"c","tool":"browser_observe","arguments":{},"namespace":null}`,
		`{"threadId":"s","turnId":"t","callId":"c","tool":"browser_observe","arguments":{}}`,
	} {
		if _, err := DecodeDynamicToolCall(json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		`null`, `[]`, `{"threadId":"s","threadId":"other"}`, `{}`,
		`{"threadId":"s","turnId":"t","callId":"c","tool":"browser_observe","arguments":{},"actor":"human"}`,
	} {
		if _, err := DecodeDynamicToolCall(json.RawMessage(raw)); !errors.Is(err, ErrBrowserToolArguments) {
			t.Fatal("malformed envelope accepted", raw, err)
		}
	}
	for _, id := range []string{`"req-1"`, `"req-\u0031"`, `0`, `-7`, `9007199254740993`, `9223372036854775807`} {
		if _, ok := BrowserRequestKey(json.RawMessage(id)); !ok {
			t.Fatal("valid request ID rejected", id)
		}
	}
	a, _ := BrowserRequestKey(json.RawMessage(`"req-1"`))
	b, _ := BrowserRequestKey(json.RawMessage(`"req-\u0031"`))
	c, _ := BrowserRequestKey(json.RawMessage(`"7"`))
	d, _ := BrowserRequestKey(json.RawMessage(`7`))
	if a != b || c == d {
		t.Fatal("request identity is not canonical and type-preserving")
	}
	for _, id := range []string{`null`, `{}`, `true`, `1.5`, `1e2`, `9223372036854775808`, `01`, `7 8`} {
		if _, ok := BrowserRequestKey(json.RawMessage(id)); ok {
			t.Fatal("invalid request ID accepted", id)
		}
	}
}
