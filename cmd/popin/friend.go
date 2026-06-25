package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
)

// friendUser is the {id, username} shape returned by GET /api/friends for both
// the accepted friends list and the incoming-requests list.
type friendUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// friendList is the GET /api/friends response.
type friendList struct {
	Friends  []friendUser `json:"friends"`
	Requests []friendUser `json:"requests"`
}

// runFriend implements `popin friend [username]`. With a username argument it
// sends a friend request; with no argument it opens the bubbletea TUI for
// accepting/denying incoming requests and unfriending.
func runFriend(cfg *daemonConfig, args []string) error {
	token, err := loadToken()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("not logged in; run `popin login` first")
		}
		return err
	}

	if len(args) > 0 {
		return sendFriendRequest(cfg, token, args[0])
	}
	return runFriendsTUI(cfg, token)
}

// sendFriendRequest POSTs a friend request to the backend and prints a
// human-readable result line for the common service-error cases. Reuses the
// daemon token via the Authorization header, the same way runDaemon does.
func sendFriendRequest(cfg *daemonConfig, token, target string) error {
	body, _ := json.Marshal(map[string]string{"target_username": target})
	req, _ := http.NewRequest(http.MethodPost, cfg.BackendURL+"/api/friends/request", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("backend: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		fmt.Printf("Friend request sent to %s.\n", target)
		return nil
	case http.StatusConflict:
		// The backend carries a specific message for pending / reverse-pending
		// / already-friends; surface it verbatim.
		msg := decodeErrorMessage(resp.Body)
		if msg == "" {
			msg = "request already exists or you are already friends"
		}
		fmt.Println(msg)
		return nil
	case http.StatusNotFound:
		fmt.Printf("No user named %q.\n", target)
		return nil
	case http.StatusBadRequest:
		return errors.New("cannot befriend yourself")
	case http.StatusUnauthorized:
		return errors.New("phone attendant token invalid or expired (run `popin login` again)")
	default:
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
}

// fetchFriends retrieves the current friends + incoming requests list.
func fetchFriends(cfg *daemonConfig, token string) (*friendList, error) {
	req, _ := http.NewRequest(http.MethodGet, cfg.BackendURL+"/api/friends", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("backend: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, errors.New("daemon token invalid or expired (run `popin login` again)")
		}
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var out friendList
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &out, nil
}

// postFriendAction POSTs to one of /api/friends/{accept,deny,unfriend}. target
// is the other user's username. Returns (status, error) where status is the
// backend's status field on success.
func postFriendAction(cfg *daemonConfig, token, endpoint, target string) (string, error) {
	body, _ := json.Marshal(map[string]string{"username": target})
	req, _ := http.NewRequest(http.MethodPost, cfg.BackendURL+endpoint, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("backend: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var r struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return "", fmt.Errorf("decode: %w", err)
		}
		return r.Status, nil
	case http.StatusNotFound:
		return "", errors.New(decodeErrorMessage(resp.Body))
	case http.StatusUnauthorized:
		return "", errors.New("daemon token invalid or expired (run `popin login` again)")
	default:
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
}

func decodeErrorMessage(r io.Reader) string {
	var m map[string]string
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		return ""
	}
	return m["error"]
}
