package config

import (
	"strings"
	"testing"
)

func TestGeneralDefaults(t *testing.T) {
	ApplyTheme(RetroTheme())
	if Config.General.ChatListMode != "recency_only" {
		t.Fatalf("expected recency_only default")
	}
	if Config.General.EnableDiagnostics {
		t.Fatalf("diagnostics must be off by default")
	}
	if Config.General.Theme != "warm" {
		t.Fatalf("expected default theme warm, got %q", Config.General.Theme)
	}
	if Config.Colors.InputBackground != "#7FA69E" {
		t.Fatalf("expected retro input background #7FA69E, got %q", Config.Colors.InputBackground)
	}
	if Config.Colors.Background != "#1B1E24" {
		t.Fatalf("expected spec background #1B1E24, got %q", Config.Colors.Background)
	}
	if Config.Colors.ListSelected != "#5F6B1E" {
		t.Fatalf("expected spec selected fill #5F6B1E, got %q", Config.Colors.ListSelected)
	}
	if Config.Colors.Timestamp != "#7B818C" {
		t.Fatalf("expected spec timestamp #7B818C, got %q", Config.Colors.Timestamp)
	}
	if Config.Colors.ListHeader != "#F1EBD9" {
		t.Fatalf("expected retro list header #F1EBD9, got %q", Config.Colors.ListHeader)
	}
	if Config.General.Profile != "" {
		t.Fatalf("expected empty default profile, got %q", Config.General.Profile)
	}
	if Config.General.EnablePassphrase {
		t.Fatalf("passphrase lock must be off by default")
	}
	if Config.General.PassphraseHash != "" {
		t.Fatalf("expected empty passphrase hash by default")
	}
}

// "default" must map to the unnamed profile's session.db, not create an
// empty session.default.db — regression for the /profile default bug.
func TestProfileDefaultAlias(t *testing.T) {
	orig := Config.General.Profile
	defer func() { Config.General.Profile = orig }()

	Config.General.Profile = "default"
	p := GetSessionFilePath()
	if strings.HasSuffix(p, "session.default") {
		t.Fatalf("default alias must not create session.default, got %q", p)
	}
	if !strings.HasSuffix(p, "session") {
		t.Fatalf("default alias must resolve to the unnamed session path, got %q", p)
	}

	Config.General.Profile = ""
	if !strings.HasSuffix(GetSessionFilePath(), "session") {
		t.Fatalf("empty profile must resolve to the unnamed session path")
	}

	Config.General.Profile = "kantor"
	if !strings.HasSuffix(GetSessionFilePath(), "session.kantor") {
		t.Fatalf("named profile must resolve to session.kantor")
	}
}

// ProfileDbPath must resolve each profile to its session DB file without
// mutating the global Profile field (callers read it concurrently).
func TestProfileDbPath(t *testing.T) {
	orig := Config.General.Profile
	defer func() { Config.General.Profile = orig }()
	Config.General.Profile = "kantor"

	if got := ProfileDbPath("default"); !strings.HasSuffix(got, "session.db") || strings.Contains(got, "session.default") {
		t.Fatalf("default must map to session.db, got %q", got)
	}
	if got := ProfileDbPath(""); !strings.HasSuffix(got, "session.db") {
		t.Fatalf("empty must map to session.db, got %q", got)
	}
	if got := ProfileDbPath("kantor"); !strings.HasSuffix(got, "session.kantor.db") {
		t.Fatalf("named profile must map to session.kantor.db, got %q", got)
	}
	if Config.General.Profile != "kantor" {
		t.Fatalf("ProfileDbPath must not mutate the global Profile, got %q", Config.General.Profile)
	}
}
