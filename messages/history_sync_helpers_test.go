package messages

import (
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// makeTestConversation builds a history-sync conversation for a group chat
// containing two plain text messages: one an hour old, one fresh.
func makeTestConversation() *waHistorySync.Conversation {
	now := uint64(time.Now().Unix())
	return &waHistorySync.Conversation{
		DisplayName:           proto.String("Test Group"),
		ID:                    proto.String("120363@g.us"),
		UnreadCount:           proto.Uint32(2),
		ConversationTimestamp: proto.Uint64(now),
		Messages: []*waHistorySync.HistorySyncMsg{
			{Message: &waWeb.WebMessageInfo{
				Key:              &waCommon.MessageKey{RemoteJID: proto.String("120363@g.us"), ID: proto.String("m1"), FromMe: proto.Bool(false), Participant: proto.String("9999@s.whatsapp.net")},
				MessageTimestamp: proto.Uint64(now - 3600),
				Message:          &waE2E.Message{Conversation: proto.String("older hello")},
			}},
			{Message: &waWeb.WebMessageInfo{
				Key:              &waCommon.MessageKey{RemoteJID: proto.String("120363@g.us"), ID: proto.String("m2"), FromMe: proto.Bool(false), Participant: proto.String("9999@s.whatsapp.net")},
				MessageTimestamp: proto.Uint64(now),
				Message:          &waE2E.Message{Conversation: proto.String("fresh hello")},
			}},
		},
	}
}

// makeTestHistorySyncEvent wraps a conversation in a HistorySync event with
// the given sync type.
func makeTestHistorySyncEvent(syncType int32, conv *waHistorySync.Conversation) *events.HistorySync {
	return &events.HistorySync{
		Data: &waHistorySync.HistorySync{
			SyncType:      waHistorySync.HistorySync_HistorySyncType(syncType).Enum(),
			Conversations: []*waHistorySync.Conversation{conv},
		},
	}
}
