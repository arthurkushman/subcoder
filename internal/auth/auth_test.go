package auth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arthurkushman/subcoder/internal/config"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.Set("anthropic", Entry{Key: "sk-ant"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai", Entry{Key: "sk-oai"}); err != nil {
		t.Fatal(err)
	}

	entry, ok := store.Get("anthropic")
	if !ok || entry.Key != "sk-ant" || entry.Type != "api" {
		t.Fatalf("entry = %+v ok=%v", entry, ok)
	}

	names := store.Providers()
	if len(names) != 2 || names[0] != "anthropic" || names[1] != "openai" {
		t.Fatalf("providers = %v", names)
	}

	if err := store.Delete("openai"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get("openai"); ok {
		t.Fatal("openai should be deleted")
	}

	info, err := os.Stat(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %v", info.Mode().Perm())
	}
}

func TestKeyResolutionOrder(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	t.Setenv(config.EnvKey("custom"), "env-key")
	if got := store.Key("custom", config.ProviderConfig{}); got != "env-key" {
		t.Fatalf("env key = %q", got)
	}

	if got := store.Key("custom", config.ProviderConfig{APIKey: "cfg-key"}); got != "cfg-key" {
		t.Fatalf("config key = %q", got)
	}

	if err := store.Set("custom", Entry{Key: "store-key"}); err != nil {
		t.Fatal(err)
	}
	if got := store.Key("custom", config.ProviderConfig{}); got != "store-key" {
		t.Fatalf("store key = %q", got)
	}

	t.Setenv("OPENAI_API_KEY", "fallback")
	if got := store.Key("openai", config.ProviderConfig{}); got != "fallback" {
		t.Fatalf("openai fallback = %q", got)
	}
}
