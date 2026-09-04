package messages

import (
	"context"
	"io"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// gateStubHandler satisfies UiMessageHandler without touching the tview UI.
type gateStubHandler struct{}

func (gateStubHandler) NewMessage(Message)      {}
func (gateStubHandler) NewScreen([]Message)     {}
func (gateStubHandler) SetChats([]Chat)         {}
func (gateStubHandler) ResetChat()              {}
func (gateStubHandler) PrintError(error)        {}
func (gateStubHandler) PrintText(string)        {}
func (gateStubHandler) PrintFile(string)        {}
func (gateStubHandler) SetStatus(SessionStatus) {}
func (gateStubHandler) OpenFile(string)         {}
func (gateStubHandler) GetWriter() io.Writer    { return io.Discard }

// The history-sync gate must import only ON_DEMAND chunks (the /backlog
// responses); server-pushed RECENT/FULL/INITIAL_BOOTSTRAP chunks must not
// flood the in-memory store with old messages.
func TestHistorySyncGateDropsAutoPushChunks(t *testing.T) {
	sm := &SessionManager{}
	sm.db = &MessageDatabase{}
	sm.db.Init()
	sm.uiHandler = gateStubHandler{}
	// minimal client backed by an in-memory sqlite store so ParseWebMessage
	container, err := sqlstore.New(context.Background(), "sqlite", "file:gate_test?mode=memory&cache=shared&_pragma=foreign_keys(1)", waLog.Noop)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		t.Fatalf("device: %v", err)
	}
	sm.client = whatsmeow.NewClient(deviceStore, waLog.Noop)

	eh := &eventHandler{sm: sm}
	conv := makeTestConversation()

	for _, tc := range []struct {
		name      string
		syncType  int32 // waHistorySync.HistorySync_*
		wantMsgs  int
		wantChats int
	}{
		{"recent_chunk_dropped", 3, 0, 1},      // RECENT
		{"full_chunk_dropped", 2, 0, 1},        // FULL
		{"initial_bootstrap_dropped", 0, 0, 1}, // INITIAL_BOOTSTRAP
		{"on_demand_imported", 6, 2, 1},        // ON_DEMAND
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm.db.Init()
			// /backlog must be in flight for the chunk's chat, or even an
			// ON_DEMAND chunk is metadata-only.
			sm.beginBacklogFetch("120363@g.us")
			evt := makeTestHistorySyncEvent(int32(tc.syncType), conv)
			eh.handleHistorySync(evt)
			if got := len(sm.db.GetMessages("120363@g.us")); got != tc.wantMsgs {
				t.Fatalf("messages = %d, want %d", got, tc.wantMsgs)
			}
			if got := len(sm.GetKnownChats()); got < tc.wantChats {
				t.Fatalf("chats = %d, want >= %d (metadata must survive the gate)", got, tc.wantChats)
			}
		})
	}
}

// An ON_DEMAND chunk must only ingest messages for the chat the in-flight
// /backlog fetch asked about; other conversations in the same chunk stay
// metadata-only, and an expired fetch window ingests nothing.
func TestOnDemandIngestScopedToFetchTarget(t *testing.T) {
	sm := &SessionManager{}
	sm.db = &MessageDatabase{}
	sm.db.Init()
	sm.uiHandler = gateStubHandler{}
	container, err := sqlstore.New(context.Background(), "sqlite", "file:scope_test?mode=memory&cache=shared&_pragma=foreign_keys(1)", waLog.Noop)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		t.Fatalf("device: %v", err)
	}
	sm.client = whatsmeow.NewClient(deviceStore, waLog.Noop)
	eh := &eventHandler{sm: sm}
	conv := makeTestConversation() // chat 120363@g.us

	// Fetch in flight for a DIFFERENT chat: chunk is metadata-only.
	sm.db.Init()
	sm.beginBacklogFetch("other@s.whatsapp.net")
	eh.handleHistorySync(makeTestHistorySyncEvent(6, conv))
	if got := len(sm.db.GetMessages("120363@g.us")); got != 0 {
		t.Fatalf("off-target chunk ingested %d messages, want 0", got)
	}
	if _, ok := sm.db.GetChat("120363@g.us"); !ok {
		t.Fatal("metadata must still be imported for off-target conversations")
	}

	// Fetch window expired: nothing is ingested.
	sm.db.Init()
	sm.beginBacklogFetch("120363@g.us")
	sm.backlogLock.Lock()
	sm.backlogUntil = time.Now().Add(-time.Second)
	sm.backlogLock.Unlock()
	eh.handleHistorySync(makeTestHistorySyncEvent(6, conv))
	if got := len(sm.db.GetMessages("120363@g.us")); got != 0 {
		t.Fatalf("expired-window chunk ingested %d messages, want 0", got)
	}
}

// TrimMessagesBefore must drop only messages older than the cutoff and keep
// the rest reachable via GetMessages/GetOldestMessage.
func TestTrimMessagesBefore(t *testing.T) {
	md := &MessageDatabase{}
	md.Init()
	now := time.Now()

	base := Message{ChatId: "123@s.whatsapp.net", Id: "m1", Timestamp: uint64(now.Add(-2 * time.Hour).Unix())}
	md.AddMessage(base, false)
	md.AddMessage(Message{ChatId: "123@s.whatsapp.net", Id: "m2", Timestamp: uint64(now.Add(-30 * time.Minute).Unix())}, false)
	md.AddMessage(Message{ChatId: "123@s.whatsapp.net", Id: "m3", Timestamp: uint64(now.Unix())}, false)

	removed := md.TrimMessagesBefore("123@s.whatsapp.net", now.Add(-1*time.Hour).Unix())
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	msgs := md.GetMessages("123@s.whatsapp.net")
	if len(msgs) != 2 {
		t.Fatalf("remaining = %d, want 2", len(msgs))
	}
	for _, m := range msgs {
		if m.Id == "m1" {
			t.Fatal("message older than cutoff must be trimmed")
		}
	}
	if _, ok := md.messagesById["m1"]; ok {
		t.Fatal("messagesById entry for trimmed message must be deleted")
	}
}
