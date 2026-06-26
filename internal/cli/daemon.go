package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

const (
	wsReconnectMin = 1 * time.Second
	wsReconnectMax = 30 * time.Second
	wsPingInterval = 30 * time.Second
)

// Listen connects to the server and listens for incoming calls indefinitely,
// opening a browser tab for each. It is the implementation of `popin listen`.
func Listen(c *Config) error {
	token, err := loadToken()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("not logged in; run `popin login` first")
		}
		return err
	}
	username, err := fetchMe(c, token)
	if err != nil {
		return fmt.Errorf("could not verify phone attendant token (is the backend up?): %w", err)
	}
	fmt.Printf("Popin phone attendant online as %s. Waiting for incoming calls.\n", username)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	backoff := wsReconnectMin
	for {
		if ctx.Err() != nil {
			return nil
		}
		replaced, err := connectOnce(ctx, c, token)
		if ctx.Err() != nil {
			return nil
		}
		if replaced {
			fmt.Println("Replaced by another phone attendant session. Exiting.")
			return nil
		}
		if err != nil {
			log.Printf("connection lost: %v", err)
		}
		fmt.Printf("Reconnecting in %s...\n", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > wsReconnectMax {
			backoff = wsReconnectMax
		}
	}
}

// connectOnce opens the daemon websocket, reads messages until the connection
// closes, and processes incoming calls. Returns (replaced, err): replaced is
// true if the server told us we've been superseded by another daemon.
func connectOnce(ctx context.Context, c *Config, token string) (bool, error) {
	u := c.WSURL("/ws/daemon") + "?token=" + url.QueryEscape(token)

	dialer := websocket.DefaultDialer
	conn, resp, err := dialer.DialContext(ctx, u, nil)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusForbidden {
			return false, errors.New("server rejected phone attendant token (run `popin login` again)")
		}
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return false, errors.New("phone attendant token invalid or expired (run `popin login` again)")
		}
		return false, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	pingCtx, pingCancel := context.WithCancel(ctx)
	defer pingCancel()
	go func() {
		t := time.NewTicker(wsPingInterval)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			}
		}
	}()

	// Cancellation (e.g. SIGINT) does not interrupt a blocked
	// conn.ReadMessage() — Gorilla reads honor only the socket's own
	// deadlines. Watch ctx and force-close the connection on cancel so the
	// read unblocks immediately instead of hanging until the server's
	// ~70s read timeout fires.
	go func() {
		<-ctx.Done()
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(2*time.Second),
		)
		_ = conn.Close()
	}()

	var replaced bool
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if replaced {
				return true, nil
			}
			return false, err
		}
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(msg, &env); err != nil {
			continue
		}
		switch env.Type {
		case "incoming_call":
			handleIncomingCall(msg)
		case "replaced":
			replaced = true
		}
	}
}

func handleIncomingCall(msg []byte) {
	var c struct {
		RoomName       string `json:"room_name"`
		CallerUsername string `json:"caller_username"`
		WebRoomURL     string `json:"web_room_url"`
	}
	if err := json.Unmarshal(msg, &c); err != nil {
		log.Printf("malformed incoming_call: %v", err)
		return
	}
	fmt.Printf("\nIncoming call from %s | room %s\n  Opening: %s\n", c.CallerUsername, c.RoomName, c.WebRoomURL)
	if err := openBrowser(c.WebRoomURL); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open browser: %v\n  Open manually: %s\n", err, c.WebRoomURL)
	}
}
