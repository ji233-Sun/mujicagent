//go:build darwin

package view

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/ji233-Sun/mujicagent/internal/browser"
)

func TestBrowserNativeTrustedCapture(t *testing.T) {
	s := newNativeBrowserScene(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The expected bitmap comes from a separate, explicitly sanitized scene,
	// rendered by MyGo's ordinary native screenshot API, not the new policy.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><head><style>
body { margin:0;background:white;color:black;font:16px monospace }
div {height:30px}
input {width:200px;height:30px;box-sizing:border-box}
</style></head><body><div id="secret">[redacted]</div><input id="password" type="password" value=""></body></html>`)
	}))
	defer server.Close()
	status := s.status(t, s.thread)
	_, err := s.runtime.Open(ctx, browser.OpenRequest{
		Request: browser.Request{Page: status.Page, Actor: browser.ActorHuman}, URL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	status = s.status(t, s.thread)
	native := s.runtime.View(status.Page.PageKey).(*mygo.WebView)
	// Wait for the actual view placement, not a fixed sleep.
	for {
		if value, err := native.Page().EvalContext(ctx, "innerWidth > 100 && innerHeight > 100"); err == nil && value == true {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
	}
	expectedPNG, err := native.CapturePage()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := png.Decode(bytes.NewReader(expectedPNG))
	if err != nil {
		t.Fatal(err)
	}
	// Main-world serialization and prototypes are attacker controlled. The
	// observer tries to put the secret back whenever redaction is observed.
	_, err = native.Page().EvalContext(ctx, `
const secret = document.getElementById("secret"), password = document.getElementById("password");
password.value = "q7"; secret.textContent = "q7 Authorization: Basic Zml4dHVyZTpwYXNzd29yZA==";
new MutationObserver(() => {
  if (secret.textContent !== "q7 Authorization: Basic Zml4dHVyZTpwYXNzd29yZA==")
    secret.textContent = "q7 Authorization: Basic Zml4dHVyZTpwYXNzd29yZA==";
}).observe(document.body, {subtree:true,characterData:true,childList:true});
setInterval(() => Promise.resolve().then(() => {
  secret.textContent = "q7 Authorization: Basic Zml4dHVyZTpwYXNzd29yZA==";
}), 0);
Document.prototype.querySelectorAll = () => [];
JSON.stringify = () => '{"ok":true,"v":"forged"}';
return true;
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.runtime.Resume(ctx, status.Page.PageKey, browser.Permissions{Capture: true})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 30; attempt++ {
		status = s.status(t, s.thread)
		capture, err := s.runtime.Capture(ctx, browser.Request{Page: status.Page, Actor: browser.ActorAgent})
		if err != nil {
			t.Fatalf("trusted capture %d: %v", attempt, err)
		}
		actual, err := png.Decode(bytes.NewReader(capture.PNG))
		if err != nil {
			t.Fatal(err)
		}
		// Human/Agent toolbar text can change the viewport height. This
		// fixture has fixed content in its first 60 CSS pixels; every extra
		// viewport pixel is explicitly white, independent of capture code.
		if actual.Bounds().Dx() != expected.Bounds().Dx() || actual.Bounds().Dy() < 120 {
			t.Fatalf("invalid viewport bounds: %v", actual.Bounds())
		}
		for y := actual.Bounds().Min.Y; y < actual.Bounds().Max.Y; y++ {
			for x := actual.Bounds().Min.X; x < actual.Bounds().Max.X; x++ {
				ar, ag, ab, aa := actual.At(x, y).RGBA()
				want := color.Color(color.White)
				if y < expected.Bounds().Max.Y {
					want = expected.At(x, y)
				}
				er, eg, eb, ea := want.RGBA()
				if ar != er || ag != eg || ab != eb || aa != ea {
					t.Fatalf("redacted viewport differs from independent sanitized scene at %d,%d (iteration %d)", x, y, attempt)
				}
			}
		}
	}
	t.Log("30 product Runtime captures match the independent sanitized native bitmap despite short password, Basic credential, forged JSON/prototype, timer, Promise and MutationObserver")
}

func TestBrowserNativeCaptureRejectsOpaqueContent(t *testing.T) {
	s := newNativeBrowserScene(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cases := map[string]string{
		"closed-shadow":            `<div id="host"></div><script>host.attachShadow({mode:"closed"}).innerHTML="<p>password: fixture-private</p>"</script>`,
		"frame":                    `<iframe srcdoc="<p>password: fixture-private</p>"></iframe>`,
		"canvas":                   `<canvas></canvas>`,
		"image":                    `<img src="data:image/svg+xml,%3Csvg/%3E">`,
		"svg":                      `<svg><text>fixture-private</text></svg>`,
		"custom-element":           `<fixture-element>fixture-private</fixture-element>`,
		"pseudo":                   `<style>p::after{content:"password: fixture-private"}</style><p>safe</p>`,
		"animation":                `<style>@keyframes shift{to{opacity:.5}}p{animation:shift 1s infinite}</style><p>safe</p>`,
		"unlisted-pseudo-resource": `<style>body::-webkit-scrollbar{background:url("data:image/svg+xml,%3Csvg/%3E")}</style>`,
		"image-input":              `<input type="image" src="data:image/svg+xml,%3Csvg/%3E">`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<!doctype html><html><body>"+cases[r.URL.Path[1:]]+"</body></html>")
	}))
	defer server.Close()
	for name := range cases {
		t.Run(name, func(t *testing.T) {
			status := s.status(t, s.thread)
			if status.State != browser.StateHuman {
				if _, err := s.runtime.TakeOver(ctx, status.Page.PageKey); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.runtime.Open(ctx, browser.OpenRequest{Request: browser.Request{Page: status.Page, Actor: browser.ActorHuman}, URL: server.URL + "/" + name})
			if err != nil {
				t.Fatal(err)
			}
			status = s.status(t, s.thread)
			if _, err = s.runtime.Resume(ctx, status.Page.PageKey, browser.Permissions{Capture: true}); err != nil {
				t.Fatal(err)
			}
			capture, err := s.runtime.Capture(ctx, browser.Request{Page: status.Page, Actor: browser.ActorAgent})
			if !errors.Is(err, browser.ErrSensitive) || len(capture.PNG) != 0 {
				t.Fatalf("opaque content released pixels: %v", err)
			}
		})
	}
}
