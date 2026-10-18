package frontmatter

import "testing"

func TestParse(t *testing.T) {
	doc := Parse("---\ndescription: Review code\nmodel: \"anthropic/claude\"\n---\nYou review code.")
	if doc.Fields["description"] != "Review code" {
		t.Fatalf("description = %q", doc.Fields["description"])
	}
	if doc.Fields["model"] != "anthropic/claude" {
		t.Fatalf("model = %q", doc.Fields["model"])
	}
	if doc.Body != "You review code." {
		t.Fatalf("body = %q", doc.Body)
	}
}

func TestParseWithoutFrontmatter(t *testing.T) {
	doc := Parse("Just a prompt.\n")
	if doc.Body != "Just a prompt." {
		t.Fatalf("body = %q", doc.Body)
	}
	if len(doc.Fields) != 0 {
		t.Fatalf("fields = %v", doc.Fields)
	}
}

func TestParseUnclosedFrontmatter(t *testing.T) {
	doc := Parse("---\ndescription: x\nbody")
	if doc.Body != "---\ndescription: x\nbody" {
		t.Fatalf("body = %q", doc.Body)
	}
}

func TestList(t *testing.T) {
	items := List("[read, grep, 'glob']")
	if len(items) != 3 || items[0] != "read" || items[2] != "glob" {
		t.Fatalf("items = %v", items)
	}
}
