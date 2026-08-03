package messages

import "testing"

func TestChatSortingByRecentThenName(t *testing.T) {
	md := &MessageDatabase{}
	md.Init()
	md.AddChat(Chat{Id: "b", Name: "B", LastMessage: 10})
	md.AddChat(Chat{Id: "a", Name: "A", LastMessage: 20})
	out := md.GetChatIds()
	if len(out) != 2 || out[0].Id != "a" {
		t.Fatalf("expected newest chat first")
	}
}
