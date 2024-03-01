// Package ws provides a higher-level abstraction to work with WebSockets.
package ws

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

var (
	ErrMessageNotSupported = errors.New("this message type is not supported")
)

type ManagerRole int

const (
	ManagerRoleUnset ManagerRole = iota
	ManagerRoleServer
	ManagerRoleClient
)

func (role ManagerRole) String() string {
	switch role {
	case ManagerRoleUnset:
		return "unset"
	case ManagerRoleServer:
		return "server"
	case ManagerRoleClient:
		return "client"
	default:
		return "unknown"
	}
}

// MessageHandler is the type for message handlers differentiated on type.
type MessageHandler func(msg Message, c *Connection) error

const (
	defaultBufferSize           = 1024
	defaultMaxMessageSize int64 = (5 * 1024)
)

// Manager is used on the server side to hold references to all active connections, handle broadcasting, etc.
type Manager struct {
	logger zerolog.Logger
	role   ManagerRole

	connections ConnectionList

	// Using a syncMutex here to be able to lcok state before editing connections
	// Could also use Channels to block
	sync.RWMutex

	// handlers holds the callbacks to handle different message types
	handlers map[string]MessageHandler

	// valid origins (if specified)
	origins         []string
	readBufferSize  int
	writeBufferSize int
	maxMessageSize  int64

	// application/service specific user data
	userData any
}

type ManagerOption func(m *Manager)

// NewManager creates and returns a Manager.
func NewManager(role ManagerRole, opts ...ManagerOption) *Manager {
	m := &Manager{
		role:            role,
		connections:     make(ConnectionList),
		handlers:        make(map[string]MessageHandler),
		readBufferSize:  defaultBufferSize,
		writeBufferSize: defaultBufferSize,
		maxMessageSize:  defaultMaxMessageSize,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

func ManagerCheckOrigin(logger zerolog.Logger, origins []string) ManagerOption {
	return func(m *Manager) {
		m.origins = origins
	}
}

func ManagerLogger(logger zerolog.Logger) ManagerOption {
	return func(m *Manager) {
		m.logger = logger
	}
}

func ManagerBuffers(readBufferSize int, writeBufferSize int) ManagerOption {
	return func(m *Manager) {
		m.readBufferSize = readBufferSize
		m.writeBufferSize = writeBufferSize
	}
}

func ManagerMaxMessageSize(maxMessageSize int64) ManagerOption {
	return func(m *Manager) {
		m.maxMessageSize = maxMessageSize
	}
}

// Role returns the manager role (server or client).
func (m *Manager) Role() ManagerRole {
	return m.role
}

// UserData returns the user-data associated with this manager.
func (m *Manager) UserData() any {
	return m.userData
}

// SetUserData sets the user-data associated with this manager.
func (m *Manager) SetUserData(value any) {
	m.userData = value
}

func (m *Manager) AddHandler(msgType string, handler MessageHandler) *Manager {
	m.handlers[msgType] = handler
	return m
}

func (m *Manager) getUpgrader() websocket.Upgrader {
	checkOrigin := func(r *http.Request) bool {
		if (m.origins == nil) || len(m.origins) == 0 {
			return true
		}
		origin := r.Header.Get("Origin")
		for _, allowed := range m.origins {
			if origin == allowed {
				return true
			}
		}
		return false
	}

	return websocket.Upgrader{
		// Apply the Origin Checker
		CheckOrigin:     checkOrigin,
		ReadBufferSize:  m.readBufferSize,
		WriteBufferSize: m.writeBufferSize,
	}
}

// routeMessage dispatches the message to the proper handler.
func (m *Manager) routeMessage(msg Message, c *Connection) error {
	// Check if Handler is present in Map
	if handler, ok := m.handlers[msg.Type]; ok {
		// Execute the handler and return any err
		if err := handler(msg, c); err != nil {
			return err
		}
		return nil
	} else {
		return ErrMessageNotSupported
	}
}

// GetWSHandler returns an HTTP Handler to serve websocket connections through the Manager.
func (m *Manager) GetWSHandler(ctx context.Context) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		m.logger.Debug().Msg("new connection")
		// Begin by upgrading the HTTP request
		websocketUpgrader := m.getUpgrader()
		conn, err := websocketUpgrader.Upgrade(w, r, nil)
		if err != nil {
			m.logger.Error().Err(err).Msg("can't upgrade connection")
			return
		}

		// Create New Connection
		client := NewConnection(conn, m)
		// Add the newly created client to the manager
		m.AddConnection(client)

		// Start the read / write processes
		// NOTE: the WebSocket connection is only allowed to have one concurrent
		// writer, we can fix this by having an unbuffered channel act as a locker.
		go client.ReadMessages(ctx)
		go client.WriteMessages(ctx)
	}
}

// AddConnection will add a connection to our connection list.
func (m *Manager) AddConnection(conn *Connection) {
	m.Lock()
	defer m.Unlock()
	m.connections[conn] = true
}

// RemoveConnection will remove the connection (closing it).
func (m *Manager) RemoveConnection(conn *Connection) {
	m.Lock()
	defer m.Unlock()

	// Check if Connection exists, then delete it
	if _, ok := m.connections[conn]; ok {
		// close websocket
		if conn.connection != nil {
			conn.connection.Close()
		}
		// remove
		delete(m.connections, conn)
	}
}

// Connections return the (active) connections associated with this Manager.
func (m *Manager) Connections() ConnectionList {
	m.Lock()
	defer m.Unlock()
	connections := make(ConnectionList)
	for conn, active := range m.connections {
		if active {
			connections[conn] = active
		}
	}
	return connections
}
