package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTranslateConfigFallsBackToEnv(t *testing.T) {
	// Nothing saved yet: .env.local is still the source, so nothing changes
	// until somebody presses Save.
	t.Setenv("OMNIROUTE_BASE_URL", "http://env:1/")
	t.Setenv("OMNIROUTE_MODEL", "env_model")
	t.Setenv("OMNIROUTE_API_KEY", "sk-envkey1234")

	got := loadTranslateConfig(filepath.Join(t.TempDir(), "nope.json"))
	if got.BaseURL != "http://env:1/" || got.Model != "env_model" || got.APIKey != "sk-envkey1234" {
		t.Fatalf("env ignored: %+v", got)
	}
}

func TestTranslateConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	in := translateConfig{BaseURL: "http://saved:2", Model: "saved_model", APIKey: "sk-abcd5678"}
	if err := saveTranslateConfig(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := loadTranslateConfig(path); got != in {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestTranslateConfigSavedBeatsEnv(t *testing.T) {
	t.Setenv("OMNIROUTE_MODEL", "env_model")
	path := filepath.Join(t.TempDir(), "cfg.json")
	_ = saveTranslateConfig(path, translateConfig{BaseURL: "u", Model: "saved_model", APIKey: "k"})

	if got := loadTranslateConfig(path); got.Model != "saved_model" {
		t.Fatalf("env won over a saved value: %q", got.Model)
	}
}

func TestTranslateConfigKeepsKeyWhenNoneSubmitted(t *testing.T) {
	// The browser never receives the key, so the form cannot send it back. An
	// empty key on save has to mean "leave it alone", or changing the model
	// would wipe the credential.
	path := filepath.Join(t.TempDir(), "cfg.json")
	_ = saveTranslateConfig(path, translateConfig{BaseURL: "u", Model: "m1", APIKey: "sk-keepme1234"})

	merged := mergeSubmittedConfig(loadTranslateConfig(path),
		translateConfig{BaseURL: "u", Model: "m2", APIKey: ""})

	if merged.APIKey != "sk-keepme1234" {
		t.Fatalf("key lost on a model change: %q", merged.APIKey)
	}
	if merged.Model != "m2" {
		t.Fatalf("model not updated: %q", merged.Model)
	}
}

func TestKeyHintShowsOnlyTheTail(t *testing.T) {
	if got := keyHint("sk-0000000000000000-000000-000000abcd"); got != "…4256" {
		t.Fatalf("hint leaks: %q", got)
	}
	if got := keyHint(""); got != "" {
		t.Fatalf("invented a hint: %q", got)
	}
	if got := keyHint("ab"); got != "…" {
		t.Fatalf("short key leaked whole: %q", got)
	}
}

func TestSaveTranslateConfigIsNotWorldReadable(t *testing.T) {
	// It holds a credential.
	path := filepath.Join(t.TempDir(), "cfg.json")
	_ = saveTranslateConfig(path, translateConfig{BaseURL: "u", Model: "m", APIKey: "k"})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("want 0600, got %v", info.Mode().Perm())
	}
}
