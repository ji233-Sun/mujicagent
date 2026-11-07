package view

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func TestOutputPreview(t *testing.T) {
	for _, text := range []string{"", "short", strings.Repeat("line\n", 20), strings.Repeat("中", 10000)} {
		preview, truncated := outputPreview(text)
		if len(preview) > outputPreviewBytes || strings.Count(preview, "\n") >= outputPreviewLines || !utf8.ValidString(preview) {
			t.Fatalf("invalid preview: %d bytes", len(preview))
		}
		if !strings.HasPrefix(text, preview) || truncated != (preview != text) {
			t.Fatal("preview changed the source or truncation flag")
		}
	}
}

func TestOutputHighlightAndUpdates(t *testing.T) {
	tokens, state := commandOutputLanguage.Tokenizer.Tokenize("$ echo \"hello\"", 0, nil)
	found := false
	for _, token := range tokens {
		found = found || token.Kind == editor.TokenString
	}
	if !found {
		t.Fatal("command string is not highlighted")
	}
	tokens, _ = commandOutputLanguage.Tokenizer.Tokenize("ordinary output", state, nil)
	if len(tokens) != 1 || tokens[0].Kind != editor.TokenText {
		t.Fatal("log treated as shell")
	}
	tokens, _ = commandOutputLanguage.Tokenizer.Tokenize("+added", state, nil)
	if tokens[0].Kind != editor.TokenInserted {
		t.Fatal("diff not highlighted")
	}
	s := outputState{editor: editor.State{Buffer: editor.NewBuffer("")}}
	for _, text := range []string{"a\r", "a\r\n", "a\r\nb", "replacement"} {
		s.update(text)
		if got := s.editor.Buffer.String(); got != strings.ReplaceAll(text, "\r\n", "\n") {
			t.Fatalf("buffer = %q", got)
		}
		version := s.editor.Buffer.Version()
		s.update(text)
		if s.editor.Buffer.Version() != version {
			t.Fatal("unchanged frame rewrote buffer")
		}
	}
}

func TestActivityOutputDisclosure(t *testing.T) {
	v := New(controller.New(t.TempDir(), model.New(), nil))
	item := model.Item{Kind: "commandExecution", Text: "$ echo hello\n" + strings.Repeat("output\n", 100)}
	tt := ui.NewTester(func(c *ui.Context) {
		v.activityOutput(c, &item, "test")
	}, 700, 500)
	tt.Frame()
	if _, ok := tt.Find(v.texts().ExpandOutput()); !ok {
		t.Fatal("missing expand")
	}
	if *v.openState("output-test") {
		t.Fatal("expanded by default")
	}
	if err := tt.Click(v.texts().ExpandOutput()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !*v.openState("output-test") {
		t.Fatal("did not expand")
	}
	item.Text += "streamed tail\n"
	tt.Frame()
	if err := tt.Click(v.texts().ActivityOutput()); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != item.Text {
		t.Fatal("expanded output did not retain the complete streamed content")
	}
	tt.Type("must not edit")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != item.Text {
		t.Fatal("output was editable")
	}
	if err := tt.Click(v.texts().CollapseOutput()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if *v.openState("output-test") {
		t.Fatal("did not collapse")
	}
	item.Text = "short"
	tt.Frame()
	if _, ok := tt.Find(v.texts().ExpandOutput()); ok {
		t.Fatal("short output has disclosure")
	}
}

func BenchmarkOutputPreview(b *testing.B) {
	for _, lines := range []int{100, 100000} {
		b.Run(fmt.Sprint(lines), func(b *testing.B) {
			text := strings.Repeat("ordinary output line\n", lines)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				outputPreview(text)
			}
		})
	}
}

func BenchmarkActivityOutputFrame(b *testing.B) {
	for _, expanded := range []bool{false, true} {
		for _, lines := range []int{100, 100000} {
			b.Run(fmt.Sprintf("expanded=%v/lines=%d", expanded, lines), func(b *testing.B) {
				v := New(controller.New(b.TempDir(), model.New(), nil))
				*v.openState("output-test") = expanded
				item := model.Item{Kind: "commandExecution", Text: "$ echo hello\n" + strings.Repeat("ordinary output line\n", lines)}
				tt := ui.NewTester(func(c *ui.Context) { v.activityOutput(c, &item, "test") }, 700, 500)
				tt.Frame()
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					tt.Frame()
				}
			})
		}
	}
}

func TestCompactAgentSettings(t *testing.T) {
	for _, language := range []string{"zh-Hans", "en"} {
		for _, size := range [][2]int{{840, 560}, {1140, 780}, {1882, 1311}} {
			t.Run(fmt.Sprintf("%s-%dx%d", language, size[0], size[1]), func(t *testing.T) {
				v := New(controller.New(t.TempDir(), model.New(), nil))
				v.C.State.Settings.Language = language
				v.Settings = true
				*v.openState("harness-codex") = true
				tt := ui.NewTester(v.Render, size[0], size[1])
				tt.SetLocale(language)
				tt.Frame()
				tt.Advance(time.Second)
				panel, ok := tt.Find(v.texts().SettingsPanel())
				if !ok {
					t.Fatal("missing settings")
				}
				wantW, wantH := min(float64(size[0])*0.96, 1200), min(float64(size[1])*0.90, 720)
				if math.Abs(float64(panel.W)-wantW) > 1 || math.Abs(float64(panel.H)-wantH) > 1 ||
					math.Abs(float64(panel.X)-(float64(size[0])-wantW)/2) > 1 || math.Abs(float64(panel.Y)-(float64(size[1])-wantH)/2) > 1 {
					t.Fatalf("settings should stay centered and respect the size cap: %+v", panel)
				}
				for _, label := range []string{v.texts().CheckForUpdates(), v.texts().Connect(), v.texts().HarnessAuto(), v.texts().AgentPath(), v.texts().CodexDefaultPermissions()} {
					bounds, ok := tt.Find(label)
					if !ok || bounds.X < panel.X || bounds.Y < panel.Y || bounds.X+bounds.W > panel.X+panel.W || bounds.Y+bounds.H > panel.Y+panel.H {
						t.Fatalf("%s outside compact settings: %+v panel=%+v", label, bounds, panel)
					}
				}
				if tt.HasText("安装到应用私有目录") || tt.HasText("Installs privately") {
					t.Fatal("removed installation prose is still visible")
				}
				if output := os.Getenv("MUJICAGENT_SETTINGS_SCREENSHOTS"); output != "" {
					if err := os.MkdirAll(output, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(output, fmt.Sprintf("%s-%dx%d.png", language, size[0], size[1])))
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
	v := New(controller.New(t.TempDir(), model.New(), nil))
	v.Settings = true
	tt := ui.NewTester(func(c *ui.Context) { v.settingsDialog(c) }, 1000, 700)
	tt.Frame()
	if _, ok := tt.Find(v.texts().HarnessDetails()); !ok {
		t.Fatal("missing agent disclosure")
	}
	if err := tt.Click(v.texts().HarnessDetails()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if !*v.openState("harness-codex") {
		t.Fatal("details not expanded")
	}
	if err := tt.Click(v.texts().HarnessAuto()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !v.C.State.Settings.DisableHarnessAutoCheck {
		t.Fatal("automatic update preference not saved")
	}
	for _, section := range []string{"Appearance", "Updates", "Logs", "Agents"} {
		v.section = section
		tt.Frame()
		tt.Frame()
	}
}
