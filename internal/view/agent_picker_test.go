package view

import (
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentCompactScreenshots(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, scene := range []string{"settings-codex", "settings-pi", "settings-droid", "picker-codex", "picker-pi", "picker-droid"} {
			t.Run(lang+"-"+scene, func(t *testing.T) {
				s := model.New()
				s.Settings.Language = lang
				thread := s.AddThread("", "")
				thread.Turns = []model.Turn{{Prompt: "Implement the next step", Status: "completed", Items: []model.Item{{Kind: "agentMessage", Text: "The implementation is ready for review."}}}}
				c := controller.New(t.TempDir(), s, nil)
				c.Connection = "Connected"
				c.Models = []codex.Model{{Model: "gpt-6.1-sol"}, {Model: "gpt-6-astra"}, {Model: "gpt-6.1-terra"}}
				v := New(c)
				if scene[:8] == "settings" {
					v.Settings = true
					v.agentSection = scene[9:]
					if v.agentSection == "codex" {
						*v.openState("harness-codex") = true
					}
				} else {
					v.modelPicker = true
					v.pickerHarness = scene[7:]
				}
				tt := ui.NewTester(v.Render, 840, 560)
				tt.Frame()
				tt.Advance(time.Second)
				tt.Frame()
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
