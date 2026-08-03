package messages

import "testing"

func TestChatMatches(t *testing.T) {
	if !chatMatches(Chat{Id: "123@s.whatsapp.net", Name: "Alice"}, "ali") {
		t.Fatalf("expected name match")
	}
	if !chatMatches(Chat{Id: "123@s.whatsapp.net", Name: ""}, "123") {
		t.Fatalf("expected id match")
	}
	if chatMatches(Chat{Id: "123@s.whatsapp.net", Name: "Alice"}, "zzz") {
		t.Fatalf("unexpected match")
	}
}
