package messages

import (
	"io"
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// stubHandler is a minimal UiMessageHandler for tests.
type stubHandler struct{}

func (stubHandler) NewMessage(Message)              {}
func (stubHandler) NewScreen([]Message)             {}
func (stubHandler) SetChats([]Chat)                 {}
func (stubHandler) ResetChat()                      {}
func (stubHandler) PrintError(error)                {}
func (stubHandler) PrintText(string)                {}
func (stubHandler) PrintFile(string)                {}
func (stubHandler) SetStatus(SessionStatus)         {}
func (stubHandler) OpenFile(string)                 {}
func (stubHandler) GetWriter() io.Writer            { return io.Discard }

func newLiveEvent(chatJID string, ts time.Time, text string) *events.Message {
	jid, _ := types.ParseJID(chatJID)
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     jid,
				Sender:   jid,
				IsFromMe: false,
				IsGroup:  false,
			},
			ID:        types.MessageID("test-msg-" + ts.Format("0102150405")),
			Timestamp: ts,
		},
		Message: &waProto.Message{
			Conversation: proto.String(text),
		},
	}
}

func setupSM(sessionStart time.Time) *SessionManager {
	sm := &SessionManager{}
	sm.Init(&stubHandler{})
	sm.sessionStart.Store(sessionStart.Unix())
	return sm
}

func TestStaleMessageSkipsTranscriptAndBumpsUnread(t *testing.T) {
	now := time.Now()
	sm := setupSM(now)
	chatID := "111@s.whatsapp.net"

	// Add an existing chat with 0 unread.
	sm.db.AddChat(Chat{Id: chatID, Name: "Alice"})

	evt := newLiveEvent(chatID, now.Add(-2*time.Hour), "old message")
	eh := &eventHandler{sm: sm}
	eh.handleLiveMessage(evt)

	// Message must NOT appear in transcript.
	msgs := sm.db.GetMessages(chatID)
	if len(msgs) != 0 {
		t.Fatalf("stale message should not be stored, got %d messages", len(msgs))
	}

	// Unread must be bumped by 1.
	chat, ok := sm.db.GetChat(chatID)
	if !ok {
		t.Fatal("chat should exist")
	}
	if chat.Unread != 1 {
		t.Fatalf("expected unread 1, got %d", chat.Unread)
	}
}

func TestFreshMessageIsIngested(t *testing.T) {
	now := time.Now()
	sm := setupSM(now)
	chatID := "222@s.whatsapp.net"

	evt := newLiveEvent(chatID, now, "fresh message")
	eh := &eventHandler{sm: sm}
	eh.handleLiveMessage(evt)

	msgs := sm.db.GetMessages(chatID)
	if len(msgs) != 1 {
		t.Fatalf("fresh message should be stored, got %d messages", len(msgs))
	}
	if msgs[0].Text != "fresh message" {
		t.Fatalf("unexpected message text: %q", msgs[0].Text)
	}
}

func TestWithinGraceMessageIsIngested(t *testing.T) {
	now := time.Now()
	sm := setupSM(now)
	chatID := "333@s.whatsapp.net"

	evt := newLiveEvent(chatID, now.Add(-3*time.Minute), "grace message")
	eh := &eventHandler{sm: sm}
	eh.handleLiveMessage(evt)

	msgs := sm.db.GetMessages(chatID)
	if len(msgs) != 1 {
		t.Fatalf("within-grace message should be stored, got %d messages", len(msgs))
	}
}

func TestZeroSessionStartIsIngested(t *testing.T) {
	sm := setupSM(time.Time{}) // zero value
	chatID := "444@s.whatsapp.net"

	evt := newLiveEvent(chatID, time.Now().Add(-1*time.Hour), "old but open")
	eh := &eventHandler{sm: sm}
	eh.handleLiveMessage(evt)

	msgs := sm.db.GetMessages(chatID)
	if len(msgs) != 1 {
		t.Fatalf("zero sessionStart should fail-open, got %d messages", len(msgs))
	}
}
