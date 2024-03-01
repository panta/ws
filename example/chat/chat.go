//go:build example

package chat

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/panta/ws"
)

const (
	// EventSendMessage is the event name for new chat messages sent
	EventSendMessage = "send_message"
	// EventNewMessage is a response to send_message
	EventNewMessage = "new_message"
	// EventChangeRoom is event when switching rooms
	EventChangeRoom = "change_room"
)

// SendMessageEvent is the payload sent in the send_message event.
type SendMessageEvent struct {
	Message string `json:"message"`
	From    string `json:"from"`
}

// NewMessageEvent is returned when responding to send_message
type NewMessageEvent struct {
	SendMessageEvent
	Sent time.Time `json:"sent"`
}

type ChangeRoomEvent struct {
	Name string `json:"name"`
}

// SendMessageHandler will send out a message to all other participants in the chat
func SendMessageHandler(event ws.Message, c *ws.Connection) error {
	// Marshal Payload into wanted format
	var chatevent SendMessageEvent
	if err := json.Unmarshal(event.Payload, &chatevent); err != nil {
		return fmt.Errorf("bad payload in request: %v", err)
	}

	// Prepare an Outgoing Message to others
	var broadMessage NewMessageEvent

	broadMessage.Sent = time.Now()
	broadMessage.Message = chatevent.Message
	broadMessage.From = chatevent.From

	data, err := json.Marshal(broadMessage)
	if err != nil {
		return fmt.Errorf("failed to marshal broadcast message: %v", err)
	}

	// Place payload into an Message
	var outgoingEvent ws.Message
	outgoingEvent.Payload = data
	outgoingEvent.Type = EventNewMessage
	// Broadcast to all other Connections
	clients := c.Manager().Connections()
	for client := range clients {
		// Only send to clients inside the same chatroom
		cUserDataIf := c.UserData()
		cChatroom, _ := cUserDataIf.(string)
		clientUserDataIf := client.UserData()
		clientChatroom, _ := clientUserDataIf.(string)

		if clientChatroom == cChatroom {
			client.SendMessage(outgoingEvent)
		}
	}

	return nil
}

// ChatRoomHandler will handle switching of chatrooms between clients
func ChatRoomHandler(event ws.Message, c *ws.Connection) error {
	// Marshal Payload into wanted format
	var changeRoomEvent ChangeRoomEvent
	if err := json.Unmarshal(event.Payload, &changeRoomEvent); err != nil {
		return fmt.Errorf("bad payload in request: %v", err)
	}

	// Add Connection to chat room
	c.SetUserData(changeRoomEvent.Name)

	return nil
}
