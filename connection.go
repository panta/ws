package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

var (
	// pongWait is how long we will await for a pong response
	pongWait = 10 * time.Second

	// pingInterval has to be less than pongWait
	// (otherwise we'd start sending other pings before getting pong responses)
	pingInterval = (pongWait * 9) / 10
)

// ConnectionList is a map used to help manage a map of connections (connections).
type ConnectionList map[*Connection]bool

// Connection is a websocket connection (either for a server or a client).
type Connection struct {
	logger zerolog.Logger
	ID     string // short ID identifying this connection

	// the websocket connection
	connection *websocket.Conn

	closeSent bool

	// manager is the manager used to manage the client connection
	manager *Manager

	// egress is an unbuffered channel act as a locker, used to avoid concurrent writes on the WebSocket.
	// (The WebSocket connection is only allowed to have one concurrent writer)
	egress chan Message

	// per-connection/client application/service specific user data
	userData any
}

// NewConnection is used to initialize a new websocket connection.
func NewConnection(conn *websocket.Conn, manager *Manager) *Connection {
	id := NewID()
	return &Connection{
		logger: manager.logger.With().
			Str("id", id).
			Str("remote-addr", conn.RemoteAddr().String()).
			Str("role", manager.role.String()).
			Logger(),
		ID:         id,
		connection: conn,
		manager:    manager,
		egress:     make(chan Message),
	}
}

// Close cleanly closes the connection
func (c *Connection) Close() {
	if c.connection != nil {
		_ = c.connection.Close()
	}
	if c.manager != nil {
		c.manager.RemoveConnection(c)
	}
}

// Manager returns the manager associated with this connection.
func (c *Connection) Manager() *Manager {
	return c.manager
}

// UserData returns the user-data associated with this connection.
func (c *Connection) UserData() any {
	return c.userData
}

// SetUserData sets the user-data associated with this connection.
func (c *Connection) SetUserData(value any) {
	c.userData = value
}

func (c *Connection) SendMessage(outgoing Message) {
	c.egress <- outgoing
}

// pongHandler is used to handle PongMessages for the Connection
func (c *Connection) pongHandler(pongMsg string) error {
	// Current time + Pong Wait time
	c.logger.Debug().Msg("received pong")
	return c.connection.SetReadDeadline(time.Now().Add(pongWait))
}

func (c *Connection) sendCloseMessage() error {
	if c.closeSent {
		return nil
	}
	if err := c.connection.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
		// Log that the connection is closed and the reason
		c.logger.Error().Err(err).Msg("close message write error")
		return fmt.Errorf("error sending close message: %w", err)
	}
	c.closeSent = true
	return nil
}

// ReadMessages will read messages for this connection in a cycle and handle them appropriately.
// This is supposed to be run in a goroutine.
func (c *Connection) ReadMessages(ctx context.Context) {
	defer func() {
		// gracefully close the connection on exit
		c.manager.RemoveConnection(c)
	}()

	// Set max message size in bytes
	if c.manager != nil && c.manager.maxMessageSize > 0 {
		c.connection.SetReadLimit(c.manager.maxMessageSize)
	}

	// Configure Wait time for Pong response, use Current time + pongWait
	// This has to be done here to set the first initial timer.
	if err := c.connection.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		c.logger.Error().Err(err).Msg("can't set read deadline")
		return
	}
	// Configure how to handle Pong responses
	c.connection.SetPongHandler(c.pongHandler)

	// Loop Forever
	for {
		select {
		case <-ctx.Done():
			c.logger.Debug().Err(ctx.Err()).Msgf("context canceled: %v", ctx.Err())
			return // exit from goroutine
		default:
			// don't block
		}

		// ReadMessage is used to read the next message in queue
		// in the connection
		messageType, payload, err := c.connection.ReadMessage()

		if err != nil {
			// If Connection is closed, we will receive an error here
			// We only want to log Strange errors, but not simple Disconnection
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.logger.Error().Err(err).Msg("error reading message")
			}
			break // Break the loop to close conn & Cleanup
		}
		c.logger.Debug().
			Int("message-type", messageType).
			Str("payload", string(payload)).Msg("received message")

		// Unmarshal incoming data into a Message struct
		var request Message
		if err := json.Unmarshal(payload, &request); err != nil {
			c.logger.Error().Err(err).Msg("error unmarshalling data into message")
			break // Breaking the connection here might be harsh xD
		}

		// Route the Message
		if err := c.manager.routeMessage(request, c); err != nil {
			c.logger.Error().Err(err).Msg("error handling message")
		}
	}
}

// WriteMessages listens on the channel for new messages and pipes them onto the websocket connection.
func (c *Connection) WriteMessages(ctx context.Context) {
	// Create a ticker that triggers a ping at given interval
	ticker := time.NewTicker(pingInterval)

	defer func() {
		// stop sending pings
		ticker.Stop()

		// Gracefully close on exit
		c.manager.RemoveConnection(c)
	}()

	for {
		select {
		case <-ctx.Done():
			c.logger.Debug().Err(ctx.Err()).Msgf("context canceled: %v", ctx.Err())
			_ = c.sendCloseMessage()
			return // exit from goroutine

		case msg, ok := <-c.egress:
			// Ok will be false in case the egress channel is closed
			if !ok {
				// Manager has closed this connection channel, so communicate that to the peer
				_ = c.sendCloseMessage()
				return // exit from goroutine
			}

			data, err := json.Marshal(msg)
			if err != nil {
				c.logger.Error().Err(err).Msg("error marshalling message")
				return // closes the connection, should we really
			}
			// Write a Regular text message to the connection
			if err := c.connection.WriteMessage(websocket.TextMessage, data); err != nil {
				c.logger.Error().Err(err).Msg("error sending data")
			}
			c.logger.Debug().Msg("sent message")

		case <-ticker.C:
			c.logger.Debug().Msg("send ping")
			// Send the Ping
			if err := c.connection.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				c.logger.Error().Err(err).Msg("error sending ping")
				return // return to break this goroutine triggering cleanup
			}
		}
	}
}
