package harness

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReleaseAndInstallationRouting(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			target, exe, err := Target(platform, arch)
			if err != nil || target == "" || (platform == "windows" && exe != "codex.exe") {
				t.Fatal(target, exe, err)
			}
		}
	}
	if _, _, err := Target("windows", "386"); err == nil {
		t.Fatal("accepted unsupported platform")
	}
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"0.167.0", "0.99.0", true}, {"1.0.0", "1.0.0", false}, {"0.99.0", "0.167.0", false}, {"broken", "1.0.0", false}} {
		if Newer(tc.a, tc.b) != tc.want {
			t.Fatal(tc)
		}
	}
	root := filepath.Join(t.TempDir(), "tools")
	for _, tc := range []struct{ path, want string }{{filepath.Join(root, "release-1", "bin", "codex"), "managed"}, {filepath.Join(root+"-other", "codex"), "custom"}, {"/opt/homebrew/Caskroom/codex/1/codex", "brew"}, {"/usr/local/lib/node_modules/@openai/codex/bin/codex.js", "npm"}} {
		method, _ := classify(tc.path, root)
		if method != tc.want {
			t.Fatalf("%s: %s", tc.path, method)
		}
	}
}

func TestExtractPreservesCompanionsAndRejectsUnsafePaths(t *testing.T) {
	for _, name := range []string{"bin/codex", "bin/codex-code-mode-host", "../escape", "/absolute", `C:\escape`, `..\escape`} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: 4})
		tw.Write([]byte("data"))
		tw.Close()
		gz.Close()
		root := t.TempDir()
		err := extract(&buf, root)
		safe := name == "bin/codex" || name == "bin/codex-code-mode-host"
		if safe {
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("accepted", name)
		}
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/tmp"})
	tw.Close()
	gz.Close()
	if err := extract(&buf, t.TempDir()); err == nil {
		t.Fatal("accepted symlink")
	}
}

// Downloads only to a temporary directory; never changes the existing installation.
func TestDownloadNativePackage(t *testing.T) {
	source := os.Getenv("MUJICAGENT_INSTALL_TEST")
	if source == "" {
		t.Skip("explicit download test only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	lookupSource := source
	if source == "cn-npm" {
		lookupSource = "cn"
	}
	release, err := Latest(ctx, lookupSource)
	if err != nil {
		t.Fatal(err)
	}
	installSource := source
	if source == "cn-npm" {
		installSource = "cn"
	}
	if source == "cn" {
		installSource = "native-mirror"
	}
	path, err := Install(ctx, t.TempDir(), installSource, release, Installation{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("verified %s at %s", release.Version, path)
}

func TestAgentProtocol(t *testing.T) {
	for _, tc := range []struct {
		driver, line, want string
		fail               bool
	}{
		{"pi", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"你好"}}`, "你好", false},
		{"pi", `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`, "done", false},
		{"pi", `{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"bad model"}}`, "", true},
		{"pi", `broken`, "", true},
		{"pi", `{"type":"tool_execution_end","result":{"content":[{"type":"text","text":"file"}]}}`, "", false},
	} {
		p := agentParser{driver: tc.driver}
		text := ""
		err := p.line([]byte(tc.line), func(e AgentEvent) { text = e.Text })
		if (err != nil) != tc.fail || text != tc.want {
			t.Fatalf("%s: %q %v", tc.line, text, err)
		}
	}
	for _, driver := range []string{"pi", "droid"} {
		args, err := agentArgs(RunOptions{Driver: driver, Session: "owned-session", Model: "custom-model"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, arg := range args {
			if arg == "custom-model" {
				found = true
			}
			if arg == "--skip-permissions-unsafe" {
				t.Fatal("unsafe default")
			}
		}
		if !found {
			t.Fatal("model omitted")
		}
	}
}

func TestModelCatalogParsing(t *testing.T) {
	pi, err := parseModelCatalog("pi", []byte(`{"models":[{"provider":"a","id":"shared","name":"A","reasoning":true,"thinkingLevelMap":{"low":"x","high":"y"},"headers":{"Authorization":"secret"}},{"provider":"b","id":"shared","name":"B"}]}`))
	if err != nil || len(pi) != 2 || pi[0].ID != "a/shared" || pi[1].ID != "b/shared" || len(pi[0].Efforts) != 3 {
		t.Fatalf("Pi catalog: %+v %v", pi, err)
	}
	droid, err := parseModelCatalog("droid", []byte(`{"models":[{"id":"custom:Mine-0","displayName":"Mine","supportedReasoningEfforts":["low","high"],"defaultReasoningEffort":"high"},{"id":"blocked","disabled":true}]}`))
	if err != nil || len(droid) != 1 || droid[0].ID != "custom:Mine-0" || droid[0].DefaultEffort != "high" {
		t.Fatalf("Droid catalog: %+v %v", droid, err)
	}
	for _, driver := range []string{"pi", "droid"} {
		args, err := agentArgs(RunOptions{Driver: driver, Effort: "high"})
		if err != nil {
			t.Fatal(err)
		}
		flag := "--thinking"
		if driver == "droid" {
			flag = "--reasoning-effort"
		}
		found := false
		for i, arg := range args {
			if arg == flag && i+1 < len(args) && args[i+1] == "high" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing effort: %v", args)
		}
	}
}

func TestModelDiscoveryProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	for _, driver := range []string{"pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "agent")
			request := "get_available_models"
			response := `{"id":"models","success":true,"data":{"models":[{"provider":"test","id":"model"}]}}`
			if driver == "droid" {
				request = "droid.list_models"
				response = `{"id":"models","result":{"models":[{"id":"model"}]}}`
			}
			script := "#!/bin/sh\nread request\ncase \"$request\" in\n*" + request + "*) printf '%s\\n' '" + response + "';;\n*) exit 2;;\nesac\nsleep 20\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			models, err := DiscoverModels(ctx, driver, binary, dir)
			if err != nil || len(models) != 1 {
				t.Fatalf("discovery: %+v %v", models, err)
			}
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nsleep 20\n"), 0700); err != nil {
				t.Fatal(err)
			}
			timeout, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer stop()
			if _, err := DiscoverModels(timeout, driver, binary, dir); err == nil {
				t.Fatal("timeout ignored")
			}
		})
	}
}

func TestInstalledModelCatalog(t *testing.T) {
	if os.Getenv("MUJICAGENT_MODEL_DISCOVERY_TEST") != "1" {
		t.Skip("explicit read-only local CLI discovery")
	}
	for _, driver := range []string{"pi", "droid"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		models, err := DiscoverModels(ctx, driver, driver, t.TempDir())
		cancel()
		if err != nil {
			t.Fatal(driver, err)
		}
		t.Logf("%s: %d models", driver, len(models))
	}
}

func TestAgentPermissionsAndImages(t *testing.T) {
	args, err := agentArgs(RunOptions{Driver: "pi"})
	if err != nil || !strings.Contains(strings.Join(args, " "), "--no-extensions --no-mcp --tools read,grep,find,ls") {
		t.Fatal(args, err)
	}
	if _, err := agentArgs(RunOptions{Driver: "pi", Access: "invalid"}); err == nil {
		t.Fatal("unknown access accepted")
	}
	path := filepath.Join(t.TempDir(), "red.png")
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	f, _ := os.Create(path)
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	parts, err := droidPrompt(RunOptions{Prompt: "color?", Images: []string{path}})
	if err != nil || len(parts) != 2 {
		t.Fatal(parts, err)
	}
	part := parts[1].(map[string]string)
	data, _ := os.ReadFile(path)
	if part["mimeType"] != "image/png" || part["data"] != base64.StdEncoding.EncodeToString(data) {
		t.Fatal("image lost")
	}
	var output bytes.Buffer
	a := droidACP{in: json.NewEncoder(&output), out: bufio.NewScanner(strings.NewReader(`{"jsonrpc":"2.0","id":"approval","method":"session/request_permission"}
{"jsonrpc":"2.0","id":1,"result":{}}
`))}
	if err := a.call("session/set_mode", map[string]string{"modeId": "normal"}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"outcome":"cancelled"`) {
		t.Fatal(output.String())
	}
}

func TestDroidACPNotifications(t *testing.T) {
	for _, method := range []string{"session/load", "session/prompt"} {
		t.Run(method, func(t *testing.T) {
			var output bytes.Buffer
			var text string
			a := droidACP{in: json.NewEncoder(&output), session: "s", emit: func(e AgentEvent) { text += e.Text }, out: bufio.NewScanner(strings.NewReader(`{"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","content":[{"type":"content"}]}}}
{"method":"session/update","params":{"sessionId":"other","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"wrong"}}}}
{"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer"}}}}
{"id":1,"result":{"stopReason":"end_turn"}}
`))}
			if err := a.call(method, map[string]any{}, nil); err != nil {
				t.Fatal(err)
			}
			want := ""
			if method == "session/prompt" {
				want = "answer"
			}
			if text != want {
				t.Fatal(text)
			}
		})
	}
	for _, response := range []string{`{"id":1,"error":{"code":-32602}}`, "", `broken`} {
		a := droidACP{in: json.NewEncoder(&bytes.Buffer{}), out: bufio.NewScanner(strings.NewReader(response))}
		if err := a.call("session/new", map[string]any{}, nil); err == nil {
			t.Fatal("failure accepted", response)
		}
	}
}

func TestInstalledAgentTurns(t *testing.T) {
	if os.Getenv("MUJICAGENT_AGENT_LIVE_TEST") != "1" {
		t.Skip("explicit paid Pi/Droid test")
	}
	for _, driver := range []string{"pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			dir := t.TempDir()
			o := RunOptions{Driver: driver, Binary: driver, Cwd: dir, Prompt: "Read proof.txt and report its exact contents. Do not modify any files.", Effort: "low"}
			if err := os.WriteFile(filepath.Join(dir, "proof.txt"), []byte("MujicProof719"), 0600); err != nil {
				t.Fatal(err)
			}
			o.Model = "magpie/workbuddy/deepseek-v4.1-flash"
			if driver == "pi" {
				o.Session = filepath.Join(dir, "session.jsonl")
			} else {
				o.Model = "custom:magpie/codex-fast/gpt-6.1-sol"
				img := image.NewRGBA(image.Rect(0, 0, 64, 64))
				for y := 0; y < 64; y++ {
					for x := 0; x < 64; x++ {
						img.Set(x, y, color.RGBA{R: 255, A: 255})
					}
				}
				path := filepath.Join(dir, "image.png")
				f, _ := os.Create(path)
				png.Encode(f, img)
				f.Close()
				o.Images = []string{path}
				o.Prompt += " Also identify the dominant color of the attached image."
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			var answer string
			emit := func(e AgentEvent) {
				if e.Delta {
					answer += e.Text
				} else {
					answer = e.Text
				}
				if e.Session != "" {
					o.Session = e.Session
				}
			}
			if err := Run(ctx, o, emit); err != nil {
				t.Fatal(err)
			}
			t.Log(driver, answer)
			if !strings.Contains(answer, "MujicProof719") {
				t.Fatal("file not read")
			}
			if driver == "droid" && !strings.Contains(strings.ToLower(answer), "red") {
				t.Fatal("image not recognized")
			}
			o.Images = nil
			o.Prompt = "Without reading files again, repeat the proof token from our previous turn."
			answer = ""
			if err := Run(ctx, o, emit); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(answer, "MujicProof719") {
				t.Fatal("session not restored", answer)
			}
			o.Prompt = "Create a file named forbidden.txt containing hello in the current directory. Use a tool to do it."
			answer = ""
			if err := Run(ctx, o, emit); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "forbidden.txt")); !os.IsNotExist(err) {
				t.Fatal("read-only permission allowed writing", err)
			}
			t.Log(driver, "read-only write blocked", answer)

		})
	}
}
