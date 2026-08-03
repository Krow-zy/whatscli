package messages

import (
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

func selfCheckUnwrapMessage() {
	base := &waProto.Message{Conversation: proto.String("hello")}
	wrapped := &waProto.Message{EphemeralMessage: &waProto.FutureProofMessage{Message: &waProto.Message{ViewOnceMessageV2: &waProto.FutureProofMessage{Message: &waProto.Message{DeviceSentMessage: &waProto.DeviceSentMessage{Message: base}}}}}}
	out := unwrapMessage(wrapped)
	if out == nil || out.GetConversation() != "hello" {
		panic("unwrapMessage failed")
	}
}

func init() { selfCheckUnwrapMessage() }
