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

func TestCombinedAnnotationsRoundTrip(t *testing.T) {
	a := Attachment{Kind: "image", Path: filepath.Join(t.TempDir(), "图.png"), Annotations: []Annotation{
		{ID: "one", Comment: "Align these\n&<> 中文", Marks: []Mark{
			{Kind: "rectangle", Color: "#07896d", Width: 3, Points: []ImagePoint{{10, 20}, {80, 90}}},
			{Kind: "arrow", Color: "#e05263", Width: 4, Points: []ImagePoint{{25, 25}, {50, 50}}},
		}},
		{ID: "two", Comment: "Whole image"},
	}}
	parts := MessageParts(a.Link())
	if len(parts) != 1 || !reflect.DeepEqual(parts[0].Attachment, &a) {
		t.Fatalf("round trip: %+v", parts)
	}
	if !ValidAnnotations(a.Annotations) {
		t.Fatal("valid annotations rejected")
	}
	a.Annotations[0].Marks[0].Points[0].X = 101
	parts = MessageParts(a.Link())
	if len(parts) != 1 || parts[0].Attachment != nil {
		t.Fatal("out of bounds annotation accepted")
	}
	a.Annotations[0].Marks[0].Points[0].X = 10
	a.Annotations[1].ID = "one"
	if ValidAnnotations(a.Annotations) {
		t.Fatal("duplicate annotation IDs accepted")
	}
	legacy := Attachment{Tip: "Old note", X: 25, Y: 75}
	if len(legacy.Notes()) != 1 || legacy.Notes()[0].Marks[0].Points[0].Y != 75 {
		t.Fatal("legacy note lost")
	}
}
