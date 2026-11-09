package harness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type downloadTransport func(*http.Request) (*http.Response, error)

func (f downloadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type downloadBody struct {
	io.Reader
	closed bool
}

func (b *downloadBody) Close() error { b.closed = true; return nil }

func TestDownloadResponseRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []int
		wantErr  string
	}{
		{"success", []int{200}, ""},
		{"gateway recovery", []int{504, 502, 200}, ""},
		{"unavailable recovery", []int{503, 200}, ""},
		{"exhausted", []int{504, 504, 504}, "download HTTP 504"},
		{"unauthorized", []int{401}, "download HTTP 401"},
		{"forbidden", []int{403}, "download HTTP 403"},
		{"missing", []int{404}, "download HTTP 404"},
		{"unexpected response", []int{500}, "download HTTP 500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var bodies []*downloadBody
				start := time.Now()
				client := &http.Client{Transport: downloadTransport(func(req *http.Request) (*http.Response, error) {
					if req.Method != "GET" || req.URL.String() != "https://example.com/package.tar.gz" || req.Header.Get("User-Agent") != "MujicAgent" {
						t.Fatal("retry changed the request")
					}
					attempt := len(bodies)
					if attempt >= len(tc.statuses) {
						t.Fatal("unexpected extra request")
					}
					wantDelay := []time.Duration{0, time.Second, 3 * time.Second}[attempt]
					if time.Since(start) != wantDelay {
						t.Fatalf("attempt %d at %s, want %s", attempt, time.Since(start), wantDelay)
					}
					if attempt > 0 && !bodies[attempt-1].closed {
						t.Fatal("previous failure body was not closed")
					}
					body := &downloadBody{Reader: strings.NewReader("package")}
					bodies = append(bodies, body)
					return &http.Response{StatusCode: tc.statuses[attempt], Body: body, Header: make(http.Header)}, nil
				})}
				req, err := http.NewRequestWithContext(t.Context(), "GET", "https://example.com/package.tar.gz", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("User-Agent", "MujicAgent")
				resp, err := downloadResponse(client, req)
				if len(bodies) != len(tc.statuses) {
					t.Fatalf("got %d requests, want %d", len(bodies), len(tc.statuses))
				}
				if tc.wantErr != "" {
					if err == nil || err.Error() != tc.wantErr || resp != nil || !bodies[len(bodies)-1].closed {
						t.Fatalf("failure: response=%v, error=%v", resp, err)
					}
					return
				}
				if err != nil || resp == nil || bodies[len(bodies)-1].closed {
					t.Fatalf("success body unavailable: %v", err)
				}
				defer resp.Body.Close()
				data, err := io.ReadAll(resp.Body)
				if err != nil || string(data) != "package" {
					t.Fatalf("success body changed: %q, %v", data, err)
				}
			})
		})
	}
}

func TestDownloadResponseCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := 0
		client := &http.Client{Transport: downloadTransport(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 504, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		})}
		req, err := http.NewRequestWithContext(ctx, "GET", "https://example.com/package", nil)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			time.Sleep(500 * time.Millisecond)
			cancel()
		}()
		start := time.Now()
		resp, err := downloadResponse(client, req)
		if !errors.Is(err, context.Canceled) || resp != nil || calls != 1 || time.Since(start) != 500*time.Millisecond {
			t.Fatalf("cancel did not stop backoff: response=%v, err=%v, calls=%d, elapsed=%s", resp, err, calls, time.Since(start))
		}
	})
}

func TestDownloadResponseTransportFailure(t *testing.T) {
	calls := 0
	want := fmt.Errorf("TLS certificate rejected")
	client := &http.Client{Transport: downloadTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, want
	})}
	req, err := http.NewRequestWithContext(t.Context(), "GET", "https://example.com/package", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := downloadResponse(client, req)
	if !errors.Is(err, want) || resp != nil || calls != 1 {
		t.Fatalf("transport error was retried or lost: response=%v, err=%v, calls=%d", resp, err, calls)
	}
}
