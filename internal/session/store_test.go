package session

import (
	"errors"
	"os"
	"testing"
)

func TestSessionStoreRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := New("/tmp/project")
	sess.Agent = "build"
	sess.Model = "mock/small"

	sess.AddUserMessage("hello world")
	assistant := sess.AddAssistantMessage("mock/small", "mock")
	assistant.AddPart(Part{Type: PartText, Text: "hi there"})

	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "hello world" {
		t.Fatalf("title = %q", loaded.Title)
	}
	if len(loaded.Messages) != 2 || loaded.Messages[1].Text() != "hi there" {
		t.Fatalf("messages = %+v", loaded.Messages)
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != sess.ID {
		t.Fatalf("list = %+v", list)
	}

	if err := store.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(sess.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected not exist, got %v", err)
	}
}

func TestLatestFiltersByDirectory(t *testing.T) {
	store := NewStore(t.TempDir())

	a := New("/a")
	a.AddUserMessage("a")
	if err := store.Save(a); err != nil {
		t.Fatal(err)
	}
	b := New("/b")
	b.AddUserMessage("b")
	b.UpdatedAt = b.UpdatedAt.Add(1)
	if err := store.Save(b); err != nil {
		t.Fatal(err)
	}

	latest, err := store.Latest("/a")
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != a.ID {
		t.Fatalf("latest = %s", latest.ID)
	}
}

func TestUndo(t *testing.T) {
	sess := New("/tmp")
	sess.AddUserMessage("first")
	sess.AddAssistantMessage("m", "p")
	sess.AddUserMessage("second")

	text, ok := sess.Undo()
	if !ok || text != "second" {
		t.Fatalf("undo = %q ok=%v", text, ok)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("messages = %d", len(sess.Messages))
	}
	if sess.Title != "first" {
		t.Fatalf("title after undo = %q", sess.Title)
	}

	text, ok = sess.Undo()
	if !ok || text != "first" {
		t.Fatalf("undo2 = %q ok=%v", text, ok)
	}
	if _, ok := sess.Undo(); ok {
		t.Fatal("expected nothing to undo")
	}
}

func TestMessageToolCalls(t *testing.T) {
	msg := &Message{ID: "m", Role: "assistant"}
	msg.AddPart(Part{Type: PartText, Text: "checking"})
	msg.AddPart(Part{Type: PartTool, Tool: &ToolCall{ID: "t1", Name: "bash"}})
	msg.AddPart(Part{Type: PartTool, Tool: &ToolCall{ID: "t2", Name: "read"}})

	calls := msg.ToolCalls()
	if len(calls) != 2 || calls[0].Name != "bash" || calls[1].Name != "read" {
		t.Fatalf("calls = %+v", calls)
	}
	if msg.Text() != "checking" {
		t.Fatalf("text = %q", msg.Text())
	}
}

func TestUsageAdd(t *testing.T) {
	u := Usage{Input: 1, Output: 2}
	u.Add(Usage{Input: 10, Output: 20, Cost: 0.5})
	if u.Input != 11 || u.Output != 22 || u.Cost != 0.5 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestDeriveTitle(t *testing.T) {
	if got := DeriveTitle("  hello\nworld  "); got != "hello world" {
		t.Fatalf("got %q", got)
	}
	long := ""
	for i := 0; i < 100; i++ {
		long += "x"
	}
	got := DeriveTitle(long)
	if len([]rune(got)) != 63 {
		t.Fatalf("len = %d (%q)", len([]rune(got)), got)
	}
}
