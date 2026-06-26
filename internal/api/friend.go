package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/popin/popin/auth"
	"github.com/popin/popin/friend"
)

// friendResponse is returned by POST /api/friends/{request,accept,deny,unfriend}.
type friendResponse struct {
	Status string `json:"status"`
}

// friendListResponse is returned by GET /api/friends.
type friendListResponse struct {
	Friends  []friend.User `json:"friends"`
	Requests []friend.User `json:"requests"`
}

func (s *Server) handleFriendRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TargetUsername string `json:"target_username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.TargetUsername == "" {
		writeError(w, "target_username is required", http.StatusBadRequest)
		return
	}

	user, _ := auth.UserFromContext(r.Context())
	err := s.friendSvc.Request(r.Context(), user.ID, req.TargetUsername)
	if err != nil {
		writeFriendError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(friendResponse{Status: "requested"})
}

func (s *Server) handleFriendAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.actOnPending(w, r, friend.ActionAccept); err != nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(friendResponse{Status: "accepted"})
}

func (s *Server) handleFriendDeny(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.actOnPending(w, r, friend.ActionDeny); err != nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(friendResponse{Status: "denied"})
}

func (s *Server) handleFriendUnfriend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Username == "" {
		writeError(w, "username is required", http.StatusBadRequest)
		return
	}

	user, _ := auth.UserFromContext(r.Context())
	err := s.friendSvc.Unfriend(r.Context(), user.ID, req.Username)
	if err != nil {
		writeFriendError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(friendResponse{Status: "unfriended"})
}

// actOnPending dispatches accept/deny by reading the target username and
// routing to Service.Accept or Service.Deny. It writes the HTTP response
// itself on error and returns a non-nil error only to let the caller short-
// circuit (it returns nil on success; the caller then writes the success
// body).
func (s *Server) actOnPending(w http.ResponseWriter, r *http.Request, act friend.Action) error {
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", http.StatusBadRequest)
		return err
	}
	if req.Username == "" {
		writeError(w, "username is required", http.StatusBadRequest)
		return errors.New("missing username")
	}

	user, _ := auth.UserFromContext(r.Context())
	var err error
	switch act {
	case friend.ActionAccept:
		err = s.friendSvc.Accept(r.Context(), user.ID, req.Username)
	case friend.ActionDeny:
		err = s.friendSvc.Deny(r.Context(), user.ID, req.Username)
	default:
		writeError(w, "unsupported action", http.StatusBadRequest)
		return errors.New("unsupported action")
	}
	if err != nil {
		writeFriendError(w, err)
		return err
	}
	return nil
}

func (s *Server) handleFriendList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, _ := auth.UserFromContext(r.Context())
	list, err := s.friendSvc.List(r.Context(), user.ID)
	if err != nil {
		log.Printf("friend list for %d: %v", user.ID, err)
		writeError(w, "Failed to list friends", http.StatusInternalServerError)
		return
	}
	if list.Friends == nil {
		list.Friends = []friend.User{}
	}
	if list.Incoming == nil {
		list.Incoming = []friend.User{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(friendListResponse{
		Friends:  list.Friends,
		Requests: list.Incoming,
	})
}

// writeFriendError maps friend.Service sentinel errors to HTTP status codes.
func writeFriendError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, friend.ErrUnknownUser):
		writeError(w, "user not found", http.StatusNotFound)
	case errors.Is(err, friend.ErrSelf):
		writeError(w, "cannot befriend yourself", http.StatusBadRequest)
	case errors.Is(err, friend.ErrAlreadyPending):
		writeError(w, "you already have a pending request to this user", http.StatusConflict)
	case errors.Is(err, friend.ErrReversePending):
		writeError(w, "this user already requested you; accept instead", http.StatusConflict)
	case errors.Is(err, friend.ErrAlreadyFriends):
		writeError(w, "already friends", http.StatusConflict)
	case errors.Is(err, friend.ErrNoPendingRequest):
		writeError(w, "no pending request from this user", http.StatusNotFound)
	case errors.Is(err, friend.ErrNotFriends):
		writeError(w, "not friends with this user", http.StatusNotFound)
	case errors.Is(err, friend.ErrInvalidAction):
		writeError(w, "invalid action", http.StatusBadRequest)
	default:
		log.Printf("friend service error: %v", err)
		writeError(w, "Failed to process friend request", http.StatusInternalServerError)
	}
}
