package model

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestInlineAttachmentRoundTrip(t *testing.T) {
	a := Attachment{Kind: "image", Path: filepath.Join(t.TempDir(), "图 片 #?<>.png"), X: 12.5, Y: 99, Tip: "调整这里\n&<>"}
	text := "开头 " + a.Link() + " 结尾"
	p := MessageParts(text)
	if len(p) != 3 || p[0].Text != "开头 " || p[2].Text != " 结尾" || !reflect.DeepEqual(p[1].Attachment, &a) {
		t.Fatalf("parts: %+v", p)
	}
	state := New()
	thread := state.AddThread("p", t.TempDir())
	thread.Draft = text
	thread.Turns = []Turn{{Prompt: text, Status: "completed"}}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Threads[0].Draft != text || loaded.Threads[0].Turns[0].Prompt != text {
		t.Fatal("attachment lost on reload")
	}
}

func TestMalformedAttachmentIsText(t *testing.T) {
	for _, text := range []string{
		"<mujica://unknown/?path=%2Ftmp%2Fx>",
		"<mujica://image/?path=relative.png>",
		"<mujica://image/?path=%ZZ>",
		"<mujica://image/?path=%2Ftmp%2Fx&tip=note&x=NaN&y=1>",
		"<mujica://image/?path=%2Ftmp%2Fx&tip=note&x=101&y=1>",
		"<mujica://image/?path=%2Ftmp%2Fx&tip=note&x=1>",
	} {
		p := MessageParts(text)
		if len(p) != 1 || p[0].Text != text || p[0].Attachment != nil {
			t.Fatalf("unexpected interpretation: %q", text)
		}
	}
}
