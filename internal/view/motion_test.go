package view

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func saveDesignFrame(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	out := os.Getenv("MUJICAGENT_DESIGN_SCREENSHOTS")
	if out == "" {
		return
	}
	if err := os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(out, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(file, tt.Image())
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("encode frame: %v, %v", err, closeErr)
	}
}

// Contact sheets retain original pixels, without scaling or synthesizing frames.
// Tiles are ordered by the timestamps in their filename, left to right.
func saveMotionStrip(t *testing.T, scene, name string, frames []string) {
	t.Helper()
	out := os.Getenv("MUJICAGENT_DESIGN_SCREENSHOTS")
	if out == "" {
		return
	}
	crop := image.Rect(12, 90, 234, 380)
	switch scene {
	case "activity":
		crop = image.Rect(265, 140, 1110, 380)
	case "popover":
		crop = image.Rect(260, 430, 600, 650)
	case "settings":
		crop = image.Rect(18, 32, 350, 230)
	}
	columns := len(frames)
	if scene == "activity" {
		columns = 2
	}
	sheet := image.NewRGBA(image.Rect(0, 0, columns*crop.Dx(), ((len(frames)+columns-1)/columns)*crop.Dy()))
	for i, frame := range frames {
		file, err := os.Open(filepath.Join(out, frame+".png"))
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		dst := image.Rect((i%columns)*crop.Dx(), (i/columns)*crop.Dy(), (i%columns+1)*crop.Dx(), (i/columns+1)*crop.Dy())
		draw.Draw(sheet, dst, img, crop.Min, draw.Src)
	}
	file, err := os.Create(filepath.Join(out, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(file, sheet)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("write strip: %v, %v", err, closeErr)
	}
}

// Direct presentation/model state plus the Tester's clock, with no input events.
// These sequences prove CPU transition behavior, not native frame pacing.
func TestWorkspaceMotion(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		for _, scene := range []string{"project", "activity", "popover", "settings"} {
			t.Run(fmt.Sprintf("%s-reduced-%v", scene, reduced), func(t *testing.T) {
				s := model.New()
				s.Settings.Language = "en"
				s.Projects = []model.Project{{ID: "workspace", Name: "MujicAgent", Expanded: false}, {ID: "docs", Name: "Documentation", Expanded: true}}
				thread := s.AddThread("workspace", "")
				thread.Title = "Review existing transitions"
				thread.Turns = []model.Turn{{Prompt: "Keep reading position stable.", Status: "completed", Items: []model.Item{{ID: "tool", Kind: "commandExecution", Text: "$ go test ./internal/view\nok   internal/view", Status: "completed"}}}}
				for i := 0; i < 3; i++ {
					other := s.AddThread("workspace", "")
					other.Title = fmt.Sprintf("Conversation %d", i+1)
				}
				s.Selected = thread.ID
				ctrl := controller.New(t.TempDir(), s, nil)
				defer ctrl.Close()
				ctrl.Connection = "Connected"
				v := New(ctrl)
				v.section = "Appearance"
				setOpen := func(open bool) {
					switch scene {
					case "project":
						s.Projects[0].Expanded = open
					case "activity":
						*v.openState("activity-" + thread.ID + "-0-0") = open
					case "popover":
						v.projectPicker = open
					case "settings":
						v.Settings = open
					}
				}
				tt := ui.NewTester(v.Render, 1140, 780)
				tt.SetLocale("en")
				tt.SetPreferences(ui.Preferences{ReduceMotion: reduced})
				tt.Advance(time.Second)
				prefix := fmt.Sprintf("motion-%s-reduced-%v", scene, reduced)
				saveDesignFrame(t, tt, prefix+"-baseline")
				composer, _ := tt.Find(v.texts().Message())
				for _, phase := range []struct {
					name string
					open bool
				}{{"open", true}, {"close", false}} {
					setOpen(phase.open)
					tt.Frame()
					probe := "Documentation"
					switch scene {
					case "activity":
						probe = "$ go test ./internal/view"
					case "popover":
						probe = v.texts().SearchProjects()
					case "settings":
						probe = v.texts().SettingsPanel()
					}
					startBounds, startFound := tt.Find(probe)
					startPixels := append([]byte(nil), tt.Image().Pix...)
					previous := time.Duration(0)
					var positions []float32
					for _, elapsed := range []time.Duration{0, 40, 80, 120, 180, 240, 320} {
						tt.Advance((elapsed - previous) * time.Millisecond)
						tt.Frame() // render at the exact recorded clock, not the last refresh
						previous = elapsed
						saveDesignFrame(t, tt, fmt.Sprintf("%s-%s-%03dms", prefix, phase.name, elapsed))
						docs, _ := tt.Find("Documentation")
						positions = append(positions, docs.Y)
						t.Logf("%s %s %dms: following project y=%.2f", prefix, phase.name, elapsed, docs.Y)
						if after, _ := tt.Find(v.texts().Message()); after != composer {
							t.Fatalf("%s moved the composer: before=%+v after=%+v", scene, composer, after)
						}
					}
					if scene == "settings" && !reduced && bytes.Equal(startPixels, tt.Image().Pix) {
						t.Fatal("settings must have an observed transition rather than switch instantaneously")
					}
					if scene == "project" && !reduced {
						first, middle, last := positions[0], positions[2], positions[len(positions)-1]
						if phase.open && !(first < middle && middle < last) || !phase.open && !(first > middle && middle > last) {
							t.Fatalf("project rows must transition with their disclosure: %v", positions)
						}
						for i := 1; i < len(positions); i++ {
							if phase.open && positions[i] < positions[i-1] || !phase.open && positions[i] > positions[i-1] {
								t.Fatalf("project transition reversed unexpectedly: %v", positions)
							}
						}
					}
					endBounds, endFound := tt.Find(probe)
					// Reduced motion suppresses movement; short color feedback is
					// allowed. Compare geometry/presence rather than all pixels.
					if reduced && (startFound != endFound || startBounds != endBounds) {
						t.Fatalf("reduced-motion geometry changed: before=%+v/%v after=%+v/%v", startBounds, startFound, endBounds, endFound)
					}
					var frames []string
					for _, ms := range []int{0, 80, 180, 320} {
						frames = append(frames, fmt.Sprintf("%s-%s-%03dms", prefix, phase.name, ms))
					}
					saveMotionStrip(t, scene, prefix+"-"+phase.name+"-frames-000-080-180-320", frames)
					settled := append([]byte(nil), tt.Image().Pix...)
					tt.Advance(400 * time.Millisecond)
					if !bytes.Equal(settled, tt.Image().Pix) {
						t.Fatal("transition continues changing after its settling interval")
					}
				}
				// Reverse an opening transition before it finishes; no input event.
				setOpen(true)
				tt.Frame()
				tt.Advance(80 * time.Millisecond)
				tt.Frame()
				saveDesignFrame(t, tt, prefix+"-reverse-before")
				beforeReverse, _ := tt.Find("Documentation")
				beforeReversePixels := append([]byte(nil), tt.Image().Pix...)
				setOpen(false)
				tt.Frame()
				if (scene == "project" || scene == "settings") && !reduced {
					if after, _ := tt.Find("Documentation"); math.Abs(float64(after.Y-beforeReverse.Y)) > 1 {
						t.Fatalf("reversing disclosure caused a jump: before=%+v after=%+v", beforeReverse, after)
					}
					if !bytes.Equal(beforeReversePixels, tt.Image().Pix) {
						t.Fatal("same-clock reversal changed appearance before animation advanced")
					}
				}
				saveDesignFrame(t, tt, prefix+"-reverse-000ms")
				tt.Advance(80 * time.Millisecond)
				tt.Frame()
				saveDesignFrame(t, tt, prefix+"-reverse-080ms")
				tt.Advance(320 * time.Millisecond)
				tt.Frame()
				saveDesignFrame(t, tt, prefix+"-reverse-end")
				saveMotionStrip(t, scene, prefix+"-reverse-frames-before-000-080-end", []string{prefix + "-reverse-before", prefix + "-reverse-000ms", prefix + "-reverse-080ms", prefix + "-reverse-end"})
			})
		}
	}
}

func TestSettingsReducedMotionPreference(t *testing.T) {
	ctrl := controller.New(t.TempDir(), model.New(), nil)
	defer ctrl.Close()
	ctrl.State.Settings.Language = "en"
	v := New(ctrl)
	v.section = "Appearance"
	tt := ui.NewTester(func(c *ui.Context) { v.theme(c); v.settingsDialog(c) }, 1140, 780)
	tt.Frame()
	v.Settings = true
	tt.Frame()
	tt.Advance(40 * time.Millisecond)
	tt.Frame()
	saveDesignFrame(t, tt, "settings-preference-normal-040ms")
	// Change the actual framework preference while the animation is moving.
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	immediate := append([]byte(nil), tt.Image().Pix...)
	saveDesignFrame(t, tt, "settings-preference-reduced-immediate")
	tt.Advance(320 * time.Millisecond)
	tt.Frame()
	if !bytes.Equal(immediate, tt.Image().Pix) {
		t.Fatal("ReduceMotion preference did not settle the in-flight settings transition immediately")
	}
	v.Settings = false
	tt.Frame()
	if tt.HasText(v.texts().SettingsPanel()) {
		t.Fatal("reduced-motion settings retained an exiting modal")
	}
	immediate = append([]byte(nil), tt.Image().Pix...)
	saveDesignFrame(t, tt, "settings-preference-reduced-close-immediate")
	tt.Advance(320 * time.Millisecond)
	tt.Frame()
	if !bytes.Equal(immediate, tt.Image().Pix) {
		t.Fatal("reduced-motion dismissal continued animating")
	}
}

func TestWorkspaceSimilarNames(t *testing.T) {
	for _, width := range []int{840, 1366} {
		for selected := range 2 {
			s := model.New()
			s.Settings.Language = "en"
			s.Projects = []model.Project{{ID: "workspace", Name: "MujicAgent", Expanded: true}}
			titles := []string{"Review desktop integration renderer / ARM64", "Review desktop integration storage / ARM64"}
			for _, title := range titles {
				thread := s.AddThread("workspace", "")
				thread.Title = title
				thread.Turns = []model.Turn{{Prompt: "Keep the distinguishing part of the conversation name available.", Status: "completed"}}
			}
			s.Selected = s.Threads[selected].ID
			ctrl := controller.New(t.TempDir(), s, nil)
			v := New(ctrl)
			ctrl.Connection = "Connected"
			tt := ui.NewTester(v.Render, width, 858)
			tt.SetLocale("en")
			tt.Advance(time.Second)
			tt.Frame()
			for _, title := range titles {
				if !tt.HasText(title) {
					t.Fatalf("complete name unavailable in navigation metadata: %q", title)
				}
			}
			saveDesignFrame(t, tt, fmt.Sprintf("similar-names-%d-selected-%d", width, selected))
			ctrl.Close()
		}
	}
}

func TestActivityEllipsisPreservesTargets(t *testing.T) {
	for _, item := range []model.Item{
		{Kind: "fileChange", Text: "/workspace/config/account-settings.production.json"},
		{Kind: "fileChange", Text: `C:\workspace\config\account-settings.production.json`},
		{Kind: "toolCall", MCP: &model.MCPCall{Tool: "read", Arguments: `{"path":"/workspace/config/account-settings.production.json"}`}},
		{Kind: "toolCall", MCP: &model.MCPCall{Tool: "Read", Arguments: `{"file_path":"/workspace/config/account-settings.production.json"}`}},
	} {
		label := item.Text
		if nativeTool(&item) {
			label = toolActivityText(&item, false)
		}
		if got := activityEllipsis(&item, label); got != "…/account-settings.production.json" {
			t.Fatalf("target suffix lost for %q: %q", label, got)
		}
	}
	if got := tailEllipsis("名称含有重要标识-ARM64", 8); !strings.HasSuffix(got, "ARM64") {
		t.Fatalf("conversation suffix lost: %q", got)
	}
	for _, name := range []string{"renderer", "storage"} {
		title := "Review desktop integration " + name + " / ARM64"
		if got := tailEllipsis(title, 18); got != "…"+name+" / ARM64" {
			t.Fatalf("distinguishing word lost: %q", got)
		}
	}
	if got := tailEllipsis("A longer conversation title that must truncate safely", 8); got != "…safely" {
		t.Fatalf("readable trailing word was split: %q", got)
	}
}

// Theme-token checks cover the color distinction, not pointer event routing.
func TestControlPaletteContrast(t *testing.T) {
	ctrl := controller.New(t.TempDir(), model.New(), nil)
	defer ctrl.Close()
	v := New(ctrl)
	var theme ui.Theme
	tt := ui.NewTester(func(c *ui.Context) {
		v.theme(c)
		theme = *c.Theme()
	}, 320, 240)
	tt.Frame()
	luminance := func(c ui.Color) float64 {
		linear := func(value uint8) float64 {
			v := float64(value) / 255
			if v <= 0.04045 {
				return v / 12.92
			}
			return math.Pow((v+0.055)/1.055, 2.4)
		}
		return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
	}
	contrast := func(a, b ui.Color) float64 {
		x, y := luminance(a), luminance(b)
		return (max(x, y) + 0.05) / (min(x, y) + 0.05)
	}
	for _, bg := range []ui.Color{theme.Background, theme.Surface, theme.SurfaceHover} {
		if ratio := contrast(theme.Text, bg); ratio < 4.5 {
			t.Fatalf("body text contrast %.2f:1 is below 4.5:1", ratio)
		}
		if ratio := contrast(theme.TextMuted, bg); ratio < 4.5 {
			t.Fatalf("ordinary secondary text contrast %.2f:1 is below 4.5:1", ratio)
		}
		t.Logf("text %.2f:1; secondary %.2f:1; accent %.2f:1; accent hover %.2f:1", contrast(theme.Text, bg), contrast(theme.TextMuted, bg), contrast(theme.Accent, bg), contrast(theme.AccentHover, bg))
	}
	if theme.Accent == theme.AccentHover || theme.AccentHover == theme.AccentPressed || theme.SurfaceHover == theme.SurfacePressed {
		t.Fatal("interactive color states are indistinguishable")
	}
	for _, bg := range []ui.Color{theme.Accent, theme.AccentHover, theme.AccentPressed} {
		if ratio := contrast(theme.AccentText, bg); ratio < 3 {
			t.Fatalf("primary action icon contrast %.2f:1 is below 3:1", ratio)
		}
	}
}

// Unusual names, paths and errors remain intact in labels/details even where
// the visual preview must truncate. Focus is requested directly via a Handle.
func TestWorkspaceDesignStates(t *testing.T) {
	for _, scene := range []string{"long-path", "long-error", "disabled", "focus", "selected", "loading", "code"} {
		t.Run(scene, func(t *testing.T) {
			s := model.New()
			s.Settings.Language = "en"
			s.Settings.Model = "gpt-6.1-sol"
			s.Projects = []model.Project{{ID: "workspace", Name: "MujicAgent", Expanded: true}}
			thread := s.AddThread("workspace", "")
			thread.Title = "Review-name-with-important-suffix-ARM64"
			thread.Draft = "Keep the existing design language."
			path := "/workspace/projects/desktop-client/internal/configuration/providers/account-settings.production.json"
			item := model.Item{ID: "read", Kind: "fileChange", Text: path + "\n-old value\n+new value", Status: "completed"}
			thread.Turns = []model.Turn{{Prompt: "Check names, paths and status details.", Status: "completed", Items: []model.Item{item}}}
			ctrl := controller.New(t.TempDir(), s, nil)
			defer ctrl.Close()
			ctrl.Connection = "Connected"
			v := New(ctrl)
			*v.openState("activity-" + thread.ID + "-0-0") = true
			if scene == "code" {
				*v.openState(thread.ID + "-0-0-0") = true
				thread.Turns[0].Items[0].Kind = "commandExecution"
				thread.Turns[0].Items[0].Text = "$ go test ./internal/view\n+ added configuration\n- removed configuration\n" + strings.Repeat("long output with spaces and identifiers: key=value ", 8)
			}
			if scene == "long-error" {
				ctrl.Error = "Connection failed for " + path + ": EACCES — permission denied. Retry after correcting the file permissions."
			}
			if scene == "disabled" {
				thread.Draft = ""
			}
			if scene == "loading" {
				ctrl.Running[thread.ID] = true
				thread.Turns[0].Status = "inProgress"
				thread.Turns[0].Items[0].Status = "inProgress"
			}
			for _, width := range []int{840, 1366} {
				focus := scene == "focus"
				tt := ui.NewTester(func(c *ui.Context) {
					v.Render(c)
					if focus {
						v.editorHandle.Focus(c)
					}
				}, width, 858)
				tt.SetLocale("en")
				tt.Advance(time.Second)
				tt.Frame()
				if focus && !tt.Focused(v.texts().Message()) {
					t.Fatal("direct focus request did not reach composer")
				}
				if scene == "long-path" && !tt.HasText(path) {
					t.Fatal("full path was lost from accessible tool label")
				}
				if scene == "long-error" && !tt.HasText(ctrl.Error) {
					t.Fatal("error details lost their code or final instructions")
				}
				saveDesignFrame(t, tt, fmt.Sprintf("states-%s-en-%dx858", scene, width))
			}
		})
	}
}
