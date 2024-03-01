//go:build example

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"

	"github.com/panta/ws"
	"github.com/panta/ws/example/chat"
)

func main() {
	flag.Parse()

	logConsoleWriter := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}
	logger := zerolog.New(logConsoleWriter).
		Level(zerolog.DebugLevel).
		With().
		Timestamp().
		Logger()

	args := flag.Args()
	var cmd string
	if len(args) == 0 {
		// no sub-command specified, use a default
		cmd = "serve"
	} else {
		cmd, args = args[0], args[1:]
	}

	logger.Debug().Str("cmd", cmd).Send()

	switch cmd {
	case "server":
		fallthrough
	case "serve":
		if err := cmdServe(logger, args); err != nil {
			logger.Fatal().Err(err).Msgf("command '%s' failed", cmd)
			os.Exit(1)
		}
	case "cli":
		fallthrough
	case "client":
		if err := cmdClient(logger, args); err != nil {
			logger.Fatal().Err(err).Msgf("command '%s' failed", cmd)
			os.Exit(1)
		}
	default:
		logger.Fatal().Msgf("Unrecognized command %q. "+
			"Command must be one of: serve, client", cmd)
	}

}

func cmdServe(logger zerolog.Logger, args []string) error {
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, os.Interrupt)

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup

	// launch goroutine to handle signals
	wg.Add(1)
	go func() {
		defer wg.Done()

		_ = <-sigC
		logger.Debug().Msg("got signal")
		cancel()
	}()

	router := setupAPI(ctx, logger)
	addr := ":8080"
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		logger.Info().Msgf("starting web server on %s...", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msgf("server error")
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		// Wait for the parent context to be done before shutting down.
		<-ctx.Done()

		logger.Info().Msg("shutting down web server")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Fatal().Err(err).Msg("failed to shut down web server")
		}
		logger.Info().Msg("web server exited properly")
	}()

	wg.Wait()
	logger.Info().Msg("exiting...")

	return nil
}

// setupAPI will start all Routes and their Handlers
func setupAPI(ctx context.Context, logger zerolog.Logger) *http.ServeMux {
	setupChatHandlers := func(m *ws.Manager) {
		m.AddHandler(chat.EventSendMessage, chat.SendMessageHandler).
			AddHandler(chat.EventChangeRoom, chat.ChatRoomHandler)
	}
	manager := ws.NewManager(ws.ManagerRoleServer,
		ws.ManagerLogger(logger),
		ws.ManagerBuffers(1024, 1024),
		setupChatHandlers,
	)

	router := http.NewServeMux()
	router.Handle("/", http.FileServer(http.Dir("./frontend")))
	router.HandleFunc("/ws", manager.GetWSHandler(ctx))
	return router
}

func cmdClient(logger zerolog.Logger, args []string) error {
	flag := flag.NewFlagSet("ws client", flag.ExitOnError) // shadow the global flag package - on purpose
	var (
		wsUrl = flag.String("url", "ws://127.0.0.1:8080/ws", "specify the URL of the server WebSocket endpoint")
	)
	flag.Parse(args)
	args = flag.Args()

	chatSendMessageHandler := func(msg ws.Message, c *ws.Connection) error {
		var chatMsg chat.SendMessageEvent
		if err := json.Unmarshal(msg.Payload, &chatMsg); err != nil {
			logger.Error().Err(err).Msg("bad payload")
			return fmt.Errorf("bad payload: %w", err)
		} else {
			logger.Info().Msgf("{send} %s: %s", chatMsg.From, chatMsg.Message)
		}
		return nil
	}
	chatNewMessageHandler := func(msg ws.Message, c *ws.Connection) error {
		var chatMsg chat.NewMessageEvent
		if err := json.Unmarshal(msg.Payload, &chatMsg); err != nil {
			logger.Error().Err(err).Msg("bad payload")
			return fmt.Errorf("bad payload: %w", err)
		} else {
			logger.Info().Msgf("{new} %s: %s", chatMsg.From, chatMsg.Message)
		}
		return nil
	}
	chatChangeRoomHandler := func(msg ws.Message, c *ws.Connection) error {
		var chatMsg chat.ChangeRoomEvent
		if err := json.Unmarshal(msg.Payload, &chatMsg); err != nil {
			logger.Error().Err(err).Msg("bad payload")
			return fmt.Errorf("bad payload: %w", err)
		} else {
			logger.Info().Msgf("{change} -> %s", chatMsg.Name)
		}
		return nil
	}

	setupChatHandlers := func(m *ws.Manager) {
		m.AddHandler(chat.EventSendMessage, chatSendMessageHandler).
			AddHandler(chat.EventNewMessage, chatNewMessageHandler).
			AddHandler(chat.EventChangeRoom, chatChangeRoomHandler)
	}
	manager := ws.NewManager(ws.ManagerRoleClient,
		ws.ManagerLogger(logger),
		ws.ManagerBuffers(1024, 1024),
		setupChatHandlers,
	)

	u, err := url.Parse(*wsUrl)
	if err != nil {
		return fmt.Errorf("can't parse websocket endpoint URL: %w", err)
	}
	logger.Debug().Msgf("connecting to '%s'...", u.String())
	wsDialer := *websocket.DefaultDialer
	c, _, err := wsDialer.Dial(u.String(), http.Header{"Origin": []string{"http://127.0.0.1:8080"}})
	if err != nil {
		return fmt.Errorf("can't dial ws endpoint: %w", err)
	}
	// will be closed by ws.Client

	// wsClient := ws.ClientNewConnection(logger, c)
	wsClient := ws.NewConnection(c, manager)
	defer wsClient.Close()

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, os.Interrupt)

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup

	// launch goroutine to handle signals
	wg.Add(1)
	go func() {
		defer wg.Done()

		_ = <-sigC
		cancel()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		wsClient.ReadMessages(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		wsClient.WriteMessages(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				msg := chat.SendMessageEvent{
					Message: fmt.Sprintf("ticker %s", t.String()),
					From:    "cli",
				}
				data, _ := json.Marshal(&msg)

				outgoing := ws.Message{
					Type:    chat.EventSendMessage,
					Payload: data,
				}
				wsClient.SendMessage(outgoing)
			}
		}
	}()

	wg.Wait()
	logger.Info().Msg("exiting...")

	return nil
}
