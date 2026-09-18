package messages

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/gen2brain/beeep"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/qrcode"
	"github.com/rivo/tview"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite" // SQLite driver (pure Go)
)

var urlPattern = regexp.MustCompile(`https?://[^\s]+`)

// staleMessageGrace tolerates phone/laptop clock skew when gating offline messages.
const staleMessageGrace = 5 * time.Minute

// SessionManager deals with the connection and receives commands from the UI.
type SessionManager struct {
	db               *MessageDatabase
	currentReceiver  string
	uiHandler        UiMessageHandler
	client           *whatsmeow.Client
	container        *sqlstore.Container
	BatteryChannel   chan BatteryMsg
	StatusChannel    chan StatusMsg
	CommandChannel   chan Command
	ChatChannel      chan Chat
	ContactChannel   chan Contact
	TextChannel      chan *waProto.Message
	deleteResultChan chan ProfileDeleteResult
	statusInfo       SessionStatus
	lastSent         time.Time
	started          bool
	eventHandler     *eventHandler
	// backlogLock guards the /backlog ingest window: the manager goroutine
	// opens/refreshes it, whatsmeow's event goroutine reads it.
	backlogLock   sync.Mutex
	backlogTarget string
	backlogUntil  time.Time
	// sessionStart tracks real login time (unix seconds) for stale-message
	// gating: written on the manager goroutine at login, read on whatsmeow's
	// event goroutine, hence atomic.
	sessionStart atomic.Int64
}

// Init initializes the SessionManager.
func (sm *SessionManager) Init(handler UiMessageHandler) {
	sm.db = &MessageDatabase{}
	sm.db.Init()
	sm.uiHandler = handler
	sm.BatteryChannel = make(chan BatteryMsg, 10)
	sm.StatusChannel = make(chan StatusMsg, 10)
	sm.CommandChannel = make(chan Command, 10)
	sm.ChatChannel = make(chan Chat, 10)
	sm.TextChannel = make(chan *waProto.Message, 10)
	sm.deleteResultChan = make(chan ProfileDeleteResult, 1)
	sm.eventHandler = &eventHandler{sm: sm}
}

// StartManager starts the receiver and message handling goroutine. With
// autoConnect the manager connects immediately using the configured
// profile; otherwise it idles in the command loop until the UI sends a
// "profile"/"connect" command. The passphrase gate passes false: running
// loginWithConnection at boot would enter the QR-wait loop on THIS
// goroutine when the configured profile has no stored device, and every
// queued command (profile switch, backlog, deleteprofile) would stall.
func (sm *SessionManager) StartManager(autoConnect bool) error {
	if sm.started {
		return errors.New("session manager running, send commands to control")
	}
	sm.started = true
	go sm.runManager(autoConnect)
	return nil
}

func (sm *SessionManager) runManager(autoConnect bool) error {
	if autoConnect {
		client, err := sm.getConnection()
		if err != nil {
			sm.uiHandler.PrintError(fmt.Errorf("failed to create WhatsApp connection: %v", err))
			return err
		}
		if client == nil {
			return errors.New("could not establish WhatsApp connection")
		}

		if err = sm.loginWithConnection(client); err != nil {
			sm.uiHandler.PrintError(err)
		}
	}

	for sm.started {
		select {
		case command := <-sm.CommandChannel:
			sm.execCommand(command)
		case batteryMsg := <-sm.BatteryChannel:
			sm.statusInfo.BatteryLoading = batteryMsg.loading
			sm.statusInfo.BatteryPowersave = batteryMsg.powersave
			sm.statusInfo.BatteryCharge = batteryMsg.charge
			sm.uiHandler.SetStatus(sm.statusInfo)
		case statusMsg := <-sm.StatusChannel:
			prevStatus := sm.statusInfo.Connected
			if statusMsg.err == nil {
				sm.statusInfo.Connected = statusMsg.connected
			}
			if sm.client != nil {
				sm.statusInfo.Connected = sm.client.IsConnected()
			} else {
				sm.statusInfo.Connected = false
			}
			sm.uiHandler.SetStatus(sm.statusInfo)
			if prevStatus != sm.statusInfo.Connected {
				if sm.statusInfo.Connected {
					sm.uiHandler.PrintText("connected")
				} else {
					sm.uiHandler.PrintText("disconnected")
				}
			}
		}
	}

	fmt.Fprintln(sm.uiHandler.GetWriter(), "closing the receiver")
	if sm.client != nil {
		sm.client.Disconnect()
	}
	return nil
}

func (sm *SessionManager) setCurrentReceiver(id string) {
	sm.currentReceiver = id
	sm.statusInfo.ContactPresence = ""
	if sm.client != nil && sm.client.IsConnected() && strings.HasSuffix(id, CONTACTSUFFIX) {
		if jid, err := types.ParseJID(id); err == nil {
			if err = sm.client.SubscribePresence(context.Background(), jid); err != nil {
				sm.uiHandler.PrintError(fmt.Errorf("presence subscription failed: %v", err))
			}
		}
	}
	sm.uiHandler.NewScreen(sm.getMessages(id))
}

func (sm *SessionManager) getConnection() (*whatsmeow.Client, error) {
	if sm.client == nil {
		dbPath := config.GetSessionFilePath() + ".db"
		container, err := sqlstore.New(context.Background(), "sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)", waLog.Noop)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to database: %v", err)
		}
		deviceStore, err := container.GetFirstDevice(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to get device: %v", err)
		}
		client := whatsmeow.NewClient(deviceStore, waLog.Noop)
		client.AddEventHandler(sm.eventHandler.Handle)
		sm.client = client
		sm.container = container
	}
	return sm.client, nil
}

func (sm *SessionManager) login() error {
	sm.client = nil
	client, err := sm.getConnection()
	if err != nil {
		return fmt.Errorf("failed to create WhatsApp connection: %v", err)
	}
	return sm.loginWithConnection(client)
}

func (sm *SessionManager) loginWithConnection(client *whatsmeow.Client) error {
	sm.uiHandler.PrintText("connecting..")
	if client.IsConnected() {
		client.Disconnect()
		sm.StatusChannel <- StatusMsg{false, nil}
		time.Sleep(500 * time.Millisecond)
	}

	if client.Store.ID == nil {
		return sm.loginWithQRCode(client)
	}

	if err := client.Connect(); err != nil {
		if errors.Is(err, whatsmeow.ErrNotConnected) || errors.Is(err, whatsmeow.ErrNotLoggedIn) {
			sm.uiHandler.PrintText("Session expired, need to scan QR code again")
			if delErr := client.Store.Delete(context.Background()); delErr != nil {
				return fmt.Errorf("failed to clear expired session: %v", delErr)
			}
			sm.client = nil
			client, err = sm.getConnection()
			if err != nil {
				return fmt.Errorf("failed to create new connection: %v", err)
			}
			return sm.loginWithQRCode(client)
		}
		return fmt.Errorf("connection failed: %v", err)
	}

	sm.saveActiveProfile()
	sm.uiHandler.PrintText("Session restored successfully")
	sm.StatusChannel <- StatusMsg{true, nil}
	sm.sessionStart.Store(time.Now().Unix())
	go sm.loadRecentChats()
	return nil
}

func (sm *SessionManager) loginWithQRCode(client *whatsmeow.Client) error {
	sm.uiHandler.PrintText("Please scan the QR code with your phone")
	qrChan, err := client.GetQRChannel(context.Background())
	if err != nil {
		return fmt.Errorf("failed to initialize QR channel: %v", err)
	}
	if err = client.Connect(); err != nil {
		return fmt.Errorf("error connecting to WhatsApp: %v", err)
	}

	for evt := range qrChan {
		switch evt.Event {
		case "code":
			terminal := qrcode.New()
			terminal.SetOutput(tview.ANSIWriter(sm.uiHandler.GetWriter()))
			terminal.Get(evt.Code).Print()
		case "success":
			sm.saveActiveProfile()
			sm.uiHandler.PrintText("Successfully logged in!")
			sm.StatusChannel <- StatusMsg{true, nil}
			sm.sessionStart.Store(time.Now().Unix())
			go sm.loadRecentChats()
			return nil
		default:
			sm.uiHandler.PrintText("QR event: " + evt.Event)
		}
	}
	return errors.New("QR code channel closed without success")
}

func (sm *SessionManager) loadRecentChats() {
	if sm.client == nil || !sm.client.IsConnected() {
		sm.uiHandler.PrintError(errors.New("not connected to WhatsApp"))
		return
	}

	sm.loadContacts()
	addedChats := 0

	if sm.client.Store != nil && sm.client.Store.Contacts != nil {
		contacts, err := sm.client.Store.Contacts.GetAllContacts(context.Background())
		if err == nil {
			for jid, contact := range contacts {
				if jid.Server != types.DefaultUserServer {
					continue
				}
				name := contact.FullName
				if name == "" {
					name = contact.PushName
				}
				if name == "" {
					name = jid.User
				}
				sm.db.AddChat(Chat{
					Id:      jid.String(),
					IsGroup: false,
					Name:    name,
				})
				addedChats++
			}
		}
	}

	groups, err := sm.client.GetJoinedGroups(context.Background())
	if err == nil {
		for _, group := range groups {
			sm.db.AddChat(Chat{
				Id:      group.JID.String(),
				IsGroup: true,
				Name:    group.Name,
			})
			addedChats++
		}
	}

	sm.uiHandler.SetChats(sm.GetKnownChats())
	if addedChats > 0 {
		sm.uiHandler.PrintText(fmt.Sprintf("Loaded %d chats", addedChats))
	}
}

func (sm *SessionManager) loadContacts() {
	if sm.client == nil || sm.client.Store == nil || sm.client.Store.Contacts == nil {
		return
	}

	contacts, err := sm.client.Store.Contacts.GetAllContacts(context.Background())
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to load contacts: %v", err))
		return
	}

	contactCount := 0
	for jid, contact := range contacts {
		name := contact.FullName
		if name == "" {
			name = contact.PushName
		}
		if name == "" {
			name = jid.User
		}
		sm.db.AddContact(Contact{
			Id:    jid.String(),
			Name:  name,
			Short: contact.PushName,
		})
		contactCount++
	}
	if contactCount > 0 {
		sm.uiHandler.PrintText(fmt.Sprintf("Loaded %d contacts", contactCount))
	}
}

func (sm *SessionManager) getChatName(jid types.JID) string {
	if sm.client != nil && jid.Server == types.GroupServer {
		groupInfo, err := sm.client.GetGroupInfo(context.Background(), jid)
		if err == nil && groupInfo.Name != "" {
			return groupInfo.Name
		}
	}
	if sm.client != nil && sm.client.Store != nil && sm.client.Store.Contacts != nil {
		contact, err := sm.client.Store.Contacts.GetContact(context.Background(), jid)
		if err == nil && contact.Found {
			if contact.FullName != "" {
				return contact.FullName
			}
			if contact.PushName != "" {
				return contact.PushName
			}
		}
	}
	return sm.db.GetIdName(jid.String())
}

func (sm *SessionManager) disconnect() error {
	if sm.client != nil && sm.client.IsConnected() {
		sm.client.Disconnect()
		sm.StatusChannel <- StatusMsg{false, nil}
	}
	return nil
}

func (sm *SessionManager) logout() error {
	if sm.client == nil {
		sm.StatusChannel <- StatusMsg{false, nil}
		sm.uiHandler.PrintText("Already logged out")
		return nil
	}

	if sm.client.Store != nil && sm.client.Store.ID != nil {
		if err := sm.client.Logout(context.Background()); err != nil && !errors.Is(err, whatsmeow.ErrNotConnected) {
			sm.uiHandler.PrintText("Warning: Couldn't fully log out: " + err.Error())
		}
	}
	sm.client = nil
	sm.container = nil
	sm.StatusChannel <- StatusMsg{false, nil}
	sm.uiHandler.PrintText("Successfully logged out")
	return nil
}

func (sm *SessionManager) execCommand(command Command) {
	switch command.Name {
	default:
		sm.uiHandler.PrintText("[" + config.Config.Colors.Negative + "]Unknown command: [-]" + command.Name)
	case "backlog":
		sm.loadBacklog(command.Params)
	case "login", "connect":
		err := sm.login()
		if err != nil {
			sm.uiHandler.PrintError(fmt.Errorf("WhatsApp connection failed: %v", err))
			sm.uiHandler.PrintText("Try using /reset to completely reset the connection")
		} else {
			sm.uiHandler.PrintText("Successfully connected to WhatsApp")
		}
	case "reset":
		sm.resetSession()
	case "notifications":
		config.SaveNotifications(!config.Config.General.EnableNotifications)
		if config.Config.General.EnableNotifications {
			sm.uiHandler.PrintText("desktop notifications enabled (saved to config)")
		} else {
			sm.uiHandler.PrintText("desktop notifications disabled (saved to config)")
		}
	case "deleteprofile":
		// Runs on the manager goroutine: releasing the active profile's DB
		// handle is safe here, and the picker waits for the result channel
		// instead of calling teardownProfile on the UI goroutine (which
		// would deadlock in ResetChat's QueueUpdateDraw).
		sm.deleteProfile(command.Params)
	case "disconnect":
		sm.uiHandler.PrintError(sm.disconnect())
	case "logout":
		sm.uiHandler.PrintError(sm.logout())
	case "profile":
		sm.profileCommand(command.Params)
	case "send":
		if checkParam(command.Params, 2) {
			sm.sendText(command.Params[0], strings.Join(command.Params[1:], " "))
		} else {
			sm.printCommandUsage("send", "[chat-id[] [message text[]")
		}
	case "select":
		if checkParam(command.Params, 1) {
			sm.setCurrentReceiver(command.Params[0])
		} else {
			sm.printCommandUsage("select", "[chat-id[]")
		}
	case "read":
		sm.markCurrentChatRead()
	case "info":
		if checkParam(command.Params, 1) {
			sm.uiHandler.PrintText(sm.db.GetMessageInfo(command.Params[0]))
		} else {
			sm.printCommandUsage("info", "[message-id[]")
		}
	case "download":
		sm.downloadCommand(command.Params, false, false)
	case "open":
		sm.downloadCommand(command.Params, true, false)
	case "show":
		sm.downloadCommand(command.Params, true, true)
	case "url":
		sm.openMessageURL(command.Params)
	case "upload":
		sm.sendMediaCommand(command.Params, MessageKindDocument)
	case "sendimage":
		sm.sendMediaCommand(command.Params, MessageKindImage)
	case "sendvideo":
		sm.sendMediaCommand(command.Params, MessageKindVideo)
	case "sendaudio":
		sm.sendMediaCommand(command.Params, MessageKindAudio)
	case "revoke":
		sm.revokeMessage(command.Params)
	case "leave":
		sm.leaveCurrentGroup()
	case "create":
		sm.createGroup(command.Params)
	case "add":
		sm.updateCurrentGroupParticipants(command.Params, whatsmeow.ParticipantChangeAdd, "add", "added new members")
	case "remove":
		sm.updateCurrentGroupParticipants(command.Params, whatsmeow.ParticipantChangeRemove, "remove", "removed members")
	case "admin":
		sm.updateCurrentGroupParticipants(command.Params, whatsmeow.ParticipantChangePromote, "admin", "promoted members")
	case "removeadmin":
		sm.updateCurrentGroupParticipants(command.Params, whatsmeow.ParticipantChangeDemote, "removeadmin", "demoted members")
	case "subject":
		sm.updateCurrentGroupSubject(command.Params)
	case "colorlist":
		out := ""
		for idx := range tcell.ColorNames {
			out += "[" + idx + "]" + idx + "[-]\n"
		}
		sm.uiHandler.PrintText(out)
	case "more":
		sm.loadBacklog(nil)
	}
}

// beginBacklogFetch marks chat as the target of an in-flight /backlog fetch
// and opens a 25s ingest window (15s batch wait + grace), refreshed on every
// batch request. ON_DEMAND chunks are only imported for this chat while the
// window is open: the server can push ON_DEMAND data spontaneously, and a
// chunk may carry conversations besides the one we asked about.
func (sm *SessionManager) beginBacklogFetch(chat string) {
	sm.backlogLock.Lock()
	sm.backlogTarget = chat
	sm.backlogUntil = time.Now().Add(25 * time.Second)
	sm.backlogLock.Unlock()
}

// backlogAccepts reports whether messages from an ON_DEMAND chunk for chatID
// may be ingested right now — only during an in-flight fetch for exactly
// that chat.
func (sm *SessionManager) backlogAccepts(chatID string) bool {
	sm.backlogLock.Lock()
	defer sm.backlogLock.Unlock()
	return sm.backlogTarget != "" && chatID == sm.backlogTarget && time.Now().Before(sm.backlogUntil)
}

// loadBacklog requests chat history older than the oldest locally stored
// message from the phone. An optional minutes parameter bounds the fetch:
// batches are requested until the requested window is covered. History is
// never discarded by /backlog — each call only ADDS messages; a later call
// with a larger window pages deeper, one with a smaller window is a no-op
// when the local history already reaches past it. Without a parameter one
// batch of 50 messages is requested. With no chat open it bulk-loads the
// most recent sidebar chats instead of failing (see loadBacklogRecent).
func (sm *SessionManager) loadBacklog(params []string) {
	if sm.client == nil || !sm.client.IsConnected() {
		sm.uiHandler.PrintError(errors.New("not connected to WhatsApp"))
		return
	}

	// /backlog <minutes> bounds the fetch; without an argument the
	// history_window_min config value applies (0 = plain one-batch mode).
	windowMinutes := config.Config.General.HistoryWindowMin
	var err error
	if len(params) > 0 {
		windowMinutes, err = strconv.ParseInt(params[0], 10, 64)
		if err != nil || windowMinutes < 0 {
			sm.printCommandUsage("backlog", "[minutes]")
			return
		}
	}

	// No chat open: WhatsApp's on-demand sync is per-conversation, so
	// instead of failing, page through the most recent sidebar chats.
	if sm.currentReceiver == "" {
		sm.loadBacklogRecent(windowMinutes)
		return
	}

	if windowMinutes > 0 {
		sm.uiHandler.PrintText(fmt.Sprintf("Retrieving message history for the last %d minute(s)...", windowMinutes))
	} else {
		sm.uiHandler.PrintText("Retrieving message history...")
	}
	loaded, covered := sm.fetchBacklog(sm.currentReceiver, windowMinutes)
	sm.reportBacklogResult(loaded, covered, windowMinutes)
	sm.uiHandler.NewScreen(sm.db.GetMessages(sm.currentReceiver))
}

// reportBacklogResult prints the /backlog outcome line. The plain (no window)
// zero-result case also reports instead of falling through in silence.
func (sm *SessionManager) reportBacklogResult(loaded int, covered bool, windowMinutes int64) {
	switch {
	case loaded > 0:
		sm.uiHandler.PrintText(fmt.Sprintf("Loaded %d additional messages", loaded))
	case covered:
		sm.uiHandler.PrintText(fmt.Sprintf("History for the last %d minute(s) is already loaded.", windowMinutes))
	default:
		sm.uiHandler.PrintText("No additional messages found. WhatsApp may limit history access.")
	}
}

// fetchBacklog pages on-demand history for one chat until the requested
// window is covered (windowMinutes <= 0 = a single 50-message batch). It
// returns the number of newly ingested messages and whether the window was
// already covered by the local history (so the caller can skip the pointless
// round-trip report).
func (sm *SessionManager) fetchBacklog(chatID string, windowMinutes int64) (loaded int, alreadyCovered bool) {
	jid, err := types.ParseJID(chatID)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("invalid chat ID %q: %v", chatID, err))
		return 0, false
	}
	if windowMinutes > 0 {
		cutoff := time.Now().Add(-time.Duration(windowMinutes) * time.Minute)
		if oldest, ok := sm.db.GetOldestMessage(chatID); ok && int64(oldest.Timestamp) <= cutoff.Unix() {
			return 0, true
		}
	}

	// The on-demand protocol fetches a fixed number of messages before an
	// anchor message. With no local anchor (clean session) send an empty
	// anchor so the phone returns the newest messages.
	deadline := time.Now().Add(120 * time.Second)
	for {
		before := len(sm.db.GetMessages(chatID))
		anchor := types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:    jid,
				IsGroup: strings.Contains(chatID, GROUPSUFFIX),
			},
		}
		if oldest, ok := sm.db.GetOldestMessage(chatID); ok {
			anchor.ID = types.MessageID(oldest.Id)
			anchor.IsFromMe = oldest.FromMe
			anchor.Timestamp = time.Unix(int64(oldest.Timestamp), 0)
			if oldest.SenderId != "" {
				if parsedSender, parseErr := types.ParseJID(oldest.SenderId); parseErr == nil {
					anchor.Sender = parsedSender
				}
			}
		} else {
			anchor.Timestamp = time.Now()
		}
		// Refresh the ingest window right before sending so the response
		// (and a trailing chunk) can land within it.
		sm.beginBacklogFetch(chatID)
		req := sm.client.BuildHistorySyncRequest(&anchor, 50)
		sendCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		resp, err := sm.client.SendPeerMessage(sendCtx, req)
		cancel()
		sm.uiHandler.PrintText(fmt.Sprintf("[dbg] req sent id=%q ts=%d err=%v", anchor.ID, anchor.Timestamp.Unix(), err))
		if err != nil {
			sm.uiHandler.PrintError(fmt.Errorf("failed to request message history: %v", err))
			break
		}
		_ = resp

		wait := time.Now().Add(15 * time.Second)
		for len(sm.db.GetMessages(chatID)) == before && time.Now().Before(wait) {
			time.Sleep(250 * time.Millisecond)
		}
		got := len(sm.db.GetMessages(chatID)) - before
		if got <= 0 {
			break
		}
		loaded += got

		if windowMinutes <= 0 {
			// plain /backlog: one batch
			break
		}
		cutoff := time.Now().Add(-time.Duration(windowMinutes) * time.Minute)
		if oldest, ok := sm.db.GetOldestMessage(chatID); !ok || int64(oldest.Timestamp) <= cutoff.Unix() {
			// reached past the requested window
			break
		}
		if time.Now().After(deadline) {
			sm.uiHandler.PrintText("Stopped: took too long; run /backlog again to continue.")
			break
		}
	}
	return loaded, false
}

// loadBacklogRecent is /backlog's bulk mode: with no chat open it fetches
// one window for each of the most recent sidebar chats (GetKnownChats is
// recency-sorted). The manager goroutine is busy for the duration, so the
// cap keeps the worst case bounded.
func (sm *SessionManager) loadBacklogRecent(windowMinutes int64) {
	chats := sm.GetKnownChats()
	if len(chats) == 0 {
		sm.uiHandler.PrintText("No chats in the sidebar yet — wait for the chat list to load, or open a chat and run /backlog.")
		return
	}
	// ponytail: cap 10 chats/request; raise or make configurable if users
	// want deeper bulk loads.
	const backlogAllChats = 10
	if len(chats) > backlogAllChats {
		chats = chats[:backlogAllChats]
	}
	sm.uiHandler.PrintText(fmt.Sprintf("No chat open: loading history for the %d most recent chats...", len(chats)))
	total := 0
	for _, chat := range chats {
		n, _ := sm.fetchBacklog(chat.Id, windowMinutes)
		total += n
	}
	sm.uiHandler.PrintText(fmt.Sprintf("Loaded %d messages across %d chats. Open a chat to read them.", total, len(chats)))
	if sm.currentReceiver != "" {
		sm.uiHandler.NewScreen(sm.db.GetMessages(sm.currentReceiver))
	}
}

func (sm *SessionManager) resetSession() {
	if sm.client != nil {
		if sm.client.IsConnected() {
			sm.client.Disconnect()
		}
		if sm.client.Store != nil {
			if err := sm.client.Store.Delete(context.Background()); err != nil {
				sm.uiHandler.PrintText("Warning: Couldn't remove session: " + err.Error())
			}
		}
	}

	sm.client = nil
	sm.container = nil
	dbPath := config.GetSessionFilePath() + ".db"
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		sm.uiHandler.PrintText("Warning: Couldn't remove database file: " + err.Error())
	}
	sm.StatusChannel <- StatusMsg{false, nil}
	sm.uiHandler.PrintText("Session reset. Use /connect to reconnect with a new QR code.")
}

// profileCommand lists profiles, switches, or removes one:
//
//	/profile              list
//	/profile <name>       switch
//	/profile remove <name>  delete the stored session DB of that profile
func (sm *SessionManager) profileCommand(params []string) {
	if len(params) == 0 {
		current := config.Config.General.Profile
		if current == "" {
			current = "default"
		}
		sm.uiHandler.PrintText("Active profile: " + current)
		sm.uiHandler.PrintText("Available profiles:")
		for _, name := range config.AvailableProfiles() {
			marker := " "
			if name == current {
				marker = "*"
			}
			sm.uiHandler.PrintText("  " + marker + " " + name)
		}
		return
	}
	if params[0] == "remove" {
		if len(params) < 2 {
			sm.printCommandUsage("profile remove", "<name>")
			return
		}
		sm.removeProfile(params[1])
		return
	}
	name := params[0]
	if !config.ValidProfileName(name) {
		sm.uiHandler.PrintError(fmt.Errorf("invalid profile name %q (allowed: letters, digits, _ and -)", name))
		return
	}
	sm.switchProfile(name)
}

// removeProfile deletes the stored session DB of a non-active profile.
// Logging out on the server (unlinking the device) must be done separately
// with /logout while that profile is active — this only removes the local copy.
func (sm *SessionManager) removeProfile(name string) {
	if !config.ValidProfileName(name) {
		sm.uiHandler.PrintError(fmt.Errorf("invalid profile name %q (allowed: letters, digits, _ and -)", name))
		return
	}
	current := config.Config.General.Profile
	if current == "" {
		current = "default"
	}
	if name == current {
		sm.uiHandler.PrintError(fmt.Errorf("cannot remove the active profile %q — switch to another profile first", name))
		return
	}
	if err := config.RemoveProfileDb(name); err != nil {
		if os.IsNotExist(err) {
			sm.uiHandler.PrintError(fmt.Errorf("profile %q has no stored session", name))
		} else {
			sm.uiHandler.PrintError(fmt.Errorf("failed to remove profile %q: %v", name, err))
		}
		return
	}
	sm.uiHandler.PrintText(fmt.Sprintf("Profile %q removed (local session file deleted)", name))
}

// ProfileDeleteResult reports the outcome of a picker-initiated profile
// deletion that ran on the manager goroutine.
type ProfileDeleteResult struct {
	Name string
	Err  error
}

// deleteProfile handles the "deleteprofile" command sent by the account
// picker. Unlike /profile remove it may delete the ACTIVE profile: the
// manager goroutine tears down the connection and releases the sqlite
// handle first, so the file is not locked and the removal succeeds. The
// result is delivered on sm.deleteResultChan (buffered, size 1) so the
// picker can wait for completion before rebuilding the list.
func (sm *SessionManager) deleteProfile(params []string) {
	if !checkParam(params, 1) {
		return
	}
	name := params[0]
	if !config.ValidProfileName(name) {
		sm.sendDeleteResult(name, fmt.Errorf("invalid profile name %q (allowed: letters, digits, _ and -)", name))
		return
	}
	active := config.Config.General.Profile
	if active == "" {
		active = "default"
	}
	// Only the ACTIVE profile's DB handle is held open by the manager, so
	// only tearing it down is needed to unlock the file. Deleting a
	// non-active profile must not kill a live connection.
	if name == active {
		sm.teardownProfile()
	}
	err := config.RemoveProfileDb(name)
	if err == nil && name == active {
		// Clear the pointer once the file is really gone, so a failed
		// removal never leaves the config pointing at a deleted DB.
		config.Config.General.Profile = ""
		config.SaveGeneralKeys(map[string]string{"profile": ""})
	}
	sm.sendDeleteResult(name, err)
}

// DeleteResultChan exposes the buffered channel the picker waits on for a
// "deleteprofile" outcome.
func (sm *SessionManager) DeleteResultChan() <-chan ProfileDeleteResult {
	return sm.deleteResultChan
}

// sendDeleteResult delivers a deletion result to the picker waiting on the
// buffered deleteResultChan.
func (sm *SessionManager) sendDeleteResult(name string, err error) {
	if sm.deleteResultChan != nil {
		sm.deleteResultChan <- ProfileDeleteResult{Name: name, Err: err}
	}
}

// switchProfile tears down the current connection and switches to another
// profile, restoring its session or showing the QR flow. The profile name is
// persisted to the config only after a successful login (see
// loginWithQRCode/loginWithConnection), so abandoning a new-profile QR does
// not strand the user on an empty profile.
func (sm *SessionManager) switchProfile(name string) {
	sm.teardownProfile()
	config.Config.General.Profile = name

	sm.uiHandler.PrintText(fmt.Sprintf("Switched to profile %q", name))
	if err := sm.login(); err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("WhatsApp connection failed: %v", err))
		sm.uiHandler.PrintText("Try using /reset to completely reset the connection")
	} else {
		sm.uiHandler.PrintText("Successfully connected to WhatsApp")
	}
}

// saveActiveProfile persists the current in-memory profile name to config.
// Called once a session for it is confirmed usable.
func (sm *SessionManager) saveActiveProfile() {
	config.SaveGeneralKeys(map[string]string{"profile": config.Config.General.Profile})
}

// teardownProfile disconnects and resets all per-profile state. The config
// Profile field is left untouched; callers set it.
func (sm *SessionManager) teardownProfile() {
	if sm.client != nil {
		if sm.client.IsConnected() {
			sm.client.Disconnect()
		}
		sm.client = nil
	}
	if sm.container != nil {
		if err := sm.container.Close(); err != nil {
			sm.uiHandler.PrintText("Warning: Couldn't close session store: " + err.Error())
		}
		sm.container = nil
	}
	sm.db.Reset()
	sm.currentReceiver = ""
	sm.statusInfo.ContactPresence = ""
	sm.uiHandler.ResetChat()
}

func (sm *SessionManager) markCurrentChatRead() {
	if sm.currentReceiver == "" {
		sm.printCommandUsage("read", "-> only works in a chat")
		return
	}
	if sm.client == nil || !sm.client.IsConnected() {
		sm.uiHandler.PrintError(errors.New("not connected to WhatsApp"))
		return
	}

	chatJID, err := types.ParseJID(sm.currentReceiver)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("invalid JID: %v", err))
		return
	}

	unreadMessages := sm.db.MarkChatRead(sm.currentReceiver)
	if len(unreadMessages) == 0 {
		sm.uiHandler.SetChats(sm.GetKnownChats())
		sm.uiHandler.PrintText("No unread messages in current chat")
		return
	}

	type senderBatch struct {
		sender    types.JID
		ids       []types.MessageID
		timestamp time.Time
	}
	batches := make(map[string]*senderBatch)
	for _, msg := range unreadMessages {
		sender := chatJID
		if strings.Contains(sm.currentReceiver, GROUPSUFFIX) && msg.SenderId != "" {
			sender, err = types.ParseJID(msg.SenderId)
			if err != nil {
				continue
			}
		}
		key := sender.String()
		if _, ok := batches[key]; !ok {
			batches[key] = &senderBatch{sender: sender}
		}
		batches[key].ids = append(batches[key].ids, types.MessageID(msg.Id))
		ts := time.Unix(int64(msg.Timestamp), 0)
		if ts.After(batches[key].timestamp) {
			batches[key].timestamp = ts
		}
	}

	for _, batch := range batches {
		if batch.timestamp.IsZero() {
			batch.timestamp = time.Now()
		}
		if err := sm.client.MarkRead(context.Background(), batch.ids, batch.timestamp, chatJID, batch.sender); err != nil {
			sm.uiHandler.PrintError(fmt.Errorf("failed to mark messages as read: %v", err))
		}
	}

	sm.uiHandler.SetChats(sm.GetKnownChats())
}

func (sm *SessionManager) downloadCommand(params []string, preview, show bool) {
	if !checkParam(params, 1) {
		name := "download"
		if preview && !show {
			name = "open"
		} else if show {
			name = "show"
		}
		sm.printCommandUsage(name, "[message-id[]")
		return
	}

	msg, ok := sm.db.GetMessage(params[0])
	if !ok {
		sm.uiHandler.PrintError(errors.New("message not found"))
		return
	}
	if show && msg.Kind != MessageKindImage {
		sm.uiHandler.PrintError(errors.New("show only works for image messages"))
		return
	}

	path, err := sm.downloadMessage(msg, preview)
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	if show {
		sm.uiHandler.PrintFile(path)
		return
	}
	if preview {
		sm.uiHandler.OpenFile(path)
		return
	}
	sm.uiHandler.PrintText("[::d] -> " + path + "[::-]")
}

func (sm *SessionManager) openMessageURL(params []string) {
	if !checkParam(params, 1) {
		sm.printCommandUsage("url", "[message-id[]")
		return
	}
	msg, ok := sm.db.GetMessage(params[0])
	if !ok {
		sm.uiHandler.PrintError(errors.New("message not found"))
		return
	}
	url := urlPattern.FindString(msg.Text)
	if url == "" {
		sm.uiHandler.PrintText("No URL found in message")
		return
	}
	sm.uiHandler.OpenFile(url)
}

func (sm *SessionManager) sendMediaCommand(params []string, kind MessageKind) {
	if sm.currentReceiver == "" {
		sm.printCommandUsage(commandNameForKind(kind), "-> only works in a chat")
		return
	}
	if !checkParam(params, 1) {
		sm.printCommandUsage(commandNameForKind(kind), "/path/to/file")
		return
	}
	path := strings.Join(params, " ")
	sm.uiHandler.PrintError(sm.sendMedia(sm.currentReceiver, path, kind))
}

func (sm *SessionManager) revokeMessage(params []string) {
	if !checkParam(params, 1) {
		sm.printCommandUsage("revoke", "[message-id[]")
		return
	}
	if sm.client == nil || !sm.client.IsConnected() {
		sm.uiHandler.PrintError(errors.New("not connected to WhatsApp"))
		return
	}

	msg, ok := sm.db.GetMessage(params[0])
	if !ok {
		sm.uiHandler.PrintError(errors.New("message not found"))
		return
	}
	chatJID, err := types.ParseJID(msg.ChatId)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("invalid chat JID: %v", err))
		return
	}
	if _, err = sm.client.RevokeMessage(context.Background(), chatJID, types.MessageID(msg.Id)); err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	sm.db.MarkMessageRevoked(msg.Id)
	if sm.currentReceiver == msg.ChatId {
		sm.uiHandler.NewScreen(sm.getMessages(msg.ChatId))
	}
	sm.uiHandler.PrintText("revoked: " + msg.Id)
}

func (sm *SessionManager) leaveCurrentGroup() {
	groupJID, err := sm.currentGroupJID()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	if err = sm.client.LeaveGroup(context.Background(), groupJID); err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	sm.uiHandler.PrintText("left group " + groupJID.String())
}

func (sm *SessionManager) createGroup(params []string) {
	if !checkParam(params, 1) {
		sm.printCommandUsage("create", "[user-id[] [user-id[] New Group Subject")
		sm.printCommandUsage("create", "New Group Subject")
		return
	}

	participants := make([]types.JID, 0)
	idx := 0
	for idx < len(params) && strings.Contains(params[idx], CONTACTSUFFIX) {
		participant, err := types.ParseJID(params[idx])
		if err != nil {
			sm.uiHandler.PrintError(fmt.Errorf("invalid user id %q: %v", params[idx], err))
			return
		}
		participants = append(participants, participant)
		idx++
	}

	name := strings.Join(params[idx:], " ")
	if name == "" {
		name = strings.Join(params, " ")
		participants = nil
	}

	groupInfo, err := sm.client.CreateGroup(context.Background(), whatsmeow.ReqCreateGroup{
		Name:         name,
		Participants: participants,
	})
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}

	sm.db.AddChat(Chat{
		Id:          groupInfo.JID.String(),
		IsGroup:     true,
		Name:        groupInfo.Name,
		LastMessage: time.Now().Unix(),
	})
	sm.uiHandler.SetChats(sm.GetKnownChats())
	sm.uiHandler.PrintText("created new group " + groupInfo.JID.String())
}

func (sm *SessionManager) updateCurrentGroupParticipants(params []string, action whatsmeow.ParticipantChange, command, success string) {
	groupJID, err := sm.currentGroupJID()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	if !checkParam(params, 1) {
		sm.printCommandUsage(command, "[user-id[]")
		return
	}

	participants := make([]types.JID, 0, len(params))
	for _, raw := range params {
		jid, err := types.ParseJID(raw)
		if err != nil {
			sm.uiHandler.PrintError(fmt.Errorf("invalid user id %q: %v", raw, err))
			return
		}
		participants = append(participants, jid)
	}

	if _, err = sm.client.UpdateGroupParticipants(context.Background(), groupJID, participants, action); err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	sm.uiHandler.PrintText(success + " for " + groupJID.String())
}

func (sm *SessionManager) updateCurrentGroupSubject(params []string) {
	groupJID, err := sm.currentGroupJID()
	if err != nil {
		sm.uiHandler.PrintError(err)
		return
	}
	if !checkParam(params, 1) {
		sm.printCommandUsage("subject", "new-subject -> in group chat")
		return
	}

	name := strings.Join(params, " ")
	if err = sm.client.SetGroupName(context.Background(), groupJID, name); err != nil {
		sm.uiHandler.PrintError(err)
		return
	}

	sm.db.AddChat(Chat{
		Id:      groupJID.String(),
		IsGroup: true,
		Name:    name,
	})
	sm.uiHandler.SetChats(sm.GetKnownChats())
	sm.uiHandler.PrintText("updated subject for " + groupJID.String())
}

func (sm *SessionManager) currentGroupJID() (types.JID, error) {
	if sm.currentReceiver == "" || !strings.Contains(sm.currentReceiver, GROUPSUFFIX) {
		return types.JID{}, errors.New("not a group")
	}
	return types.ParseJID(sm.currentReceiver)
}

func (sm *SessionManager) printCommandUsage(command, usage string) {
	sm.uiHandler.PrintText("[" + config.Config.Colors.Negative + "]Usage:[-] " + command + " " + usage)
}

func checkParam(arr []string, length int) bool {
	return arr != nil && len(arr) >= length
}

func (sm *SessionManager) getMessages(wid string) []Message {
	return sm.db.GetMessages(wid)
}

func (sm *SessionManager) sendText(wid, text string) {
	if sm.client == nil || !sm.client.IsConnected() {
		sm.uiHandler.PrintError(errors.New("not connected to WhatsApp"))
		return
	}

	receiver, err := types.ParseJID(wid)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("invalid JID: %v", err))
		return
	}

	raw := &waProto.Message{Conversation: proto.String(text)}
	sm.lastSent = time.Now()
	resp, err := sm.client.SendMessage(context.Background(), receiver, raw)
	if err != nil {
		sm.uiHandler.PrintError(fmt.Errorf("failed to send message: %v", err))
		return
	}

	newMsg := sm.outgoingMessageFromSendResponse(resp, wid, raw, MessageKindText, text, "", "")
	sm.db.AddMessage(newMsg, false)
	if sm.currentReceiver == wid {
		sm.uiHandler.NewMessage(newMsg)
	}
	sm.uiHandler.SetChats(sm.GetKnownChats())
}

func (sm *SessionManager) sendMedia(chatID, path string, kind MessageKind) error {
	if sm.client == nil || !sm.client.IsConnected() {
		return errors.New("not connected to WhatsApp")
	}

	data, mimeType, fileName, err := readUploadFile(path)
	if err != nil {
		return err
	}

	receiver, err := types.ParseJID(chatID)
	if err != nil {
		return fmt.Errorf("invalid JID: %v", err)
	}

	uploadResp, err := sm.client.Upload(context.Background(), data, uploadMediaType(kind))
	if err != nil {
		return fmt.Errorf("failed to upload file: %v", err)
	}

	fileLength := uploadResp.FileLength
	raw := &waProto.Message{}
	switch kind {
	case MessageKindImage:
		raw.ImageMessage = &waProto.ImageMessage{
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &fileLength,
		}
	case MessageKindVideo:
		raw.VideoMessage = &waProto.VideoMessage{
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &fileLength,
		}
	case MessageKindAudio:
		raw.AudioMessage = &waProto.AudioMessage{
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &fileLength,
			PTT:           proto.Bool(false),
		}
	case MessageKindDocument:
		raw.DocumentMessage = &waProto.DocumentMessage{
			Mimetype:      proto.String(mimeType),
			Title:         proto.String(fileName),
			FileName:      proto.String(fileName),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &fileLength,
		}
	default:
		return errors.New("unsupported media type")
	}

	sm.lastSent = time.Now()
	resp, err := sm.client.SendMessage(context.Background(), receiver, raw)
	if err != nil {
		return fmt.Errorf("failed to send media message: %v", err)
	}

	text := mediaDisplayText(kind, fileName, "")
	newMsg := sm.outgoingMessageFromSendResponse(resp, chatID, raw, kind, text, mimeType, fileName)
	sm.db.AddMessage(newMsg, false)
	if sm.currentReceiver == chatID {
		sm.uiHandler.NewMessage(newMsg)
	}
	sm.uiHandler.SetChats(sm.GetKnownChats())
	return nil
}

func (sm *SessionManager) outgoingMessageFromSendResponse(resp whatsmeow.SendResponse, chatID string, raw *waProto.Message, kind MessageKind, text, mimeType, fileName string) Message {
	selfID := ""
	if sm.client != nil && sm.client.Store != nil && sm.client.Store.ID != nil {
		selfID = sm.client.Store.ID.String()
	}

	contactID := chatID
	if strings.Contains(chatID, GROUPSUFFIX) {
		contactID = selfID
	}

	return Message{
		Id:           string(resp.ID),
		ChatId:       chatID,
		SenderId:     selfID,
		ContactId:    contactID,
		ContactName:  sm.db.GetIdName(contactID),
		ContactShort: sm.db.GetIdShort(contactID),
		Timestamp:    uint64(resp.Timestamp.Unix()),
		FromMe:       true,
		Text:         text,
		Kind:         kind,
		MimeType:     mimeType,
		FileName:     fileName,
		RawMessage:   raw,
	}
}

func (sm *SessionManager) chatMuted(chatID string) bool {
	chat, ok := sm.db.GetChat(chatID)
	return ok && chat.IsMuted()
}

func notify(title, message string) error {
	if !config.Config.General.EnableNotifications {
		return nil
	} else if config.Config.General.UseTerminalBell {
		_, err := fmt.Printf("\a")
		return err
	}
	return beeep.Notify(title, message, "")
}

type eventHandler struct {
	sm *SessionManager
}

func (eh *eventHandler) Handle(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		eh.handleLiveMessage(v)
	case *events.HistorySync:
		eh.handleHistorySync(v)
	case *events.Connected:
		eh.sm.StatusChannel <- StatusMsg{true, nil}
	case *events.Disconnected:
		eh.sm.StatusChannel <- StatusMsg{false, nil}
	case *events.LoggedOut:
		eh.sm.StatusChannel <- StatusMsg{false, nil}
		eh.sm.uiHandler.PrintText("Logged out: " + fmt.Sprintf("%v", v.Reason))
	case *events.Mute:
		eh.handleMuteEvent(v)
	case *events.Presence:
		eh.handlePresenceEvent(v)
	}
}

// handleMuteEvent syncs chat mutes made on other devices (0 = unmuted,
// -1 = muted forever, else mute expiry timestamp).
func (eh *eventHandler) handleMuteEvent(evt *events.Mute) {
	if evt == nil || evt.Action == nil {
		return
	}
	until := int64(0)
	if evt.Action.GetMuted() {
		until = evt.Action.GetMuteEndTimestamp()
		if until == 0 {
			until = -1
		}
	}
	eh.sm.db.SetChatMuted(evt.JID.String(), until)
}

// handlePresenceEvent tracks the online status of the currently open chat.
func (eh *eventHandler) handlePresenceEvent(evt *events.Presence) {
	if evt == nil || eh.sm.currentReceiver == "" {
		return
	}
	if evt.From.String() != eh.sm.currentReceiver {
		return
	}
	if evt.Unavailable {
		if evt.LastSeen.IsZero() {
			eh.sm.statusInfo.ContactPresence = "offline"
		} else {
			eh.sm.statusInfo.ContactPresence = "last seen " + evt.LastSeen.Format("15:04")
		}
	} else {
		eh.sm.statusInfo.ContactPresence = "online"
	}
	eh.sm.uiHandler.SetStatus(eh.sm.statusInfo)
}

// isStaleMessage reports whether msg predates this login session (unix
// seconds, atomic for the manager/event goroutine split). Zero start means
// unset — fail open toward visibility.
func (sm *SessionManager) isStaleMessage(msg Message) bool {
	start := sm.sessionStart.Load()
	if start == 0 {
		return false
	}
	return int64(msg.Timestamp) < start-int64(staleMessageGrace/time.Second)
}

func (eh *eventHandler) handleLiveMessage(evt *events.Message) {
	msg, action, ok := eh.normalizeEventMessage(evt)
	if !ok {
		return
	}

	switch action {
	case "revoke":
		if eh.sm.db.MarkMessageRevoked(msg.Id) && eh.sm.currentReceiver == msg.ChatId {
			eh.sm.uiHandler.NewScreen(eh.sm.getMessages(msg.ChatId))
		}
		eh.sm.uiHandler.SetChats(eh.sm.GetKnownChats())
		return
	case "ignore":
		return
	}

	// Gate stale offline messages: bump sidebar unread only, skip transcript.
	if eh.sm.isStaleMessage(msg) {
		if !msg.FromMe {
			eh.sm.db.BumpChatUnread(msg.ChatId)
		}
		eh.sm.uiHandler.SetChats(eh.sm.GetKnownChats())
		return
	}

	markUnread := !msg.FromMe && msg.ChatId != eh.sm.currentReceiver
	isNew := eh.sm.db.AddMessage(msg, markUnread)
	if msg.ChatId == eh.sm.currentReceiver {
		if isNew {
			eh.sm.uiHandler.NewMessage(msg)
		} else {
			eh.sm.uiHandler.NewScreen(eh.sm.getMessages(msg.ChatId))
		}
	} else if markUnread && !eh.sm.chatMuted(msg.ChatId) && msg.Timestamp > uint64(time.Now().Unix()-30) {
		if err := notify(msg.ContactShort, msg.Text); err != nil {
			eh.sm.uiHandler.PrintError(err)
		}
	}
	eh.sm.uiHandler.SetChats(eh.sm.GetKnownChats())
}

func (eh *eventHandler) handleHistorySync(evt *events.HistorySync) {
	if evt == nil || evt.Data == nil {
		return
	}
	// WhatsApp's phone pushes the full history in chained chunks
	// (INITIAL_BOOTSTRAP, RECENT, FULL, ...). Ingesting those floods the
	// transcript with days-old messages on every session. Chat history is
	// loaded manually via /backlog, which the server answers with an
	// ON_DEMAND sync — that is the only type we import.
	if evt.Data.GetSyncType() != waHistorySync.HistorySync_ON_DEMAND {
		// Still import conversation metadata so the sidebar lists chats
		// (names, unread counts, mute state) without their old messages.
		for _, conv := range evt.Data.GetConversations() {
			eh.importChatMetadata(conv)
		}
		eh.sm.uiHandler.SetChats(eh.sm.GetKnownChats())
		return
	}
	touched := false
	for _, conv := range evt.Data.GetConversations() {
		eh.importChatMetadata(conv)
		// Only ingest messages for the chat an in-flight /backlog fetch
		// asked about. The server can push ON_DEMAND chunks spontaneously
		// and a chunk may carry other conversations; importing those would
		// load history nobody requested.
		if !eh.sm.backlogAccepts(conv.GetID()) {
			continue
		}
		touched = true

		for _, histMsg := range conv.GetMessages() {
			webMsg := histMsg.GetMessage()
			if webMsg == nil {
				continue
			}
			chatJID, err := types.ParseJID(conv.GetID())
			if err != nil {
				continue
			}
			if eh.sm.client == nil {
				// History sync can arrive while the client is torn down
				// (profile switch); nothing to parse against.
				continue
			}
			parsed, err := eh.sm.client.ParseWebMessage(chatJID, webMsg)
			if err != nil {
				continue
			}
			msg, action, ok := eh.normalizeEventMessage(parsed)
			if !ok || action != "" {
				continue
			}
			eh.sm.db.AddMessage(msg, false)
		}
		eh.sm.db.UpdateChatUnread(conv.GetID(), int(conv.GetUnreadCount()))
	}

	eh.sm.uiHandler.SetChats(eh.sm.GetKnownChats())
	// Re-render only when this chunk actually touched the open chat —
	// otherwise a late chunk for another chat swaps the transcript.
	if touched && eh.sm.currentReceiver != "" {
		eh.sm.uiHandler.NewScreen(eh.sm.getMessages(eh.sm.currentReceiver))
	}
}

// importChatMetadata records a history-sync conversation's sidebar state
// (name, unread count, mute, last-activity timestamp) without importing any
// of its messages.
func (eh *eventHandler) importChatMetadata(conv *waHistorySync.Conversation) {
	chatID := conv.GetID()
	if chatID == "" {
		chatID = conv.GetNewJID()
	}
	if chatID == "" {
		return
	}
	chatJID, err := types.ParseJID(chatID)
	if err != nil {
		return
	}
	chatName := conv.GetName()
	if chatName == "" {
		chatName = conv.GetDisplayName()
	}
	if chatName == "" {
		chatName = eh.sm.getChatName(chatJID)
	}
	lastMessage := int64(conv.GetLastMsgTimestamp())
	if lastMessage == 0 {
		lastMessage = int64(conv.GetConversationTimestamp())
	}
	eh.sm.db.AddChat(Chat{
		Id:          chatID,
		IsGroup:     chatJID.Server == types.GroupServer,
		Name:        chatName,
		Unread:      int(conv.GetUnreadCount()),
		LastMessage: lastMessage,
		MutedUntil:  int64(conv.GetMuteEndTime()),
	})
}

func (eh *eventHandler) normalizeEventMessage(evt *events.Message) (Message, string, bool) {
	if evt == nil || evt.Message == nil {
		return Message{}, "ignore", false
	}

	raw := unwrapMessage(evt.Message)
	if raw == nil {
		return Message{}, "ignore", false
	}

	if protocol := raw.GetProtocolMessage(); protocol != nil {
		if protocol.GetType() == waProto.ProtocolMessage_REVOKE && protocol.GetKey() != nil {
			return Message{
				Id:     protocol.GetKey().GetID(),
				ChatId: evt.Info.Chat.String(),
			}, "revoke", true
		}
		return Message{}, "ignore", false
	}

	msg, ok := eh.messageFromInfo(evt.Info, raw)
	return msg, "", ok
}

func (eh *eventHandler) messageFromInfo(info types.MessageInfo, raw *waProto.Message) (Message, bool) {
	if raw == nil {
		return Message{}, false
	}

	chatID := info.Chat.String()
	if chatID == "" {
		return Message{}, false
	}

	contactID, contactName, contactShort := eh.contactForMessage(info)
	msg := Message{
		Id:           string(info.ID),
		ChatId:       chatID,
		SenderId:     info.Sender.String(),
		ContactId:    contactID,
		ContactName:  contactName,
		ContactShort: contactShort,
		Timestamp:    uint64(info.Timestamp.Unix()),
		FromMe:       info.IsFromMe,
		RawMessage:   raw,
	}

	switch {
	case raw.GetConversation() != "":
		msg.Kind = MessageKindText
		msg.Text = raw.GetConversation()
		return msg, true
	case raw.GetExtendedTextMessage() != nil:
		ext := raw.GetExtendedTextMessage()
		msg.Kind = MessageKindText
		msg.Text = ext.GetText()
		msg.Forwarded = ext.GetContextInfo().GetIsForwarded()
		return msg, true
	case raw.GetImageMessage() != nil:
		image := raw.GetImageMessage()
		msg.Kind = MessageKindImage
		msg.MimeType = image.GetMimetype()
		msg.Text = mediaDisplayText(MessageKindImage, "", image.GetCaption())
		msg.Forwarded = image.GetContextInfo().GetIsForwarded()
		return msg, true
	case raw.GetVideoMessage() != nil:
		video := raw.GetVideoMessage()
		msg.Kind = MessageKindVideo
		msg.MimeType = video.GetMimetype()
		msg.Text = mediaDisplayText(MessageKindVideo, "", video.GetCaption())
		msg.Forwarded = video.GetContextInfo().GetIsForwarded()
		return msg, true
	case raw.GetAudioMessage() != nil:
		audio := raw.GetAudioMessage()
		msg.Kind = MessageKindAudio
		msg.MimeType = audio.GetMimetype()
		msg.Text = mediaDisplayText(MessageKindAudio, "", "")
		msg.Forwarded = audio.GetContextInfo().GetIsForwarded()
		return msg, true
	case raw.GetDocumentMessage() != nil:
		doc := raw.GetDocumentMessage()
		msg.Kind = MessageKindDocument
		msg.MimeType = doc.GetMimetype()
		msg.FileName = doc.GetFileName()
		msg.Text = mediaDisplayText(MessageKindDocument, doc.GetFileName(), doc.GetCaption())
		msg.Forwarded = doc.GetContextInfo().GetIsForwarded()
		return msg, true
	default:
		return Message{}, false
	}
}

func (eh *eventHandler) contactForMessage(info types.MessageInfo) (string, string, string) {
	if info.IsGroup {
		id := info.Sender.String()
		return id, eh.getContactName(info.Sender), eh.getContactShort(info.Sender)
	}
	id := info.Chat.String()
	chat := info.Chat
	return id, eh.getContactName(chat), eh.getContactShort(chat)
}

func (eh *eventHandler) getContactName(jid types.JID) string {
	if eh.sm.client != nil && eh.sm.client.Store != nil && eh.sm.client.Store.Contacts != nil {
		contact, err := eh.sm.client.Store.Contacts.GetContact(context.Background(), jid)
		if err == nil && contact.Found {
			if contact.FullName != "" {
				return contact.FullName
			}
			if contact.PushName != "" {
				return contact.PushName
			}
		}
	}
	return eh.sm.db.GetIdName(jid.String())
}

func (eh *eventHandler) getContactShort(jid types.JID) string {
	if eh.sm.client != nil && eh.sm.client.Store != nil && eh.sm.client.Store.Contacts != nil {
		contact, err := eh.sm.client.Store.Contacts.GetContact(context.Background(), jid)
		if err == nil && contact.Found && contact.PushName != "" {
			return contact.PushName
		}
	}
	return eh.sm.db.GetIdShort(jid.String())
}

func (sm *SessionManager) downloadMessage(msg Message, preview bool) (string, error) {
	if sm.client == nil || !sm.client.IsConnected() {
		return "", errors.New("not connected to WhatsApp")
	}

	downloadable, err := downloadableFromMessage(msg)
	if err != nil {
		return "", err
	}

	baseDir := config.Config.General.DownloadPath
	if preview {
		baseDir = config.Config.General.PreviewPath
	}
	if err = os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}

	fileName := downloadFileName(msg)
	fullPath := filepath.Join(baseDir, fileName)
	if _, err = os.Stat(fullPath); err == nil {
		return fullPath, nil
	}

	data, err := sm.client.Download(context.Background(), downloadable)
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(fullPath, data, 0o644); err != nil {
		return "", err
	}
	return fullPath, nil
}

func downloadableFromMessage(msg Message) (whatsmeow.DownloadableMessage, error) {
	if msg.RawMessage == nil {
		return nil, errors.New("This is not a downloadable message")
	}
	switch msg.Kind {
	case MessageKindImage:
		if media := msg.RawMessage.GetImageMessage(); media != nil {
			return media, nil
		}
	case MessageKindVideo:
		if media := msg.RawMessage.GetVideoMessage(); media != nil {
			return media, nil
		}
	case MessageKindAudio:
		if media := msg.RawMessage.GetAudioMessage(); media != nil {
			return media, nil
		}
	case MessageKindDocument:
		if media := msg.RawMessage.GetDocumentMessage(); media != nil {
			return media, nil
		}
	}
	return nil, errors.New("This is not a downloadable message")
}

func readUploadFile(path string) ([]byte, string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", "", err
	}
	fileName := filepath.Base(path)
	mimeType := detectMimeType(path, data)
	return data, mimeType, fileName, nil
}

func detectMimeType(path string, data []byte) string {
	if len(data) == 0 {
		if extType := mime.TypeByExtension(filepath.Ext(path)); extType != "" {
			return stripMimeParams(extType)
		}
		return "application/octet-stream"
	}
	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}
	detected := stripMimeParams(http.DetectContentType(sample))
	if extType := mime.TypeByExtension(filepath.Ext(path)); extType != "" {
		extType = stripMimeParams(extType)
		if detected == "application/octet-stream" || strings.HasPrefix(extType, "audio/") || strings.HasPrefix(extType, "video/") {
			return extType
		}
	}
	return detected
}

func stripMimeParams(value string) string {
	if idx := strings.Index(value, ";"); idx >= 0 {
		return value[:idx]
	}
	return value
}

func downloadFileName(msg Message) string {
	if msg.FileName != "" {
		safeName := path.Base(strings.ReplaceAll(msg.FileName, "\\", "/"))
		if safeName != "" && safeName != "." && safeName != ".." {
			return safeName
		}
	}
	ext := ""
	if msg.MimeType != "" {
		if exts, err := mime.ExtensionsByType(msg.MimeType); err == nil && len(exts) > 0 {
			ext = exts[0]
		}
	}
	return msg.Id + ext
}

func uploadMediaType(kind MessageKind) whatsmeow.MediaType {
	switch kind {
	case MessageKindImage:
		return whatsmeow.MediaImage
	case MessageKindVideo:
		return whatsmeow.MediaVideo
	case MessageKindAudio:
		return whatsmeow.MediaAudio
	default:
		return whatsmeow.MediaDocument
	}
}

func commandNameForKind(kind MessageKind) string {
	switch kind {
	case MessageKindImage:
		return "sendimage"
	case MessageKindVideo:
		return "sendvideo"
	case MessageKindAudio:
		return "sendaudio"
	default:
		return "upload"
	}
}

func mediaDisplayText(kind MessageKind, fileName, caption string) string {
	label := "[FILE]"
	switch kind {
	case MessageKindImage:
		label = "[IMAGE]"
	case MessageKindVideo:
		label = "[VIDEO]"
	case MessageKindAudio:
		label = "[AUDIO]"
	case MessageKindDocument:
		label = "[DOCUMENT]"
	}
	parts := []string{label}
	if fileName != "" && kind == MessageKindDocument {
		parts = append(parts, fileName)
	}
	if caption != "" {
		parts = append(parts, caption)
	}
	return strings.Join(parts, " ")
}

// isSidebarChat reports whether a chat ID belongs in the sidebar: real 1:1
// accounts and groups only — never status, broadcasts or newsletters.
func isSidebarChat(id string) bool {
	return id != "" &&
		!strings.HasSuffix(id, "@broadcast") &&
		!strings.HasSuffix(id, "@newsletter")
}

func (sm *SessionManager) GetKnownChats() []Chat {
	chats := sm.db.GetChatIds()
	filtered := make([]Chat, 0, len(chats))
	for _, chat := range chats {
		if !isSidebarChat(chat.Id) {
			continue
		}
		if config.Config.General.ChatListMode == "recency_only" && chat.LastMessage == 0 {
			continue
		}
		filtered = append(filtered, chat)
	}
	return filtered
}

func (sm *SessionManager) PinChat(chat Chat) {
	if chat.Id == "" || chat.Id == STATUSSUFFIX {
		return
	}
	if chat.Name == "" {
		chat.Name = sm.db.GetIdName(chat.Id)
	}
	if chat.LastMessage == 0 {
		chat.LastMessage = time.Now().Unix()
	}
	sm.db.AddChat(chat)
	sm.uiHandler.SetChats(sm.GetKnownChats())
}

// ResolveSearchChats searches the in-memory chat and contact stores only —
// no database or network I/O, so it stays instant on every keystroke.
func (sm *SessionManager) ResolveSearchChats(keyword string) []Chat {
	needle := strings.TrimSpace(strings.ToLower(keyword))
	if needle == "" {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]Chat, 0)
	for _, chat := range sm.db.GetChatIds() {
		if !isSidebarChat(chat.Id) {
			continue
		}
		if chatMatches(chat, needle) {
			seen[chat.Id] = struct{}{}
			out = append(out, chat)
		}
	}
	for _, contact := range sm.db.GetAllContacts() {
		if !strings.HasSuffix(contact.Id, CONTACTSUFFIX) {
			continue
		}
		if _, ok := seen[contact.Id]; ok {
			continue
		}
		chat := Chat{Id: contact.Id, IsGroup: false, Name: contact.Name}
		if !chatMatches(chat, needle) {
			continue
		}
		seen[contact.Id] = struct{}{}
		out = append(out, chat)
	}
	return out
}

func chatMatches(chat Chat, needle string) bool {
	if strings.Contains(strings.ToLower(chat.Name), needle) {
		return true
	}
	return strings.Contains(strings.ToLower(chat.Id), needle)
}

func unwrapMessage(msg *waProto.Message) *waProto.Message {
	for msg != nil {
		switch {
		case msg.GetEphemeralMessage() != nil:
			msg = msg.GetEphemeralMessage().GetMessage()
		case msg.GetViewOnceMessage() != nil:
			msg = msg.GetViewOnceMessage().GetMessage()
		case msg.GetViewOnceMessageV2() != nil:
			msg = msg.GetViewOnceMessageV2().GetMessage()
		case msg.GetViewOnceMessageV2Extension() != nil:
			msg = msg.GetViewOnceMessageV2Extension().GetMessage()
		case msg.GetDeviceSentMessage() != nil:
			msg = msg.GetDeviceSentMessage().GetMessage()
		default:
			return msg
		}
	}
	return nil
}
