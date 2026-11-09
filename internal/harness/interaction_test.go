package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDroidRemoteRequestResolution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events := make(chan AgentEvent, 8)
	handlers := newInteractions(ctx, func(e AgentEvent) { events <- e })
	defer handlers.close()
	var output bytes.Buffer
	a := droidRPC{ctx: handlers.ctx, interactions: handlers, emit: handlers.publish, control: &Control{in: json.NewEncoder(&output), driver: "droid"}, streaming: true, session: "session"}
	for _, request := range []string{
		`{"id":"question","method":"droid.ask_user","params":{"toolCallId":"ask","questions":[{"index":1,"question":"Which platform?","options":["macOS"],"optionDescriptions":["Desktop"]}]}}`,
		`{"id":"permission","method":"droid.request_permission","params":{"options":[{"value":"proceed_once"},{"value":"cancel"}]}}`,
	} {
		if err := a.consume([]byte(request)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		select {
		case e := <-events:
			if e.Interaction == nil {
				t.Fatal("request was not published")
			}
			if q := e.Interaction.Request.Questions; len(q) > 0 && q[0].Options[0].Description != "Desktop" {
				t.Fatal("option description lost")
			}
		case <-ctx.Done():
			t.Fatal("reader blocked before publishing both requests")
		}
	}
	for _, notification := range []string{
		`{"type":"ask_user_resolved","requestIds":["question"]}`,
		`{"type":"permission_resolved","requestId":"permission"}`,
	} {
		if err := a.consume([]byte(`{"method":"droid.session_notification","params":{"sessionId":"session","notification":` + notification + `}}`)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		select {
		case e := <-events:
			if e.Resolved == nil {
				t.Fatal("cancelled request not removed")
			}
		case <-ctx.Done():
			t.Fatal("remote cancellation blocked behind user input")
		}
	}
	handlers.close()
	if output.Len() != 0 {
		t.Fatal("responded to an already resolved request")
	}
}

func TestDroidInvalidQuestionReturnsProtocolError(t *testing.T) {
	handlers := newInteractions(context.Background(), func(AgentEvent) { t.Error("invalid request opened a form") })
	defer handlers.close()
	var output bytes.Buffer
	a := droidRPC{ctx: handlers.ctx, interactions: handlers, emit: handlers.publish, control: &Control{in: json.NewEncoder(&output), driver: "droid"}}
	if err := a.consume([]byte(`{"id":"empty","method":"droid.ask_user","params":{"questions":[]}}`)); err != nil {
		t.Fatal(err)
	}
	handlers.workers.Wait()
	if !strings.Contains(output.String(), `"code":-32602`) || context.Cause(handlers.ctx) != nil {
		t.Fatalf("invalid question hung or terminated session: %s", output.String())
	}
}
