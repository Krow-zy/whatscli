package main

import (
	"strings"
	"testing"

	"github.com/normen/whatscli/messages"
	"github.com/rivo/tview"
)

func TestChatPanelNavigationWraps(t *testing.T) {
	chatRoot = tview.NewTreeNode("Chats").SetExpanded(true)
	contacts := tview.NewTreeNode("Contacts").SetSelectable(false).SetExpanded(true)
	groups := tview.NewTreeNode("Groups").SetSelectable(false).SetExpanded(true)
	first := tview.NewTreeNode("A")
	second := tview.NewTreeNode("B")
	contacts.AddChild(first)
	groups.AddChild(second)
	chatRoot.AddChild(contacts)
	chatRoot.AddChild(groups)
	treeView = tview.NewTreeView().SetRoot(chatRoot).SetCurrentNode(first)

	handleChatPanelUp(nil)
	if treeView.GetCurrentNode() != second {
		t.Fatalf("expected wrap across sections to last node")
	}

	handleChatPanelDown(nil)
	if treeView.GetCurrentNode() != first {
		t.Fatalf("expected wrap across sections to first node")
	}
}

func TestToggleHelpRestoresChat(t *testing.T) {
	textView = tview.NewTextView().SetDynamicColors(true).SetRegions(true)
	currentReceiver = messages.Chat{Id: "123@s.whatsapp.net", Name: "Alice"}
	curRegions = []messages.Message{
		{Id: "m1", ChatId: "123@s.whatsapp.net", Text: "hello", Timestamp: 1700000000},
	}
	helpVisible = false

	ToggleHelp()
	if !helpVisible {
		t.Fatalf("expected help to be visible after first toggle")
	}
	if strings.Contains(textView.GetText(false), "hello") {
		t.Fatalf("expected help screen to replace chat content")
	}

	ToggleHelp()
	if helpVisible {
		t.Fatalf("expected help to be hidden after second toggle")
	}
	if got := textView.GetText(false); !strings.Contains(got, "hello") {
		t.Fatalf("expected chat content restored, got %q", got)
	}
}
