package messages

import (
	"testing"
	"time"
)

func TestDownloadFileNameSanitizesPathTraversal(t *testing.T) {
	msg := Message{
		Id:       "msg-1",
		FileName: "../../.ssh/authorized_keys",
	}

	got := downloadFileName(msg)
	if got != "authorized_keys" {
		t.Fatalf("expected sanitized basename, got %q", got)
	}
}

func TestDownloadFileNameSanitizesWindowsPathTraversal(t *testing.T) {
	msg := Message{
		Id:       "msg-2",
		FileName: `..\..\AppData\Roaming\startup.bat`,
	}

	got := downloadFileName(msg)
	if got != "startup.bat" {
		t.Fatalf("expected sanitized basename, got %q", got)
	}
}

func TestDownloadFileNameFallsBackForInvalidName(t *testing.T) {
	msg := Message{
		Id:       "msg-3",
		FileName: "..",
		MimeType: "image/png",
	}

	got := downloadFileName(msg)
	if got != "msg-3.png" {
		t.Fatalf("expected fallback filename, got %q", got)
	}
}

func TestChatIsMuted(t *testing.T) {
	if (Chat{MutedUntil: 0}).IsMuted() {
		t.Fatalf("unmuted chat reported muted")
	}
	if !(Chat{MutedUntil: -1}).IsMuted() {
		t.Fatalf("forever mute not detected")
	}
	if !(Chat{MutedUntil: time.Now().Unix() + 3600}).IsMuted() {
		t.Fatalf("future mute not detected")
	}
	if (Chat{MutedUntil: time.Now().Unix() - 3600}).IsMuted() {
		t.Fatalf("expired mute still active")
	}
}

func TestSetChatMutedSurvivesAddChatMerge(t *testing.T) {
	md := &MessageDatabase{}
	md.Init()
	md.AddChat(Chat{Id: "111@s.whatsapp.net", Name: "A"})
	md.SetChatMuted("111@s.whatsapp.net", -1)
	md.AddChat(Chat{Id: "111@s.whatsapp.net", Name: "A"})

	chat, ok := md.GetChat("111@s.whatsapp.net")
	if !ok || !chat.IsMuted() {
		t.Fatalf("mute state lost on AddChat merge")
	}

	md.SetChatMuted("111@s.whatsapp.net", 0)
	chat, _ = md.GetChat("111@s.whatsapp.net")
	if chat.IsMuted() {
		t.Fatalf("unmute did not apply")
	}
}

func TestGetKnownChatsFiltersRecencyAndNonChats(t *testing.T) {
	sm := &SessionManager{}
	sm.db = &MessageDatabase{}
	sm.db.Init()
	sm.db.AddChat(Chat{Id: "111@s.whatsapp.net", Name: "NoMessages"})
	sm.db.AddChat(Chat{Id: "222@s.whatsapp.net", Name: "Recent", LastMessage: 20})
	sm.db.AddChat(Chat{Id: "333@g.us", Name: "Group", IsGroup: true, LastMessage: 10})
	sm.db.AddChat(Chat{Id: "status@broadcast", Name: "Status", LastMessage: 30})
	sm.db.AddChat(Chat{Id: "999@newsletter", Name: "News", LastMessage: 40})

	out := sm.GetKnownChats()
	if len(out) != 2 || out[0].Id != "222@s.whatsapp.net" || out[1].Id != "333@g.us" {
		t.Fatalf("expected only recent account+group chats, got %+v", out)
	}
}

func TestResolveSearchChatsUsesMemoryStores(t *testing.T) {
	sm := &SessionManager{}
	sm.db = &MessageDatabase{}
	sm.db.Init()
	sm.db.AddChat(Chat{Id: "333@g.us", Name: "Kelompok Studi", IsGroup: true, LastMessage: 10})
	sm.db.AddContact(Contact{Id: "444@s.whatsapp.net", Name: "Budi"})
	sm.db.AddContact(Contact{Id: "status@broadcast", Name: "Status"})

	if out := sm.ResolveSearchChats("budi"); len(out) != 1 || out[0].Id != "444@s.whatsapp.net" {
		t.Fatalf("expected contact match from memory, got %+v", out)
	}
	if out := sm.ResolveSearchChats("kelompok"); len(out) != 1 || !out[0].IsGroup {
		t.Fatalf("expected group match from memory, got %+v", out)
	}
	if out := sm.ResolveSearchChats("status"); len(out) != 0 {
		t.Fatalf("status must never appear in search, got %+v", out)
	}
}
