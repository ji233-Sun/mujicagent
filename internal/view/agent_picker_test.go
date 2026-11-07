package view

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestHarnessInstallScreenshots(t *testing.T) {
	scenes := []string{
		"vp-current", "vp-update", "vp-confirm", "npm-confirm", "brew-confirm",
		"managed-official-confirm", "managed-npm-confirm", "managed-mirror-confirm",
		"unknown", "missing", "install-official", "install-npm", "install-mirror", "install-npm-fallback",
		"preparing", "preview-error", "installing", "install-error", "cancelled", "running",
		"bun-confirm", "pnpm-confirm", "unsupported-manager", "manager-unavailable", "manager-unverified", "manager-ambiguous",
	}
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, scene := range scenes {
			t.Run(lang+"/"+scene, func(t *testing.T) {
				s := model.New()
				s.Settings.Language = lang
				s.Settings.HarnessSource = "cn-native" // Legacy global selection must not drive updates.
				thread := s.AddThread("", "")
				c := controller.New(t.TempDir(), s, nil)
				c.Connection = "Connected"
				u := c.HarnessFor("codex")
				u.Binary = s.Settings.CodexPath
				u.CheckedAt = time.Now()
				u.Installation = harness.Installation{Path: "/Users/demo/.vite-plus/bin/codex", Version: "0.167.0", Method: "vp", Prefix: "/Users/demo/.vite-plus/bin/vp"}
				u.Release = harness.Release{Version: "0.168.0", URL: "https://github.com/openai/codex/releases/download/rust-v0.168.0/codex-package-aarch64-apple-darwin.tar.gz", Digest: "sha256:" + strings.Repeat("a1", 32), Entry: "bin/codex"}
				v := New(c)
				v.Settings = true
				*v.openState("harness-codex") = true
				fresh := strings.HasPrefix(scene, "install-") && scene != "install-error"
				if fresh || scene == "missing" {
					u.Installation = harness.Installation{}
					u.Missing = true
					c.Connection = "Disconnected"
				}
				switch scene {
				case "vp-current":
					u.Release.Version = u.Installation.Version
				case "npm-confirm":
					u.Installation.Method = "npm"
					u.Installation.Path = "/usr/local/bin/codex"
					u.Installation.Prefix = "/usr/local"
				case "bun-confirm", "pnpm-confirm":
					id := strings.TrimSuffix(scene, "-confirm")
					u.Installation.Method = id
					u.Installation.Path = "/Users/demo/." + id + "/bin/codex"
					u.Installation.Manager = "/Users/demo/." + id + "/bin/" + id
					u.Installation.PackageRoot = "/Users/demo/." + id + "/global/node_modules"
				case "unsupported-manager":
					u.Installation.Method = "vlt"
					u.Installation.UpdateIssue = "unsupported"
					u.Installation.Path = "/Users/demo/.vlt/bin/codex"
				case "manager-unavailable", "manager-unverified":
					u.Installation.Method = "pnpm"
					u.Installation.UpdateIssue = strings.TrimPrefix(scene, "manager-")
					u.Installation.Path = "/Users/demo/.local/share/pnpm/codex"
				case "manager-ambiguous":
					u.Installation.Method = "ambiguous"
					u.Installation.UpdateIssue = "ambiguous"
					u.Release = harness.Release{} // Warning must survive a failed version lookup.
				case "brew-confirm":
					u.Installation.Method = "brew"
					u.Installation.Path = "/opt/homebrew/bin/codex"
					u.Installation.Prefix = "/opt/homebrew"
				case "unknown":
					u.Installation.Method = "custom"
					u.Installation.Path = "/opt/custom/codex"
				case "running":
					c.Running[thread.ID] = true
				case "installing":
					u.Installing = true
					c.Connection = "Disconnected"
				case "install-error":
					u.Error = "installation failed: exit status 1"
					c.Connection = "Disconnected"
				}
				stage := "/Users/demo/Library/Application Support/MujicAgent/tools/codex/release-0.168.0-preview"
				if strings.HasPrefix(scene, "managed-") {
					u.Installation.Method = "managed"
					u.Installation.Path = strings.Replace(stage, "0.168.0-preview", "0.167.0-old", 1) + "/bin/codex"
				}
				if strings.HasSuffix(scene, "-confirm") || fresh || scene == "preparing" || scene == "preview-error" {
					v.harnessDialog = true
					p := &harness.InstallPlan{Current: u.Installation, Release: u.Release, Path: u.Installation.Path}
					u.Plan = p
					switch u.Installation.Method {
					case "vp":
						p.Command, p.Args = u.Installation.Prefix, []string{"update", "--global", "@openai/codex@0.168.0"}
					case "npm":
						p.Command = "/usr/local/bin/npm"
						p.Args = []string{"install", "--global", "--prefix", "/usr/local", "@openai/codex@0.168.0", "--ignore-scripts", "--no-audit", "--no-fund"}
					case "brew":
						p.Command, p.Args = "/opt/homebrew/bin/brew", []string{"upgrade", "--cask", "codex"}
					case "bun", "pnpm":
						p.Command, p.Args = u.Installation.Manager, []string{"add", "--global", "@openai/codex@0.168.0", "--ignore-scripts"}
					default:
						p.Stage, p.Path = stage, stage+"/bin/codex"
						if strings.Contains(scene, "mirror") || strings.Contains(scene, "npm") {
							p.Source = "native-mirror"
							p.Release.URL = "https://registry.npmmirror.com/@openai/codex/-/codex-0.168.0-darwin-arm64.tgz"
							p.Release.Digest = "sha512-" + strings.Repeat("aB12", 22)
							p.Path = stage + "/package/vendor/aarch64-apple-darwin/bin/codex"
							v.harnessSource = "native-mirror"
						}
						if strings.Contains(scene, "npm") {
							v.harnessSource = "cn"
							if scene != "install-npm-fallback" {
								p.Source = "cn"
								p.Command = "/usr/local/bin/npm"
								p.Args = []string{"install", "--prefix", stage, "@openai/codex@0.168.0", "--registry", "https://registry.npmmirror.com", "--ignore-scripts", "--no-audit", "--no-fund"}
								p.Path = stage + "/node_modules/.bin/codex"
							}
						}
					}
					if scene == "preparing" {
						u.Plan, u.Preparing = nil, true
					} else if scene == "preview-error" {
						u.Plan = nil
						u.Error = "download HTTP 503"
					}
				}
				tt := ui.NewTester(v.Render, 1060, 720)
				tt.Frame()
				tt.Advance(time.Second)
				tt.Frame()
				tx := v.texts()
				if tt.HasText(tx.HarnessSource()) || tt.HasText(tx.HarnessManagedInstall()) {
					t.Fatal("legacy channel or managed migration control remains")
				}
				for _, label := range []string{tx.HarnessOfficial(), tx.HarnessCN(), tx.HarnessNativeMirror()} {
					if tt.HasText(label) != fresh {
						t.Fatalf("install choice visibility: %s, fresh=%v", label, fresh)
					}
				}
				if v.harnessDialog {
					label := tx.HarnessConfirmUpdate()
					if fresh {
						label = tx.HarnessConfirmInstall()
					}
					b, ok := tt.Find(label)
					if !ok || b.X < 0 || b.Y < 0 || b.X+b.W > 1060 || b.Y+b.H > 720 {
						t.Fatalf("confirmation clipped: %+v", b)
					}
					if u.Plan != nil {
						expected := u.Plan.CommandPreview()
						if expected == "" {
							expected = tx.HarnessNativeAction()
						}
						if !tt.HasText(expected) || !tt.HasText(u.Plan.Path) {
							t.Fatal("preview omitted command/action or path")
						}
					}
				}
				if scene == "unknown" && !tt.HasText(tx.HarnessUnknownManager()) {
					t.Fatal("unknown manager missing guidance")
				}
				if u.Installation.Path != "" && !u.Installation.CanUpdate() {
					if slices.Contains(tt.Texts(), tx.HarnessUpdate()) {
						t.Fatal("unsupported installation offers an update")
					}
					message := tx.HarnessUnsupportedManager("vlt")
					switch scene {
					case "unknown":
						message = tx.HarnessUnknownManager()
					case "manager-unavailable":
						message = tx.HarnessManagerUnavailable("pnpm")
					case "manager-unverified":
						message = tx.HarnessManagerUnverified("pnpm")
					case "manager-ambiguous":
						message = tx.HarnessManagerAmbiguous()
					}
					b, ok := tt.Find(message)
					if !ok || b.X+b.W > 1060 || b.Y+b.H > 720 {
						t.Fatalf("unsupported warning missing or clipped: %+v", b)
					}
				}
				if scene == "missing" && !tt.HasText(tx.HarnessInstall()) {
					t.Fatal("missing install entry")
				}
				if output := os.Getenv("MUJICAGENT_INSTALL_SCREENSHOTS"); output != "" {
					if err := os.MkdirAll(output, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(output, lang+"-"+scene+".png"))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, tt.Image()); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestAgentCompactScreenshots(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, scene := range []string{"settings-codex", "settings-codex-running", "settings-pi", "settings-droid", "picker-codex", "picker-pi", "picker-droid", "picker-favorites", "picker-loading", "picker-error", "picker-many", "picker-search-empty", "picker-favorites-empty", "permissions-pi", "permissions-droid"} {
			t.Run(lang+"-"+scene, func(t *testing.T) {
				s := model.New()
				s.Settings.Language = lang
				thread := s.AddThread("", "")
				thread.Turns = []model.Turn{{Prompt: "Implement the next step", Status: "completed", Items: []model.Item{{Kind: "agentMessage", Text: "The implementation is ready for review."}}}}
				c := controller.New(t.TempDir(), s, nil)
				c.Connection = "Connected"
				c.Models = []codex.Model{{Model: "gpt-6.1-sol"}, {Model: "gpt-6-astra"}, {Model: "gpt-6.1-terra"}}
				for _, id := range []string{"pi", "droid"} {
					h := c.HarnessFor(id)
					h.ModelsChecked = true
					h.Installation.Version = "1.5.0"
					h.Models = []harness.AgentModel{
						{ID: "gpt-6.1-sol", Name: "GPT-6.1 Sol", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"},
						{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "high"},
						{ID: "custom:Local-0", Name: "Local model"},
					}
				}
				s.Settings.ModelFavorites = []model.ModelFavorite{{Harness: "codex", ModelID: "gpt-6-astra"}, {Harness: "pi", ModelID: "gpt-6.1-sol"}, {Harness: "droid", ModelID: "gpt-6.1-sol"}}
				v := New(c)
				if strings.HasPrefix(scene, "permissions-") {
					thread.Harness = strings.TrimPrefix(scene, "permissions-")
					v.permissionTarget = thread.ID
					v.permissionPicker = true
				} else if strings.HasPrefix(scene, "settings") {
					v.Settings = true
					v.agentSection = strings.TrimSuffix(scene[9:], "-running")
					if scene == "settings-codex-running" {
						c.Running[thread.ID] = true
						h := c.HarnessFor("codex")
						h.Installation.Version = "0.167.0"
						h.Installation.Method = "custom"
						h.Release.Version = "0.167.0"
					}
					if v.agentSection != "" {
						*v.openState("harness-" + v.agentSection) = true
					}
				} else {
					v.modelPicker = true
					v.pickerHarness = scene[7:]
					if v.pickerHarness == "pi" || v.pickerHarness == "droid" {
						thread.Harness = v.pickerHarness
						thread.AgentModel = "gpt-6.1-sol"
						thread.AgentEffort = "medium"
					}
					if v.pickerHarness == "loading" || v.pickerHarness == "error" {
						v.pickerHarness = "pi"
						h := c.HarnessFor("pi")
						h.Models = nil
						h.ModelsLoading = scene == "picker-loading"
						if !h.ModelsLoading {
							h.ModelsError = "CLI unavailable"
						}
					}
				}
				switch scene {
				case "picker-many":
					v.pickerHarness = "pi"
					for i := 0; i < 20; i++ {
						c.HarnessFor("pi").Models = append(c.HarnessFor("pi").Models, harness.AgentModel{ID: fmt.Sprintf("extra-%02d", i)})
					}
				case "picker-search-empty":
					v.pickerHarness = "codex"
					v.modelSearch = "no matching model"
				case "picker-favorites-empty":
					v.pickerHarness = "favorites"
					s.Settings.ModelFavorites = nil
				}
				tt := ui.NewTester(v.Render, 840, 560)
				tt.Frame()
				tt.Advance(time.Second)
				tt.Frame()
				if strings.HasPrefix(scene, "picker-") {
					panel, ok := tt.Find(v.texts().ModelPicker())
					if !ok || panel.H != 360 || panel.Y < 0 || panel.Y+panel.H > 560 {
						t.Fatalf("model picker must stay 360px tall within the viewport: %+v", panel)
					}
					list, ok := tt.Find(v.texts().ModelList())
					if !ok || list.H < 280 || list.Y+list.H > panel.Y+panel.H-8 {
						t.Fatalf("model list must fill the available panel space: %+v", list)
					}
					if scene == "picker-many" && v.modelList.AtEnd() {
						t.Fatal("long catalog must overflow inside the scrollable list")
					}
				}
				if scene == "settings-codex-running" {
					if !tt.HasText(v.texts().UpToDate()) || tt.HasText(v.texts().UpdateAfterConversation()) {
						t.Fatal("running Codex settings must show update status without the conversation hint")
					}
				}
				for _, removed := range []string{"Default model", "默认模型", "Model ID", "模型 ID", "Custom", "自定义"} {
					if slices.Contains(tt.Texts(), removed) {
						t.Fatalf("removed model control is visible: %s", removed)
					}
				}
				if scene == "settings-pi" || scene == "settings-droid" {
					if slices.Contains(tt.Texts(), v.texts().Models()) {
						t.Fatal("settings still exposes model selection")
					}
				}
				if scene == "picker-pi" || scene == "picker-droid" {
					for _, label := range []string{"gpt-6.1-sol", "claude-sonnet-4-6", "custom:Local-0", v.texts().SearchModels(), v.texts().AdjustReasoningEffort()} {
						if _, ok := tt.Find(label); !ok {
							t.Fatalf("missing model control: %s", label)
						}
					}
				}
				names := []string{"Codex", "Pi", "Droid"}
				if strings.HasPrefix(scene, "permissions-") {
					names = []string{v.texts().ReadOnly(), v.texts().FullAccess()}
					if thread.Driver() == "droid" {
						names = append(names, v.texts().AgentFileEdits(), v.texts().AgentDevelopment())
					}
				}
				for _, name := range names {
					b, ok := tt.Find(name)
					if !ok || b.W < 20 || b.X < 0 || b.X+b.W > 840 {
						t.Fatalf("missing/clipped %s: %+v", name, b)
					}
				}
				if output := os.Getenv("MUJICAGENT_AGENTS_SCREENSHOTS"); output != "" {
					if err := os.MkdirAll(output, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(output, lang+"-"+scene+".png"))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, tt.Image()); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestModelFavoriteOrderingAndHarnessIdentity(t *testing.T) {
	s := model.New()
	s.Settings.Language = "en"
	s.AddThread("", "")
	c := controller.New(t.TempDir(), s, nil)
	v := New(c)
	models := []codex.Model{{Model: "first"}, {Model: "shared"}}
	s.ToggleModelFavorite(model.ModelFavorite{Harness: "pi", ModelID: "shared"})
	tt := ui.NewTester(func(ctx *ui.Context) { v.modelCatalogPanel(ctx, "droid", models, "", func(codex.Model) {}, nil) }, 500, 300)
	tt.Frame()
	first, _ := tt.Find("first")
	shared, _ := tt.Find("shared")
	if first.Y >= shared.Y {
		t.Fatal("Pi favorite reordered Droid")
	}
	star, ok := tt.Find("Favorite Droid · shared")
	if !ok {
		t.Fatal("favorite action missing")
	}
	tt.ClickAt(star.X+star.W/2, star.Y+star.H/2)
	tt.Frame()
	first, _ = tt.Find("first")
	shared, _ = tt.Find("shared")
	if first.Y <= shared.Y {
		t.Fatal("favorite was not pinned")
	}
	if c.State.Thread(c.State.Selected).Driver() != "codex" {
		t.Fatal("favorite changed active harness")
	}
	v.pickerHarness = "favorites"
	all := ui.NewTester(func(ctx *ui.Context) { v.favoriteModelPanel(ctx) }, 500, 300)
	all.Frame()
	for _, label := range []string{"Pi · shared", "Droid · shared"} {
		if _, ok := all.Find(label); !ok {
			t.Fatal("missing distinct tuple", label)
		}
	}
	s.ToggleModelFavorite(model.ModelFavorite{Harness: "pi", ModelID: "shared"})
	all.Frame()
	if _, ok := all.Find("Pi · shared"); ok {
		t.Fatal("unfavorite remains visible")
	}
	if _, ok := all.Find("Droid · shared"); !ok {
		t.Fatal("unfavorite removed other harness")
	}
	target, _ := all.Find("Droid · shared")
	all.ClickAt(target.X+target.W/2, target.Y+target.H/2)
	all.Frame()
	if thread := s.Thread(s.Selected); thread.Driver() != "droid" || thread.AgentModel != "shared" {
		t.Fatal("favorite selected the wrong harness/model")
	}

	s.Settings.Effort, s.Settings.ServiceTier = "ultra", "fast"
	v.chooseModel(codex.Model{Model: "codex-favorite", DefaultReasoningEffort: "medium", SupportedReasoningEfforts: []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	}{{ReasoningEffort: "medium"}}})
	if s.Thread(s.Selected).Driver() != "codex" || s.Settings.Model != "codex-favorite" || s.Settings.Effort != "medium" || s.Settings.ServiceTier != "" {
		t.Fatal("Codex favorite did not apply compatible model options after handoff")
	}

}

func TestModelPickerWithoutCodexCatalog(t *testing.T) {
	s := model.New()
	s.Settings.Language = "en"
	s.AddThread("", "")
	c := controller.New(t.TempDir(), s, nil)
	v := New(c)
	tt := ui.NewTester(func(ctx *ui.Context) { v.modelSelector(ctx) }, 600, 400)
	tt.Frame()
	button, ok := tt.Find(v.texts().ModelAndEffort())
	if !ok {
		t.Fatal("missing model selector")
	}
	tt.ClickAt(button.X+button.W/2, button.Y+button.H/2)
	tt.Frame()
	if !v.modelPicker {
		t.Fatal("empty Codex catalog blocked the model picker")
	}
	for _, name := range []string{"Pi", "Droid"} {
		if _, ok := tt.Find(name); !ok {
			t.Fatalf("%s is unavailable without Codex models", name)
		}
	}

}
