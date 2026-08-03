package messages

import "testing"

func TestAddMessageDoesNotUseSenderNameForGroupChatTitle(t *testing.T) {
	md := &MessageDatabase{}
	md.Init()
	md.AddMessage(Message{Id: "1", ChatId: "123@g.us", ContactName: "Sender Name", Timestamp: 1}, false)
	chats := md.GetChatIds()
	if len(chats) != 1 {
		t.Fatalf("expected one chat")
	}
	if chats[0].Name != "" {
		t.Fatalf("group name should stay empty until real group metadata arrives, got %q", chats[0].Name)
	}
}
