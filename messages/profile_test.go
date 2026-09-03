package messages

import (
	"io"
	"strings"
	"testing"

	"github.com/normen/whatscli/config"
)

// captureHandler records the last error/text printed, so guard behaviour can
// be asserted without a tview app.
type captureHandler struct {
	errText  string
	lastText string
}

func (c *captureHandler) NewMessage(Message)  {}
func (c *captureHandler) NewScreen([]Message) {}
func (c *captureHandler) SetChats([]Chat)     {}
func (c *captureHandler) ResetChat()          {}
func (c *captureHandler) PrintError(e error) {
	if e != nil {
		c.errText = e.Error()
	}
}
func (c *captureHandler) PrintText(s string)      { c.lastText = s }
func (c *captureHandler) PrintFile(string)        {}
func (c *captureHandler) SetStatus(SessionStatus) {}
func (c *captureHandler) OpenFile(string)         {}
func (c *captureHandler) GetWriter() io.Writer    { return io.Discard }

// removeProfile must refuse to delete the active profile — otherwise a
// mis-click would wipe the session the user is logged into.
func TestRemoveProfileRefusesActive(t *testing.T) {
	sm := &SessionManager{db: &MessageDatabase{}}
	sm.db.Init()
	cap := &captureHandler{}
	sm.uiHandler = cap

	orig := config.Config.General.Profile
	defer func() { config.Config.General.Profile = orig }()
	config.Config.General.Profile = "activetest"

	sm.removeProfile("activetest")
	if cap.errText == "" {
		t.Fatal("removing the active profile must report an error")
	}
	if !strings.Contains(cap.errText, "active") {
		t.Fatalf("error should mention the active profile, got %q", cap.errText)
	}
}

// removeProfile must reject names that could escape the config directory.
func TestRemoveProfileRejectsInvalidName(t *testing.T) {
	sm := &SessionManager{db: &MessageDatabase{}}
	sm.db.Init()
	cap := &captureHandler{}
	sm.uiHandler = cap

	orig := config.Config.General.Profile
	defer func() { config.Config.General.Profile = orig }()
	config.Config.General.Profile = "default"

	sm.removeProfile("../evil")
	if cap.errText == "" || !strings.Contains(cap.errText, "invalid") {
		t.Fatalf("invalid profile name must be rejected, got %q", cap.errText)
	}
}
