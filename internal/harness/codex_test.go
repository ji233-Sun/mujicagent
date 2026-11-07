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
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLocalManagerPreviewAndExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixtures; native Windows routing is checked separately")
	}
	for _, method := range []string{"vp", "npm", "brew"} {
		t.Run(method, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "工具 with spaces")
			bin := filepath.Join(root, "bin")
			if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(root, "args")
			version := filepath.Join(root, "version")
			t.Setenv("HARNESS_TEST_ARGS", log)
			t.Setenv("HARNESS_TEST_VERSION", version)
			if err := os.WriteFile(version, []byte("0.167.0"), 0600); err != nil {
				t.Fatal(err)
			}
			program := "#!/bin/sh\nif [ \"${0##*/}\" = codex ]; then\n if [ \"$1\" = --version ]; then printf 'codex-cli '; cat \"$HARNESS_TEST_VERSION\"; printf '\\n'; fi\n exit 0\nfi\nprintf '%s\\n' \"$@\" > \"$HARNESS_TEST_ARGS\"\nprintf '0.168.0' > \"$HARNESS_TEST_VERSION\"\n"
			manager := method
			managerPath := filepath.Join(bin, manager)
			if err := os.WriteFile(managerPath, []byte(program), 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(bin, "codex")
			if err := os.Symlink(managerPath, path); err != nil {
				t.Fatal(err)
			}
			current := Installation{Path: path, Version: "0.167.0", Method: method, Prefix: root}
			if method == "vp" {
				current.Prefix = managerPath
				inspected, err := Inspect(context.Background(), path, filepath.Join(root, "managed"))
				if err != nil || inspected != current {
					t.Fatalf("VP launcher: %+v %v", inspected, err)
				}
			}
			plan, err := PrepareInstall(context.Background(), filepath.Join(root, "managed"), "", Release{Version: "0.168.0"}, current)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatal("preview ran updater")
			}
			if plan.Stage != "" || plan.Path != path || !strings.Contains(plan.CommandPreview(), "'"+managerPath+"'") {
				t.Fatalf("wrong preview: %+v", plan)
			}
			want := []string{"update", "--global", "@openai/codex@0.168.0"}
			if method == "npm" {
				want = []string{"install", "--global", "--prefix", root, "@openai/codex@0.168.0", "--ignore-scripts", "--no-audit", "--no-fund"}
			} else if method == "brew" {
				want = []string{"upgrade", "--cask", "codex"}
			}
			if !slices.Equal(plan.Args, want) {
				t.Fatal(plan.Args)
			}
			result, err := plan.Execute(context.Background())
			if err != nil || result != path {
				t.Fatal(result, err)
			}
			args, err := os.ReadFile(log)
			if err != nil || string(args) != strings.Join(want, "\n")+"\n" {
				t.Fatalf("executed another command: %q %v", args, err)
			}
		})
	}
}

func TestInstallSourcePolicy(t *testing.T) {
	root := t.TempDir()
	release := Release{Version: "0.168.0", URL: "https://example.test/codex.tar.gz", Digest: "sha256:" + strings.Repeat("0", 64), Entry: "bin/codex"}
	for _, source := range []string{"", "native-mirror"} {
		current := Installation{Path: filepath.Join(root, "old", "bin", "codex"), Method: "managed", Source: source}
		p, err := PrepareInstall(context.Background(), root, "cn", release, current)
		if err != nil || p.Source != source || p.Command != "" || p.Stage == "" {
			t.Fatalf("changed managed source: %+v %v", p, err)
		}
		if _, err := os.Stat(p.Stage); !os.IsNotExist(err) {
			t.Fatal("preview created installation")
		}
		// A failed download must leave the old path and remove only its new stage.
		p.Release.Digest = "missing"
		if _, err := p.Execute(context.Background()); err == nil {
			t.Fatal("accepted missing checksum")
		}
		if _, err := os.Stat(p.Stage); !os.IsNotExist(err) {
			t.Fatal("failed stage retained")
		}
	}
	if _, err := PrepareInstall(context.Background(), root, "", release, Installation{Path: "/custom/codex", Method: "custom"}); err == nil {
		t.Fatal("unknown install silently migrated")
	}
	if _, err := PrepareInstall(context.Background(), root, "bad", release, Installation{}); err == nil {
		t.Fatal("accepted unknown source")
	}
}

func TestManagedSourceInspection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	root := t.TempDir()
	stage := filepath.Join(root, "release-old")
	bin := filepath.Join(stage, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'codex-cli 0.167.0\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"", "cn", "native-mirror"} {
		data, _ := json.Marshal(source)
		if err := os.WriteFile(filepath.Join(stage, "source.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		current, err := Inspect(context.Background(), path, root)
		if err != nil || current.Method != "managed" || current.Source != source {
			t.Fatal(current, err)
		}
	}
}

func TestManagedNpmExecutionPreservesSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX npm fixture")
	}
	root := t.TempDir()
	manager := filepath.Join(root, "npm")
	stage := filepath.Join(root, "managed", "release-next")
	script := "#!/bin/sh\nmkdir -p \"$2/node_modules/.bin\"\nprintf '#!/bin/sh\\nif [ \"$1\" = --version ]; then echo codex-cli 0.168.0; fi\\n' > \"$2/node_modules/.bin/codex\"\nchmod 700 \"$2/node_modules/.bin/codex\"\n"
	if err := os.WriteFile(manager, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	p := &InstallPlan{Release: Release{Version: "0.168.0"}, Source: "cn", Stage: stage,
		Path: filepath.Join(stage, "node_modules", ".bin", "codex"), Command: manager, Args: []string{"--prefix", stage}}
	path, err := p.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	current, err := Inspect(context.Background(), path, filepath.Dir(stage))
	if err != nil || current.Method != "managed" || current.Source != "cn" {
		t.Fatal(current, err)
	}
	if _, err := p.Execute(context.Background()); err == nil {
		t.Fatal("reused an existing stage")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("removed the existing installation", err)
	}
}

func TestWindowsNpmRuntimeOutsideGlobalPrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "工具 Program Files")
	prefix := filepath.Join(t.TempDir(), "AppData", "npm")
	cli := filepath.Join(root, "node_modules", "npm", "bin", "npm-cli.js")
	if err := os.MkdirAll(filepath.Dir(cli), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{cli, filepath.Join(root, "npm.cmd"), filepath.Join(root, "node.exe")} {
		if err := os.WriteFile(path, nil, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root)
	node, script, err := windowsNpmRuntime(prefix)
	if err != nil || node != filepath.Join(root, "node.exe") || script != cli {
		t.Fatal(node, script, err)
	}
	if err := os.Remove(cli); err != nil {
		t.Fatal(err)
	}
	if _, _, err := windowsNpmRuntime(prefix); err == nil {
		t.Fatal("accepted missing npm runtime")
	}
}

func TestInstalledCodexManager(t *testing.T) {
	if os.Getenv("MUJICAGENT_CODEX_INSPECT_TEST") != "1" {
		t.Skip("explicit read-only local inspection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	current, err := Inspect(ctx, "codex", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if current.Method != "vp" {
		t.Fatalf("expected local VP installation: %+v", current)
	}
	plan, err := PrepareInstall(ctx, t.TempDir(), "", Release{Version: current.Version}, current)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(plan.CommandPreview())
}

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
	if !a.permissionDenied || !strings.Contains(output.String(), `"outcome":"cancelled"`) {
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
			if err := Run(ctx, o, emit); err != nil && !strings.Contains(err.Error(), "blocked by the selected permissions") {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "forbidden.txt")); !os.IsNotExist(err) {
				t.Fatal("read-only permission allowed writing", err)
			}
			t.Log(driver, "read-only write blocked", answer)

		})
	}
}
