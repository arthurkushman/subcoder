package permission

import (
	"context"
	"errors"
	"testing"
)

func TestDecisionDefaults(t *testing.T) {
	engine := New(nil, nil, false)
	cases := map[string]Decision{
		"read":      Allow,
		"glob":      Allow,
		"bash":      Ask,
		"write":     Ask,
		"edit":      Ask,
		"webfetch":  Ask,
		"unknown-x": Ask,
	}
	for tool, want := range cases {
		if got := engine.Decision(tool, ""); got != want {
			t.Errorf("Decision(%q) = %v, want %v", tool, got, want)
		}
	}
}

func TestDecisionRules(t *testing.T) {
	rules := map[string]any{
		"bash": map[string]any{
			"git push*": string(Deny),
			"git *":     string(Allow),
			"*":         string(Ask),
		},
		"write": string(Allow),
	}
	engine := New(rules, nil, false)

	cases := []struct {
		tool  string
		value string
		want  Decision
	}{
		{"bash", "git status", Allow},
		{"bash", "git push --force", Deny},
		{"bash", "rm -rf /", Ask},
		{"write", "/etc/hosts", Allow},
	}
	for _, tc := range cases {
		if got := engine.Decision(tc.tool, tc.value); got != tc.want {
			t.Errorf("Decision(%q, %q) = %v, want %v", tc.tool, tc.value, got, tc.want)
		}
	}
}

func TestSpecificPatternWins(t *testing.T) {
	rules := map[string]any{
		"bash": map[string]any{
			"*":            "ask",
			"git push*":    "deny",
			"git push foo": "allow",
		},
	}
	engine := New(rules, nil, false)
	if got := engine.Decision("bash", "git push foo"); got != Allow {
		t.Fatalf("got %v want allow", got)
	}
	if got := engine.Decision("bash", "git push bar"); got != Deny {
		t.Fatalf("got %v want deny", got)
	}
}

func TestCheckAutoApprove(t *testing.T) {
	engine := New(nil, nil, true)
	if err := engine.Check(context.Background(), Request{Tool: "bash", Pattern: "rm -rf x"}); err != nil {
		t.Fatalf("auto approve: %v", err)
	}
}

func TestCheckNonInteractiveDeniesAsk(t *testing.T) {
	engine := New(nil, nil, false)
	err := engine.Check(context.Background(), Request{Tool: "bash", Pattern: "ls"})
	var denied *DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("err = %v", err)
	}
	if err := engine.Check(context.Background(), Request{Tool: "read", Pattern: "/x"}); err != nil {
		t.Fatalf("read should be allowed: %v", err)
	}
}

type recordingAsker struct {
	replies map[string]Reply
	asked   []Request
}

func (r *recordingAsker) Ask(ctx context.Context, req Request) (Reply, error) {
	r.asked = append(r.asked, req)
	return r.replies[req.Tool], nil
}

func TestCheckAskAndRemember(t *testing.T) {
	asker := &recordingAsker{replies: map[string]Reply{"bash": {Allowed: true, Always: true}}}
	engine := New(nil, asker, false)

	if err := engine.Check(context.Background(), Request{Tool: "bash", Pattern: "make test"}); err != nil {
		t.Fatal(err)
	}
	if len(asker.asked) != 1 {
		t.Fatalf("asked = %d", len(asker.asked))
	}

	if err := engine.Check(context.Background(), Request{Tool: "bash", Pattern: "make test"}); err != nil {
		t.Fatal(err)
	}
	if len(asker.asked) != 1 {
		t.Fatalf("expected remembered permission, asked = %d", len(asker.asked))
	}
}

func TestCheckDeniedByUser(t *testing.T) {
	asker := &recordingAsker{replies: map[string]Reply{"bash": {Allowed: false}}}
	engine := New(nil, asker, false)
	err := engine.Check(context.Background(), Request{Tool: "bash", Pattern: "ls"})
	var denied *DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckDeniedByConfig(t *testing.T) {
	engine := New(map[string]any{"bash": "deny"}, nil, true)
	err := engine.Check(context.Background(), Request{Tool: "bash", Pattern: "ls"})
	var denied *DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("err = %v", err)
	}
}
