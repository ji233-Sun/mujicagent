package view

import (
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

func TestAgentCompactScreenshots(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, scene := range []string{"settings-codex", "settings-pi", "settings-droid", "picker-codex", "picker-pi", "picker-droid", "picker-favorites", "picker-loading", "picker-error"} {
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
				if strings.HasPrefix(scene, "settings") {
					v.Settings = true
					v.agentSection = scene[9:]
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
				tt := ui.NewTester(v.Render, 840, 560)
				tt.Frame()
				tt.Advance(time.Second)
				tt.Frame()
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
				for _, name := range []string{"Codex", "Pi", "Droid"} {
					b, ok := tt.Find(name)
					if !ok || b.W < 24 || b.X < 0 || b.X+b.W > 840 {
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
