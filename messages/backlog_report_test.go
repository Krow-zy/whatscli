package messages

import (
	"io"
	"strings"
	"sync"
	"testing"
)

// recHandler mirrors gateStubHandler but records UI output so backlog tests
// can assert failures are visible instead of silent.
type recHandler struct {
	mu     sync.Mutex
	texts  []string
	errors []string
}

func (r *recHandler) NewMessage(Message)      {}
func (r *recHandler) NewScreen([]Message)     {}
func (r *recHandler) SetChats([]Chat)         {}
func (r *recHandler) ResetChat()              {}
func (r *recHandler) PrintError(err error)    { r.mu.Lock(); defer r.mu.Unlock(); r.errors = append(r.errors, err.Error()) }
func (r *recHandler) PrintText(s string)      { r.mu.Lock(); defer r.mu.Unlock(); r.texts = append(r.texts, s) }
func (r *recHandler) PrintFile(string)        {}
func (r *recHandler) SetStatus(SessionStatus) {}
func (r *recHandler) OpenFile(string)         {}
func (r *recHandler) GetWriter() io.Writer    { return io.Discard }

func (r *recHandler) hasText(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.texts {
		if strings.Contains(t, substr) {
			return true
		}
	}
	return false
}

func newBacklogReportManager(h *recHandler) *SessionManager {
	sm := &SessionManager{}
	sm.db = &MessageDatabase{}
	sm.db.Init()
	sm.uiHandler = h
	return sm
}

// Plain /backlog (no window) with zero messages loaded must print a
// "no messages" outcome line instead of printing nothing. loadBacklog itself
// requires a live connected client, so this goes through the extracted
// reportBacklogResult helper, which loadBacklog must call for its switch.
func TestBacklogZeroResultIsReported(t *testing.T) {
	h := &recHandler{}
	sm := newBacklogReportManager(h)

	sm.reportBacklogResult(0, false, 0)
	if !h.hasText("No additional messages found") {
		t.Fatal("plain /backlog with zero messages loaded printed no outcome line (silent failure)")
	}
}

func TestFetchBacklogParseErrorIsVisible(t *testing.T) {
	// The loadBacklog -> fetchBacklog path must surface an unparsable chat ID
	// as a visible error instead of returning (0, false) in silence.
	h := &recHandler{}
	sm := newBacklogReportManager(h)

	// ParseJID is lenient: only malformed AD-JID dot/colon forms (or extra
	// separators combined with them) fail, e.g. three dot-segments.
	loaded, covered := sm.fetchBacklog("a.b.c@s.whatsapp.net", 0)
	if loaded != 0 || covered {
		t.Fatalf("fetchBacklog(invalid) = (%d, %v), want (0, false)", loaded, covered)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.errors) == 0 {
		t.Fatal("fetchBacklog with unparsable chat ID printed no visible error (silent failure)")
	}
}
