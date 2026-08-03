package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"code.rocketnine.space/tslocum/cbind"
	"github.com/gdamore/tcell/v2"
	"github.com/normen/whatscli/config"
	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
	"github.com/skratchdot/open-golang/open"
	"github.com/zyedidia/clipboard"
)

var VERSION string = "v1.2.0"

var sndTxt string = ""
var currentReceiver messages.Chat = messages.Chat{}
var curRegions []messages.Message

var textView *tview.TextView
var treeView *tview.TreeView
var textInput *tview.InputField
var chatSearchInput *tview.InputField
var topBar *tview.TextView
var infoBar *tview.TextView

var chatRoot *tview.TreeNode
var app *tview.Application
var sidebarFlex *tview.Flex
var sidebarTitle string = "Contacts"
var searchMode bool = false
var helpVisible bool = false
var searchResults []messages.Chat

var sessionManager *messages.SessionManager
var keyBindings *cbind.Configuration
var uiHandler messages.UiMessageHandler

func main() {
	config.InitConfig()
	uiHandler = UiHandler{}
	sessionManager = &messages.SessionManager{}
	sessionManager.Init(uiHandler)

	app = tview.NewApplication()
	setTviewTheme()
	gridLayout := tview.NewGrid()
	gridLayout.SetRows(1, 0, 1)
	gridLayout.SetColumns(config.Config.Ui.ChatSidebarWidth, 0, config.Config.Ui.ChatSidebarWidth)
	gridLayout.SetBorders(true)
	gridLayout.SetBackgroundColor(uiColor(config.Config.Colors.Background))
	gridLayout.SetBordersColor(uiColor(config.Config.Colors.Borders))

	cmdPrefix := config.Config.General.CmdPrefix
	topBar = tview.NewTextView()
	topBar.SetDynamicColors(true)
	topBar.SetScrollable(false)
	topBar.SetText("[" + config.Config.Colors.ListHeader + "::b]WhatsCLI " + VERSION + "[-::-]  [" + config.Config.Colors.Text + "::d]Type " + cmdPrefix + "help for help[-::-]")
	topBar.SetBackgroundColor(uiColor(config.Config.Colors.Background))

	infoBar = tview.NewTextView()
	infoBar.SetDynamicColors(true)
	UpdateStatusBar(messages.SessionStatus{})

	textView = tview.NewTextView().SetDynamicColors(true).SetRegions(true).SetWordWrap(true).SetChangedFunc(func() { app.Draw() })
	textView.SetBackgroundColor(uiColor(config.Config.Colors.Background))
	textView.SetTextColor(uiColor(config.Config.Colors.Text))

	textInput = tview.NewInputField()
	textInput.SetLabel("Reply: ")
	textInput.SetBackgroundColor(uiColor(config.Config.Colors.Background))
	textInput.SetFieldBackgroundColor(uiColor(config.Config.Colors.InputBackground))
	textInput.SetFieldTextColor(uiColor(config.Config.Colors.InputText))
	textInput.SetLabelColor(uiColor(config.Config.Colors.ListHeader))
	textInput.SetChangedFunc(func(change string) { sndTxt = change })
	textInput.SetDoneFunc(EnterCommand)
	textInput.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyDown {
			offset, _ := textView.GetScrollOffset()
			textView.ScrollTo(offset+1, 0)
			return nil
		}
		if event.Key() == tcell.KeyUp {
			offset, _ := textView.GetScrollOffset()
			textView.ScrollTo(offset-1, 0)
			return nil
		}
		if event.Key() == tcell.KeyPgDn {
			offset, _ := textView.GetScrollOffset()
			textView.ScrollTo(offset+10, 0)
			return nil
		}
		if event.Key() == tcell.KeyPgUp {
			offset, _ := textView.GetScrollOffset()
			textView.ScrollTo(offset-10, 0)
			return nil
		}
		return event
	})

	chatSearchInput = tview.NewInputField()
	chatSearchInput.SetLabel("Search: ")
	chatSearchInput.SetBackgroundColor(uiColor(config.Config.Colors.Background))
	chatSearchInput.SetFieldBackgroundColor(uiColor(config.Config.Colors.SearchBackground))
	chatSearchInput.SetFieldTextColor(uiColor(config.Config.Colors.Text))
	chatSearchInput.SetLabelColor(uiColor(config.Config.Colors.ListHeader))
	chatSearchInput.SetChangedFunc(func(change string) { applyChatSearch(change) })
	chatSearchInput.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			chatSearchInput.SetText("")
			clearChatSearch()
			app.SetFocus(treeView)
			return nil
		}
		if event.Key() == tcell.KeyEnter {
			handleOpenChat(event)
			return nil
		}
		return event
	})

	sidebarFlex = tview.NewFlex().SetDirection(tview.FlexRow)
	sidebarFlex.AddItem(chatSearchInput, 1, 0, false)
	sidebarFlex.AddItem(MakeTree(), 0, 1, false)

	gridLayout.AddItem(topBar, 0, 0, 1, 4, 0, 0, false)
	gridLayout.AddItem(infoBar, 2, 0, 1, 1, 0, 0, false)
	gridLayout.AddItem(sidebarFlex, 1, 0, 1, 1, 0, 0, false)
	gridLayout.AddItem(textView, 1, 1, 1, 3, 0, 0, false)
	gridLayout.AddItem(textInput, 2, 1, 1, 3, 0, 0, false)

	PrintHelp()
	app.SetRoot(gridLayout, true)
	if os.Getenv("WHATSCLI_DEBUG_BG") == "1" {
		app.SetRoot(makeDebugBackgroundView(), true)
	}
	app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		applyFocusVisuals()
		fillScreen(screen, uiColor(config.Config.Colors.Background))
		return false
	})
	app.EnableMouse(true)
	app.SetFocus(textInput)
	if err := sessionManager.StartManager(); err != nil {
		PrintError(err)
	}
	LoadShortcuts()
	app.Run()
}

func makeDebugBackgroundView() tview.Primitive {
	bg := uiColor(config.Config.Colors.Background)
	pages := tview.NewFlex().SetDirection(tview.FlexRow)
	pages.AddItem(debugBlock("WHITE TEST", tcell.ColorWhite, tcell.ColorBlack), 0, 1, false)
	pages.AddItem(debugBlock("RED TEST", tcell.ColorRed, tcell.ColorBlack), 0, 1, false)
	pages.AddItem(debugBlock("GREEN TEST", tcell.ColorGreen, tcell.ColorBlack), 0, 1, false)
	pages.AddItem(debugBlock("BLUE TEST", tcell.ColorBlue, tcell.ColorWhite), 0, 1, false)
	pages.AddItem(debugBlock("APP BG TEST", bg, uiColor(config.Config.Colors.Text)), 0, 1, false)
	pages.AddItem(debugBlock("INPUT BG TEST", uiColor(config.Config.Colors.InputBackground), uiColor(config.Config.Colors.InputText)), 0, 1, false)
	return pages
}

func debugBlock(label string, bg, fg tcell.Color) *tview.TextView {
	v := tview.NewTextView().SetDynamicColors(false)
	v.SetTextAlign(tview.AlignCenter)
	v.SetBackgroundColor(bg)
	v.SetTextColor(fg)
	v.SetText("\n" + label + "\n")
	return v
}

func setTviewTheme() {
	bg := uiColor(config.Config.Colors.Background)
	border := uiColor(config.Config.Colors.Borders)
	text := uiColor(config.Config.Colors.Text)
	accent := uiColor(config.Config.Colors.ListContact)
	fill := uiColor(config.Config.Colors.InputBackground)
	inverse := uiColor(config.Config.Colors.InputText)
	tview.Styles.PrimitiveBackgroundColor = bg
	tview.Styles.ContrastBackgroundColor = fill
	tview.Styles.MoreContrastBackgroundColor = fill
	tview.Styles.BorderColor = border
	tview.Styles.TitleColor = text
	tview.Styles.GraphicsColor = border
	tview.Styles.PrimaryTextColor = text
	tview.Styles.SecondaryTextColor = accent
	tview.Styles.TertiaryTextColor = accent
	tview.Styles.InverseTextColor = inverse
	tview.Styles.ContrastSecondaryTextColor = inverse
}

func fillScreen(screen tcell.Screen, color tcell.Color) {
	w, h := screen.Size()
	style := tcell.StyleDefault.Background(color).Foreground(color)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			screen.SetContent(x, y, ' ', nil, style)
		}
	}
}

func uiColor(name string) tcell.Color {
	if strings.HasPrefix(name, "#") && len(name) == 7 {
		return tcell.NewHexColor(int32((parseHexByte(name[1:3]) << 16) | (parseHexByte(name[3:5]) << 8) | parseHexByte(name[5:7]))).TrueColor()
	}
	if c, ok := tcell.ColorNames[strings.ToLower(name)]; ok {
		return c
	}
	return tcell.ColorWhite
}

func parseHexByte(s string) int64 {
	v, err := strconv.ParseInt(s, 16, 32)
	if err != nil {
		return 255
	}
	return v
}

func MakeTree() *tview.TreeView {
	chatRoot = tview.NewTreeNode(sidebarTitle).SetColor(uiColor(config.Config.Colors.ListHeader)).SetExpanded(true)
	treeView = tview.NewTreeView().SetRoot(chatRoot).SetCurrentNode(chatRoot)
	treeView.SetBackgroundColor(uiColor(config.Config.Colors.Background))
	treeView.SetGraphicsColor(uiColor(config.Config.Colors.Borders))
	treeView.SetChangedFunc(func(node *tview.TreeNode) {
		reference := node.GetReference()
		if reference == nil {
			SetDisplayedChat(messages.Chat{})
			return
		}
		children := node.GetChildren()
		if len(children) == 0 {
			SetDisplayedChat(reference.(messages.Chat))
		} else {
			node.SetExpanded(!node.IsExpanded())
		}
	})
	return treeView
}

func handleFocusMessage(ev *tcell.EventKey) *tcell.EventKey {
	if !textView.HasFocus() {
		app.SetFocus(textView)
		if len(curRegions) > 0 {
			textView.Highlight(curRegions[len(curRegions)-1].Id)
		}
	}
	UpdateStatusBar(messages.SessionStatus{})
	return nil
}
func handleFocusInput(ev *tcell.EventKey) *tcell.EventKey {
	ResetMsgSelection()
	if !textInput.HasFocus() {
		app.SetFocus(textInput)
	}
	UpdateStatusBar(messages.SessionStatus{})
	return nil
}
func handleFocusContacts(ev *tcell.EventKey) *tcell.EventKey {
	ResetMsgSelection()
	if !treeView.HasFocus() {
		app.SetFocus(treeView)
	}
	UpdateStatusBar(messages.SessionStatus{})
	return nil
}
func handleSwitchPanels(ev *tcell.EventKey) *tcell.EventKey {
	ResetMsgSelection()
	if chatSearchInput.HasFocus() {
		app.SetFocus(treeView)
	} else if treeView.HasFocus() {
		app.SetFocus(textInput)
	} else {
		app.SetFocus(chatSearchInput)
	}
	UpdateStatusBar(messages.SessionStatus{})
	return nil
}
func handleCommand(command string) func(ev *tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		sessionManager.CommandChannel <- messages.Command{command, nil}
		return nil
	}
}

func handleCopyUser(ev *tcell.EventKey) *tcell.EventKey {
	if hls := textView.GetHighlights(); len(hls) > 0 {
		for _, val := range curRegions {
			if val.Id == hls[0] {
				clipboard.WriteAll(val.ContactId, "clipboard")
				PrintText("copied id of " + val.ContactName + " to clipboard")
			}
		}
		ResetMsgSelection()
	} else if currentReceiver.Id != "" {
		clipboard.WriteAll(currentReceiver.Id, "clipboard")
		PrintText("copied id of " + currentReceiver.Name + " to clipboard")
	}
	return nil
}

func handlePasteUser(ev *tcell.EventKey) *tcell.EventKey {
	if clip, err := safeReadClipboard(); err == nil {
		textInput.SetText(textInput.GetText() + " " + clip)
	} else {
		PrintError(err)
	}
	return nil
}
func safeReadClipboard() (clip string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("clipboard paste is unavailable: %v", rec)
		}
	}()
	return clipboard.ReadAll("clipboard")
}
func handleQuit(ev *tcell.EventKey) *tcell.EventKey {
	sessionManager.CommandChannel <- messages.Command{"disconnect", nil}
	app.Stop()
	return nil
}
func handleHelp(ev *tcell.EventKey) *tcell.EventKey { ToggleHelp(); return nil }
func handleMessageCommand(command string) func(ev *tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		hls := textView.GetHighlights()
		if len(hls) > 0 {
			sessionManager.CommandChannel <- messages.Command{command, []string{hls[0]}}
			ResetMsgSelection()
			app.SetFocus(textInput)
		}
		return nil
	}
}
func handleMessagesMove(amount int) func(ev *tcell.EventKey) *tcell.EventKey {
	return func(ev *tcell.EventKey) *tcell.EventKey {
		if len(curRegions) == 0 {
			return nil
		}
		hls := textView.GetHighlights()
		if len(hls) > 0 {
			if newId := GetOffsetMsgId(hls[0], amount); newId != "" {
				textView.Highlight(newId)
			}
		} else if amount < 0 {
			textView.Highlight(curRegions[0].Id)
		} else {
			textView.Highlight(curRegions[len(curRegions)-1].Id)
		}
		textView.ScrollToHighlight()
		return nil
	}
}
func handleChatPanelUp(ev *tcell.EventKey) *tcell.EventKey {
	children := chatRoot.GetChildren()
	if len(children) == 0 {
		return nil
	}
	current := treeView.GetCurrentNode()
	idx := 0
	for i, n := range children {
		if n == current {
			idx = i
			break
		}
	}
	if idx > 0 {
		treeView.SetCurrentNode(children[idx-1])
	} else {
		treeView.SetCurrentNode(children[len(children)-1])
	}
	return nil
}
func handleChatPanelDown(ev *tcell.EventKey) *tcell.EventKey {
	children := chatRoot.GetChildren()
	if len(children) == 0 {
		return nil
	}
	current := treeView.GetCurrentNode()
	idx := -1
	for i, n := range children {
		if n == current {
			idx = i
			break
		}
	}
	if idx >= 0 && idx < len(children)-1 {
		treeView.SetCurrentNode(children[idx+1])
	} else {
		treeView.SetCurrentNode(children[0])
	}
	return nil
}
func handleOpenChat(ev *tcell.EventKey) *tcell.EventKey {
	if treeView == nil {
		return nil
	}
	node := treeView.GetCurrentNode()
	if node == nil || node.GetReference() == nil {
		return nil
	}
	recv, ok := node.GetReference().(messages.Chat)
	if !ok {
		return nil
	}
	SetDisplayedChat(recv)
	sessionManager.PinChat(recv)
	app.SetFocus(textInput)
	return nil
}
func handleMessagesLast(ev *tcell.EventKey) *tcell.EventKey {
	if len(curRegions) == 0 {
		return nil
	}
	textView.Highlight(curRegions[len(curRegions)-1].Id)
	textView.ScrollToHighlight()
	return nil
}
func handleMessagesFirst(ev *tcell.EventKey) *tcell.EventKey {
	if len(curRegions) == 0 {
		return nil
	}
	textView.Highlight(curRegions[0].Id)
	textView.ScrollToHighlight()
	return nil
}
func handleExitMessages(ev *tcell.EventKey) *tcell.EventKey {
	if len(curRegions) == 0 {
		return nil
	}
	ResetMsgSelection()
	app.SetFocus(textInput)
	return nil
}

func applyChatSearch(query string) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		clearChatSearch()
		return
	}
	searchMode = true
	searchResults = sessionManager.ResolveSearchChats(trimmed)
	renderChatNodes(searchResults, "Contacts")
	if len(searchResults) > 0 {
		treeView.SetCurrentNode(chatRoot.GetChildren()[0])
	}
}
func clearChatSearch() {
	searchMode = false
	searchResults = nil
	renderChatNodes(sessionManager.GetKnownChats(), "Contacts")
}
func renderChatNodes(ids []messages.Chat, title string) {
	sidebarTitle = title
	chatRoot.SetText(title)
	chatRoot.ClearChildren()
	oldId := currentReceiver.Id
	for _, element := range ids {
		if element.Id == messages.STATUSSUFFIX {
			continue
		}
		name := element.Name
		if name == "" {
			name = strings.TrimSuffix(strings.TrimSuffix(element.Id, messages.GROUPSUFFIX), messages.CONTACTSUFFIX)
		}
		if element.Unread > 0 {
			name += " ([" + config.Config.Colors.UnreadCount + "]" + fmt.Sprint(element.Unread) + "[-])"
		}
		node := tview.NewTreeNode(name).SetReference(element).SetSelectable(true)
		node.SetColor(uiColor(config.Config.Colors.ListContact))
		if element.IsGroup {
			node.SetColor(uiColor(config.Config.Colors.ListGroup))
		}
		if element.Id == currentReceiver.Id {
			node.SetColor(uiColor(config.Config.Colors.InputText))
			node.SetText("[" + config.Config.Colors.InputText + ":" + config.Config.Colors.ListSelected + "] " + name + " [-:-:-]")
		}
		if element.Id == oldId {
			currentReceiver = element
		}
		chatRoot.AddChild(node)
		if element.Id == currentReceiver.Id {
			treeView.SetCurrentNode(node)
		}
	}
}

func LoadShortcuts() {
	keyBindings = cbind.NewConfiguration()
	_ = keyBindings.Set(config.Config.Keymap.FocusMessages, handleFocusMessage)
	_ = keyBindings.Set(config.Config.Keymap.FocusInput, handleFocusInput)
	_ = keyBindings.Set(config.Config.Keymap.FocusChats, handleFocusContacts)
	_ = keyBindings.Set("Ctrl+f", func(ev *tcell.EventKey) *tcell.EventKey {
		app.SetFocus(chatSearchInput)
		UpdateStatusBar(messages.SessionStatus{})
		return nil
	})
	_ = keyBindings.Set(config.Config.Keymap.SwitchPanels, handleSwitchPanels)
	_ = keyBindings.Set(config.Config.Keymap.CommandRead, handleCommand("read"))
	_ = keyBindings.Set(config.Config.Keymap.Copyuser, handleCopyUser)
	_ = keyBindings.Set(config.Config.Keymap.Pasteuser, handlePasteUser)
	_ = keyBindings.Set(config.Config.Keymap.CommandBacklog, handleCommand("backlog"))
	_ = keyBindings.Set(config.Config.Keymap.CommandConnect, handleCommand("login"))
	_ = keyBindings.Set(config.Config.Keymap.CommandQuit, handleQuit)
	_ = keyBindings.Set(config.Config.Keymap.CommandHelp, handleHelp)
	keyBindings.SetKey(tcell.ModNone, tcell.KeyF1, handleHelp)
	app.SetInputCapture(keyBindings.Capture)

	keysMessages := cbind.NewConfiguration()
	_ = keysMessages.Set(config.Config.Keymap.MessageDownload, handleMessageCommand("download"))
	_ = keysMessages.Set(config.Config.Keymap.MessageOpen, handleMessageCommand("open"))
	_ = keysMessages.Set(config.Config.Keymap.Copyuser, handleCopyUser)
	_ = keysMessages.Set(config.Config.Keymap.Pasteuser, handlePasteUser)
	_ = keysMessages.Set(config.Config.Keymap.MessageShow, handleMessageCommand("show"))
	_ = keysMessages.Set(config.Config.Keymap.MessageUrl, handleMessageCommand("url"))
	_ = keysMessages.Set(config.Config.Keymap.MessageInfo, handleMessageCommand("info"))
	_ = keysMessages.Set(config.Config.Keymap.MessageRevoke, handleMessageCommand("revoke"))
	keysMessages.SetKey(tcell.ModNone, tcell.KeyEscape, handleExitMessages)
	keysMessages.SetKey(tcell.ModNone, tcell.KeyUp, handleMessagesMove(-1))
	keysMessages.SetKey(tcell.ModNone, tcell.KeyDown, handleMessagesMove(1))
	keysMessages.SetKey(tcell.ModNone, tcell.KeyPgUp, handleMessagesMove(-10))
	keysMessages.SetKey(tcell.ModNone, tcell.KeyPgDn, handleMessagesMove(10))
	keysMessages.SetRune(tcell.ModNone, 'k', handleMessagesMove(-1))
	keysMessages.SetRune(tcell.ModNone, 'j', handleMessagesMove(1))
	keysMessages.SetRune(tcell.ModNone, 'g', handleMessagesFirst)
	keysMessages.SetRune(tcell.ModNone, 'G', handleMessagesLast)
	keysMessages.SetRune(tcell.ModCtrl, 'u', handleMessagesMove(-10))
	keysMessages.SetRune(tcell.ModCtrl, 'd', handleMessagesMove(10))
	textView.SetInputCapture(keysMessages.Capture)

	keysChatPanel := cbind.NewConfiguration()
	keysChatPanel.SetKey(tcell.ModNone, tcell.KeyUp, handleChatPanelUp)
	keysChatPanel.SetKey(tcell.ModNone, tcell.KeyDown, handleChatPanelDown)
	_ = keysChatPanel.Set(config.Config.Keymap.OpenChat, handleOpenChat)
	keysChatPanel.SetRune(tcell.ModCtrl, 'u', handleChatPanelUp)
	keysChatPanel.SetRune(tcell.ModCtrl, 'd', handleChatPanelDown)
	treeView.SetInputCapture(keysChatPanel.Capture)
}

func PrintHelp() {
	cmdPrefix := config.Config.General.CmdPrefix
	textView.Clear()
	fmt.Fprintln(textView, "[-::u]Keys:[-::-]")
	fmt.Fprintln(textView, "Global")
	fmt.Fprintln(textView, "[::b]Up/Down[::-] = Move in active list")
	fmt.Fprintln(textView, "[::b]"+config.Config.Keymap.SwitchPanels+"[::-] = Switch focus")
	fmt.Fprintln(textView, "[::b]Ctrl+f[::-] = Focus contact search")
	fmt.Fprintln(textView, "[::b]"+config.Config.Keymap.OpenChat+"[::-] = Open selected chat")
	fmt.Fprintln(textView, "[::b]"+config.Config.Keymap.FocusMessages+"[::-] = Focus message actions")
	fmt.Fprintln(textView, "[::b]"+config.Config.Keymap.FocusChats+"[::-] = Focus contacts list")
	fmt.Fprintln(textView, "[::b]"+config.Config.Keymap.FocusInput+"[::-] = Focus reply box")
	fmt.Fprintln(textView, "[::b]"+config.Config.Keymap.CommandQuit+"[::-] = Exit app")
	fmt.Fprintln(textView, "Message panel")
	fmt.Fprintln(textView, "[::b]Up/Down[::-] = Select message")
	fmt.Fprintln(textView, "[::b]"+config.Config.Keymap.MessageDownload+"[::-] = Download attachment")
	fmt.Fprintln(textView, "[::b]"+config.Config.Keymap.MessageOpen+"[::-] = Download & open attachment")
	fmt.Fprintln(textView, "[::b]"+config.Config.Keymap.MessageShow+"[::-] = Show image using "+config.Config.General.ShowCommand)
	fmt.Fprintln(textView, "[::b]"+config.Config.Keymap.MessageUrl+"[::-] = Open URL from selected message")
	fmt.Fprintln(textView, "[::b]"+config.Config.Keymap.MessageRevoke+"[::-] = Revoke selected message")
	fmt.Fprintln(textView, "[::b]"+config.Config.Keymap.MessageInfo+"[::-] = Show selected message info\n")
	fmt.Fprintln(textView, "Config file in ->", config.GetConfigFilePath())
	fmt.Fprintln(textView, "Type [::b]"+cmdPrefix+"commands[::-] to see all commands")
	fmt.Fprintln(textView, "Pick a contact on the left, then type in the reply box at the bottom.")
	helpVisible = true
}

// ToggleHelp switches between the help screen and the current chat, so the
// chat no longer gets wiped when help is opened.
func ToggleHelp() {
	if helpVisible {
		restoreChatScreen()
	} else {
		PrintHelp()
	}
}

func restoreChatScreen() {
	helpVisible = false
	textView.Clear()
	if len(curRegions) > 0 {
		textView.SetText(getMessagesString(curRegions))
		textView.ScrollToEnd()
		return
	}
	if currentReceiver.Id != "" {
		PrintText("[::d]~~~ no messages, press " + config.Config.Keymap.CommandBacklog + " to load backlog if available ~~~[::-]")
		return
	}
	PrintHelp()
}

func PrintCommands() {
	cmdPrefix := config.Config.General.CmdPrefix
	textView.Clear()
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[-::u]Commands:[-::-]")
	fmt.Fprintln(textView, "")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"disconnect[::-] = Close the connection")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"logout[::-] = Remove login data from computer")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"reset[::-] = Remove stored session and reconnect cleanly")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"quit [::-]or[::b] "+config.Config.Keymap.CommandQuit+"[::-] = Exit app")
	fmt.Fprintln(textView, "Chat")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"backlog [::-]or[::b] "+config.Config.Keymap.CommandBacklog+"[::-] = load next "+fmt.Sprint(config.Config.General.BacklogMsgQuantity)+" previous messages")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"read [::-]or[::b] "+config.Config.Keymap.CommandRead+"[::-] = mark new messages in chat as read")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"upload[::-] /path/to/file = Upload any file as document")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"sendimage[::-] /path/to/file = Send image message")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"sendvideo[::-] /path/to/file = Send video message")
	fmt.Fprintln(textView, "[::b] "+cmdPrefix+"sendaudio[::-] /path/to/file = Send audio message")
	fmt.Fprintln(textView, "Use [::b]"+config.Config.Keymap.Copyuser+"[::-] to copy a selected user id to clipboard")
	fmt.Fprintln(textView, "Use [::b]"+config.Config.Keymap.Pasteuser+"[::-] to paste clipboard to text input")
}

func EnterCommand(key tcell.Key) {
	if sndTxt == "" {
		return
	}
	if key == tcell.KeyEsc {
		textInput.SetText("")
		return
	}
	cmdPrefix := config.Config.General.CmdPrefix
	if sndTxt == cmdPrefix+"help" {
		ToggleHelp()
		textInput.SetText("")
		return
	}
	if sndTxt == cmdPrefix+"commands" {
		PrintCommands()
		textInput.SetText("")
		return
	}
	if sndTxt == cmdPrefix+"quit" {
		sessionManager.CommandChannel <- messages.Command{"disconnect", nil}
		app.Stop()
		return
	}
	if strings.HasPrefix(sndTxt, cmdPrefix) {
		cmd := strings.TrimPrefix(sndTxt, cmdPrefix)
		var params []string
		if strings.Index(cmd, " ") >= 0 {
			cmdParts := strings.Split(cmd, " ")
			cmd = cmdParts[0]
			params = cmdParts[1:]
		}
		sessionManager.CommandChannel <- messages.Command{cmd, params}
		textInput.SetText("")
		return
	}
	if currentReceiver.Id == "" {
		PrintText("No active chat. Open one from Contacts first (Enter / Tab / Ctrl+e).")
		textInput.SetText("")
		return
	}
	sessionManager.CommandChannel <- messages.Command{Name: "send", Params: []string{currentReceiver.Id, sndTxt}}
	textInput.SetText("")
}

func GetOffsetMsgId(curId string, offset int) string {
	if len(curRegions) == 0 {
		return ""
	}
	for idx, val := range curRegions {
		if val.Id == curId {
			arrPos := idx + offset
			if len(curRegions) > arrPos && arrPos >= 0 {
				return curRegions[arrPos].Id
			}
		}
	}
	if offset > 0 {
		return curRegions[0].Id
	}
	return curRegions[len(curRegions)-1].Id
}
func ResetMsgSelection() {
	if len(textView.GetHighlights()) > 0 {
		textView.Highlight("")
	}
	textView.ScrollToEnd()
}
func PrintText(txt string) { fmt.Fprintln(textView, txt) }
func PrintError(err error) {
	if err != nil {
		fmt.Fprintln(textView, "["+config.Config.Colors.Negative+"]", err.Error(), "[-]")
	}
}
func PrintErrorMsg(text string, err error) {
	if err != nil {
		fmt.Fprintln(textView, "["+config.Config.Colors.Negative+"]", text, err.Error(), "[-]")
	}
}

func PrintImage(path string) {
	cmdParts := append(strings.Split(config.Config.General.ShowCommand, " "), path)
	var cmd *exec.Cmd
	if len(cmdParts) > 1 {
		cmd = exec.Command(cmdParts[0], cmdParts[1:]...)
	} else if len(cmdParts) > 0 {
		cmd = exec.Command(cmdParts[0])
	}
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		if err = cmd.Start(); err == nil {
			reader := bufio.NewReader(stdout)
			io.Copy(tview.ANSIWriter(textView), reader)
			return
		}
	}
	PrintError(err)
}

func UpdateStatusBar(statusInfo messages.SessionStatus) {
	out := " "
	if statusInfo.Connected {
		out += "[" + config.Config.Colors.Positive + "]online[-]"
	} else {
		out += "[" + config.Config.Colors.Negative + "]offline[-]"
	}
	out += " [::d](" + fmt.Sprint(statusInfo.BatteryCharge) + "%"
	if statusInfo.BatteryLoading {
		out += " [" + config.Config.Colors.Positive + "]L[-]"
	} else {
		out += " [" + config.Config.Colors.Negative + "]l[-]"
	}
	if statusInfo.BatteryPowersave {
		out += " [" + config.Config.Colors.Negative + "]S[-]"
	} else {
		out += " [" + config.Config.Colors.Positive + "]s[-]"
	}
	out += ")[::-] " + statusInfo.LastSeen
	if currentReceiver.Name != "" {
		out += " [::d]chat:[::-] " + currentReceiver.Name
	}
	focusLabel := ""
	if chatSearchInput != nil && chatSearchInput.HasFocus() {
		focusLabel = "search"
	} else if treeView != nil && treeView.HasFocus() {
		focusLabel = "chats"
	} else if textView != nil && textView.HasFocus() {
		focusLabel = "messages"
	} else if textInput != nil && textInput.HasFocus() {
		focusLabel = "input"
	}
	if focusLabel != "" {
		out += " [::d]focus:[::-] [" + config.Config.Colors.InputBackground + "::b]" + focusLabel + "[-::-]"
	}
	if infoBar != nil {
		infoBar.SetText(out)
	}
	applyFocusVisuals()
}

func applyFocusVisuals() {
	if textInput != nil {
		textInput.SetLabelColor(uiColor(config.Config.Colors.ListHeader))
		textInput.SetFieldBackgroundColor(uiColor(config.Config.Colors.Background))
		textInput.SetFieldTextColor(uiColor(config.Config.Colors.Text))
	}
	if chatSearchInput != nil {
		chatSearchInput.SetLabelColor(uiColor(config.Config.Colors.ListHeader))
		chatSearchInput.SetFieldBackgroundColor(uiColor(config.Config.Colors.SearchBackground))
		chatSearchInput.SetFieldTextColor(uiColor(config.Config.Colors.Text))
	}
	if textInput != nil && textInput.HasFocus() {
		textInput.SetLabelColor(uiColor(config.Config.Colors.InputBackground))
		textInput.SetFieldBackgroundColor(uiColor(config.Config.Colors.InputBackground))
		textInput.SetFieldTextColor(uiColor(config.Config.Colors.InputText))
	}
	if chatSearchInput != nil && chatSearchInput.HasFocus() {
		chatSearchInput.SetLabelColor(uiColor(config.Config.Colors.InputBackground))
		chatSearchInput.SetFieldBackgroundColor(uiColor(config.Config.Colors.InputBackground))
		chatSearchInput.SetFieldTextColor(uiColor(config.Config.Colors.InputText))
	}
	if chatRoot != nil {
		if treeView != nil && treeView.HasFocus() {
			chatRoot.SetText("[" + config.Config.Colors.InputBackground + "::b]" + sidebarTitle + "[-::-]")
		} else {
			chatRoot.SetText(sidebarTitle)
		}
	}
}

func SetDisplayedChat(wid messages.Chat) {
	currentReceiver = wid
	helpVisible = false
	textView.Clear()
	textView.SetTitle(wid.Name)
	sessionManager.CommandChannel <- messages.Command{"select", []string{currentReceiver.Id}}
	app.SetFocus(textInput)
	UpdateStatusBar(messages.SessionStatus{})
}
func getMessagesString(msgs []messages.Message) string {
	out := ""
	for _, msg := range msgs {
		out += getTextMessageString(&msg) + "\n"
	}
	return out
}
func getTextMessageString(msg *messages.Message) string {
	text := tview.Escape(msg.Text)
	if msg.Forwarded {
		text = "[" + config.Config.Colors.ForwardedText + "]" + text + "[-]"
	}
	tim := time.Unix(int64(msg.Timestamp), 0).Format("02-01-06 15:04:05")
	if msg.FromMe {
		return "[\"" + msg.Id + "\"][" + config.Config.Colors.Timestamp + "](" + tim + ") [" + config.Config.Colors.ChatMe + "::b]Me: [-::-]" + text + "[\"\"]"
	}
	return "[\"" + msg.Id + "\"][" + config.Config.Colors.Timestamp + "](" + tim + ") [" + config.Config.Colors.ChatContact + "::b]" + msg.ContactShort + ": [-::-]" + text + "[\"\"]"
}

type UiHandler struct{}

func (u UiHandler) NewMessage(msg messages.Message) {
	go app.QueueUpdateDraw(func() {
		if helpVisible {
			restoreChatScreen()
		}
		curRegions = append(curRegions, msg)
		PrintText(getTextMessageString(&msg))
	})
}
func (u UiHandler) NewScreen(msgs []messages.Message) {
	go app.QueueUpdateDraw(func() {
		helpVisible = false
		textView.Clear()
		screen := getMessagesString(msgs)
		textView.SetText(screen)
		curRegions = msgs
		if screen == "" {
			if currentReceiver.Id == "" {
				PrintHelp()
			} else {
				PrintText("[::d]~~~ no messages, press " + config.Config.Keymap.CommandBacklog + " to load backlog if available ~~~[::-]")
			}
		}
	})
}
func (u UiHandler) SetChats(ids []messages.Chat) {
	go app.QueueUpdateDraw(func() {
		if searchMode {
			renderChatNodes(searchResults, "Contacts")
		} else {
			renderChatNodes(ids, "Contacts")
		}
	})
}
func (u UiHandler) PrintError(err error)  { PrintError(err) }
func (u UiHandler) PrintText(msg string)  { PrintText(msg) }
func (u UiHandler) PrintFile(path string) { go app.QueueUpdateDraw(func() { PrintImage(path) }) }
func (u UiHandler) OpenFile(path string)  { open.Run(path) }
func (u UiHandler) SetStatus(status messages.SessionStatus) {
	go app.QueueUpdateDraw(func() { UpdateStatusBar(status) })
}
func (u UiHandler) GetWriter() io.Writer { return textView }
