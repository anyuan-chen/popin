// Package friend manages the friendship graph between Popin users.
//
// State is stored in an append-only events log (friendship_events). The
// current relationship between two users is always derived from the newest
// event of the unordered pair {A, B}; state transitions (request, accept,
// deny, unfriend) are new INSERTs, never UPDATEs. As a consequence denial and
// unfriending never erase history and re-requesting after a denial is simply a
// fresh request that becomes the new latest event.
//
// Latest-action semantics:
//
//	none / deny / unfriend -> not friends (a request is allowed)
//	request (actor = the requester)        -> pending
//	accept                                 -> friends
package friend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// Action is a friendship_event action.
type Action string

const (
	ActionRequest  Action = "request"
	ActionAccept   Action = "accept"
	ActionDeny     Action = "deny"
	ActionUnfriend Action = "unfriend"
)

func IsValidAction(a Action) bool {
	switch a {
	case ActionRequest, ActionAccept, ActionDeny, ActionUnfriend:
		return true
	}
	return false
}

// Errors returned by Service. Use errors.Is to discriminate.
var (
	ErrUnknownUser      = errors.New("unknown user")
	ErrSelf             = errors.New("cannot befriend yourself")
	ErrAlreadyPending   = errors.New("you already have a pending request to this user")
	ErrReversePending   = errors.New("this user already requested you; accept instead")
	ErrAlreadyFriends   = errors.New("already friends")
	ErrNoPendingRequest = errors.New("no pending request from this user")
	ErrNotFriends       = errors.New("not friends with this user")
	ErrInvalidAction    = errors.New("invalid action")
)

// event is a single friendship_events row. Only the latest event of a pair is
// meaningful for current state, but all events are retained for history.
type event struct {
	ID        int64
	ActorID   int64
	SubjectID int64
	Action    Action
	CreatedAt string
}

// User is the minimal user shape returned by List (mirrors auth.User).
type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// List is the result of List(userID).
type List struct {
	Friends  []User `json:"friends"`
	Incoming []User `json:"requests"` // pending requests addressed to userID
}

// Service derives the friendship graph from the append-only events log.
type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// Request creates a pending request from fromID to the user named toUsername.
// If the latest pair event already implies a blocking state, returns the
// appropriate sentinel error and inserts nothing.
func (s *Service) Request(ctx context.Context, fromID int64, toUsername string) error {
	if toUsername == "" {
		return ErrUnknownUser
	}

	to, err := s.lookupByUsername(ctx, toUsername)
	if err != nil {
		return err
	}
	if to.ID == fromID {
		return ErrSelf
	}

	latest, err := s.latestEvent(ctx, fromID, to.ID)
	if err != nil {
		return fmt.Errorf("latest event: %w", err)
	}
	if latest != nil {
		switch latest.Action {
		case ActionRequest:
			if latest.ActorID == fromID {
				return ErrAlreadyPending
			}
			return ErrReversePending
		case ActionAccept:
			return ErrAlreadyFriends
		case ActionDeny, ActionUnfriend:
			// not friends — a new request is allowed
		}
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO friendship_events (actor_id, subject_id, action) VALUES (?, ?, ?)`,
		fromID, to.ID, ActionRequest,
	)
	if err != nil {
		return fmt.Errorf("insert request: %w", err)
	}
	return nil
}

// List returns the user's current accepted friends and pending incoming
// requests, derived from the latest pair event for every partner that has any
// shared history.
func (s *Service) List(ctx context.Context, userID int64) (List, error) {
	out := List{Friends: []User{}, Incoming: []User{}}

	// Collect every partner the user has ever interacted with, plus their
	// username. We UNION both directions because either side may have
	// originated the latest event.
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT u.id, u.username
FROM users u
JOIN friendship_events fe
  ON (fe.actor_id = u.id AND fe.subject_id = ?)
   OR (fe.subject_id = u.id AND fe.actor_id = ?)
WHERE u.id != ?`,
		userID, userID, userID,
	)
	if err != nil {
		return out, fmt.Errorf("list partners: %w", err)
	}
	defer rows.Close()

	type partner struct {
		User
	}
	var partners []partner
	seen := make(map[int64]bool)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username); err != nil {
			return out, fmt.Errorf("scan partner: %w", err)
		}
		if seen[u.ID] {
			continue
		}
		seen[u.ID] = true
		partners = append(partners, partner{User: u})
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("iterate partners: %w", err)
	}

	// For each partner, consult the latest pair event to bucket the
	// relationship. Low-volume, in-memory; this avoids LEAST/GREATEST
	// gymnastics in SQL and keeps the query portable.
	for _, p := range partners {
		latest, err := s.latestEvent(ctx, userID, p.ID)
		if err != nil {
			return out, fmt.Errorf("latest event for partner %d: %w", p.ID, err)
		}
		if latest == nil {
			continue
		}
		switch latest.Action {
		case ActionAccept:
			out.Friends = append(out.Friends, p.User)
		case ActionRequest:
			if latest.ActorID != userID {
				out.Incoming = append(out.Incoming, p.User)
			}
		case ActionDeny, ActionUnfriend:
			// not friends
		}
	}

	sort.Slice(out.Friends, func(i, j int) bool {
		return out.Friends[i].Username < out.Friends[j].Username
	})
	sort.Slice(out.Incoming, func(i, j int) bool {
		return out.Incoming[i].Username < out.Incoming[j].Username
	})
	return out, nil
}

// Accept accepts the pending request from the user named fromUsername that is
// addressed to addresseeID. Only the addressee (userID == addresseeID) may
// accept; the latest pair event must be a request from fromUsername.
func (s *Service) Accept(ctx context.Context, addresseeID int64, fromUsername string) error {
	return s.actOnPending(ctx, addresseeID, fromUsername, ActionAccept)
}

// Deny denies the pending request from the user named fromUsername that is
// addressed to addresseeID. Idempotent: a second accept/deny fails the
// precondition (no pending request) rather than appending a duplicate row.
func (s *Service) Deny(ctx context.Context, addresseeID int64, fromUsername string) error {
	return s.actOnPending(ctx, addresseeID, fromUsername, ActionDeny)
}

// Unfriend removes an accepted friendship between userID and the user named
// targetUsername. Either side may unfriend. The latest pair event must be an
// accept. Idempotent: re-unfriending fails the precondition.
func (s *Service) Unfriend(ctx context.Context, userID int64, targetUsername string) error {
	target, err := s.lookupByUsername(ctx, targetUsername)
	if err != nil {
		return err
	}
	if target.ID == userID {
		return ErrSelf
	}

	latest, err := s.latestEvent(ctx, userID, target.ID)
	if err != nil {
		return fmt.Errorf("latest event: %w", err)
	}
	if latest == nil || latest.Action != ActionAccept {
		return ErrNotFriends
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO friendship_events (actor_id, subject_id, action) VALUES (?, ?, ?)`,
		userID, target.ID, ActionUnfriend,
	)
	if err != nil {
		return fmt.Errorf("insert unfriend: %w", err)
	}
	return nil
}

// AreFriends reports whether the latest event of the pair {aID, bID} is an
// accept. Used by the call-gating check in the server.
func (s *Service) AreFriends(ctx context.Context, aID, bID int64) (bool, error) {
	latest, err := s.latestEvent(ctx, aID, bID)
	if err != nil {
		return false, err
	}
	return latest != nil && latest.Action == ActionAccept, nil
}

// actOnPending handles accept and deny: both require the latest pair event to
// be a request from fromUsername directed at addresseeID, and both append a
// new event. The precondition check makes these operations idempotent (a
// second act-on overrides nothing and appends no duplicate row).
func (s *Service) actOnPending(ctx context.Context, addresseeID int64, fromUsername string, act Action) error {
	if act != ActionAccept && act != ActionDeny {
		return ErrInvalidAction
	}
	from, err := s.lookupByUsername(ctx, fromUsername)
	if err != nil {
		return err
	}
	if from.ID == addresseeID {
		return ErrSelf
	}

	latest, err := s.latestEvent(ctx, addresseeID, from.ID)
	if err != nil {
		return fmt.Errorf("latest event: %w", err)
	}
	if latest == nil || latest.Action != ActionRequest || latest.ActorID != from.ID {
		return ErrNoPendingRequest
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO friendship_events (actor_id, subject_id, action) VALUES (?, ?, ?)`,
		addresseeID, from.ID, act,
	)
	if err != nil {
		return fmt.Errorf("insert %s: %w", act, err)
	}
	return nil
}

// latestEvent returns the newest friendship_events row for the unordered pair
// {aID, bID}, or nil if the pair has no history.
func (s *Service) latestEvent(ctx context.Context, aID, bID int64) (*event, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, actor_id, subject_id, action, created_at
FROM friendship_events
WHERE (actor_id = ? AND subject_id = ?)
   OR (actor_id = ? AND subject_id = ?)
ORDER BY created_at DESC, id DESC
LIMIT 1`,
		aID, bID, bID, aID,
	)
	var e event
	err := row.Scan(&e.ID, &e.ActorID, &e.SubjectID, &e.Action, &e.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan latest event: %w", err)
	}
	return &e, nil
}

// lookupByUsername resolves a single user by username.
func (s *Service) lookupByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnknownUser
		}
		return nil, fmt.Errorf("query user: %w", err)
	}
	return &u, nil
}
