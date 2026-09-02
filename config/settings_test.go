package config

import "testing"

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
