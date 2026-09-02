package config

import "testing"

func TestPassphraseRoundtrip(t *testing.T) {
	hash := HashPassphrase("s3cret!")
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if !VerifyPassphrase("s3cret!", hash) {
		t.Fatalf("correct passphrase must verify")
	}
	if VerifyPassphrase("wrong", hash) {
		t.Fatalf("wrong passphrase must not verify")
	}
}

func TestPassphraseHashesAreSalted(t *testing.T) {
	a := HashPassphrase("same")
	b := HashPassphrase("same")
	if a == b {
		t.Fatalf("two hashes of the same passphrase must differ (random salt)")
	}
	if !VerifyPassphrase("same", a) || !VerifyPassphrase("same", b) {
		t.Fatalf("both salted hashes must verify the passphrase")
	}
}

func TestPassphraseRejectsCorruptEncodings(t *testing.T) {
	cases := []string{
		"",
		"garbage",
		"pbkdf2-sha256:600000:salt:key:extra",
		"md5:600000:00:00",
		"pbkdf2-sha256:0:00:00",
		"pbkdf2-sha256:600000:not-hex:00",
		"pbkdf2-sha256:600000:00:not-hex",
		"pbkdf2-sha256:abc:00:00",
	}
	for _, c := range cases {
		if VerifyPassphrase("anything", c) {
			t.Fatalf("corrupt encoding %q must not verify", c)
		}
	}
}

func TestValidProfileName(t *testing.T) {
	valid := []string{"kantor", "a-b_1", "Work", "0123"}
	for _, name := range valid {
		if !ValidProfileName(name) {
			t.Fatalf("expected %q to be a valid profile name", name)
		}
	}
	invalid := []string{"", "../x", "a b", "a/b", "a\\b", "a:b", "a*b", ".hidden"}
	for _, name := range invalid {
		if ValidProfileName(name) {
			t.Fatalf("expected %q to be rejected", name)
		}
	}
}
