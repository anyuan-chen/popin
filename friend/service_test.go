package friend

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/popin/popin/db"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// registerUser inserts a user row directly so the friend package tests don't
// depend on bcrypt or the auth package. Mirrors the users table shape.
func registerUser(t *testing.T, d *sql.DB, username string) int64 {
	t.Helper()
	res, err := d.Exec(
		`INSERT INTO users (username, password_hash) VALUES (?, ?)`,
		username, "x",
	)
	if err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	return NewService(setupTestDB(t))
}

func TestRequest_Success(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("Request: %v", err)
	}

	latest, err := svc.latestEvent(context.Background(), alice, bob)
	if err != nil {
		t.Fatalf("latestEvent: %v", err)
	}
	if latest == nil {
		t.Fatal("expected an event after Request")
	}
	if latest.Action != ActionRequest {
		t.Errorf("action = %q, want %q", latest.Action, ActionRequest)
	}
	if latest.ActorID != alice || latest.SubjectID != bob {
		t.Errorf("actor=%d subject=%d, want actor=%d subject=%d",
			latest.ActorID, latest.SubjectID, alice, bob)
	}
}

func TestRequest_UnknownUser(t *testing.T) {
	svc := newTestService(t)
	alice := registerUser(t, svc.db, "alice")

	err := svc.Request(context.Background(), alice, "ghost")
	if !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("error = %v, want ErrUnknownUser", err)
	}
}

func TestRequest_Self(t *testing.T) {
	svc := newTestService(t)
	alice := registerUser(t, svc.db, "alice")

	err := svc.Request(context.Background(), alice, "alice")
	if !errors.Is(err, ErrSelf) {
		t.Fatalf("error = %v, want ErrSelf", err)
	}
}

func TestRequest_AlreadyPending(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	registerUser(t, db, "bob")

	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("first Request: %v", err)
	}
	err := svc.Request(context.Background(), alice, "bob")
	if !errors.Is(err, ErrAlreadyPending) {
		t.Fatalf("second Request error = %v, want ErrAlreadyPending", err)
	}
}

func TestRequest_ReversePending(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("alice Request: %v", err)
	}
	err := svc.Request(context.Background(), bob, "alice")
	if !errors.Is(err, ErrReversePending) {
		t.Fatalf("bob Request error = %v, want ErrReversePending", err)
	}
}

func TestRequest_DenyThenReRequestAllowed(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := svc.Deny(context.Background(), bob, "alice"); err != nil {
		t.Fatalf("Deny: %v", err)
	}

	// After a deny, a fresh request from alice is allowed (denial is not
	// permanent: it simply was the latest event).
	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("re-Request after deny: %v", err)
	}

	latest, err := svc.latestEvent(context.Background(), alice, bob)
	if err != nil {
		t.Fatalf("latestEvent: %v", err)
	}
	if latest == nil || latest.Action != ActionRequest || latest.ActorID != alice {
		t.Fatalf("latest = %+v, want a fresh request from alice", latest)
	}
}

func TestRequest_UnfriendThenReRequestAllowed(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := svc.Accept(context.Background(), bob, "alice"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := svc.Unfriend(context.Background(), bob, "alice"); err != nil {
		t.Fatalf("Unfriend: %v", err)
	}
	// After unfriend, a fresh request from alice is allowed.
	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("re-Request after unfriend: %v", err)
	}
}

func TestAccept_Success(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := svc.Accept(context.Background(), bob, "alice"); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	latest, err := svc.latestEvent(context.Background(), alice, bob)
	if err != nil {
		t.Fatalf("latestEvent: %v", err)
	}
	if latest == nil || latest.Action != ActionAccept {
		t.Fatalf("latest = %+v, want accept", latest)
	}
	if latest.ActorID != bob || latest.SubjectID != alice {
		t.Errorf("actor=%d subject=%d, want actor=%d (bob) subject=%d (alice)",
			latest.ActorID, latest.SubjectID, bob, alice)
	}

	ok, err := svc.AreFriends(context.Background(), alice, bob)
	if err != nil {
		t.Fatalf("AreFriends: %v", err)
	}
	if !ok {
		t.Fatal("AreFriends = false, want true after accept")
	}
}

func TestAccept_NoPending(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	err := svc.Accept(context.Background(), bob, "alice")
	if !errors.Is(err, ErrNoPendingRequest) {
		t.Fatalf("error = %v, want ErrNoPendingRequest", err)
	}
}

func TestAccept_OnlyAddresseeMayAct(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	// Alice (the requester) tries to accept their own outgoing request: the
	// latest event is a request from alice, so precondition (request from
	// "alice" addressed at addresseeID=alice) sees ActorID == addresseeID? No:
	// addresseeID=alice, latest.ActorID=alice, so it looks like a request
	// FROM the addressee to the requester -> not a pending request TO alice.
	// Either way, it must fail ErrNoPendingRequest (or ErrSelf if they're the
	// same). Here addresseeID=alice, from="alice" -> ErrSelf.
	err := svc.Accept(context.Background(), alice, "alice")
	if !errors.Is(err, ErrSelf) {
		t.Fatalf("error = %v, want ErrSelf", err)
	}
	_ = bob
}

func TestAccept_Idempotent(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := svc.Accept(context.Background(), bob, "alice"); err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	// Second accept: latest is now an accept, not a request -> fail precondition.
	err := svc.Accept(context.Background(), bob, "alice")
	if !errors.Is(err, ErrNoPendingRequest) {
		t.Fatalf("second Accept error = %v, want ErrNoPendingRequest", err)
	}
}

func TestDeny_Success(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := svc.Deny(context.Background(), bob, "alice"); err != nil {
		t.Fatalf("Deny: %v", err)
	}

	ok, err := svc.AreFriends(context.Background(), alice, bob)
	if err != nil {
		t.Fatalf("AreFriends: %v", err)
	}
	if ok {
		t.Fatal("AreFriends = true, want false after deny")
	}

	latest, err := svc.latestEvent(context.Background(), alice, bob)
	if err != nil {
		t.Fatalf("latestEvent: %v", err)
	}
	if latest == nil || latest.Action != ActionDeny {
		t.Fatalf("latest = %+v, want deny", latest)
	}
}

func TestUnfriend_Success(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := svc.Accept(context.Background(), bob, "alice"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := svc.Unfriend(context.Background(), bob, "alice"); err != nil {
		t.Fatalf("Unfriend: %v", err)
	}

	ok, err := svc.AreFriends(context.Background(), alice, bob)
	if err != nil {
		t.Fatalf("AreFriends: %v", err)
	}
	if ok {
		t.Fatal("AreFriends = true, want false after unfriend")
	}
}

func TestUnfriend_NotFriends(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	// No history: not friends.
	err := svc.Unfriend(context.Background(), bob, "alice")
	if !errors.Is(err, ErrNotFriends) {
		t.Fatalf("error = %v, want ErrNotFriends", err)
	}

	// Pending request only: still not friends.
	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	err = svc.Unfriend(context.Background(), bob, "alice")
	if !errors.Is(err, ErrNotFriends) {
		t.Fatalf("pending-state Unfriend error = %v, want ErrNotFriends", err)
	}
}

func TestUnfriend_Idempotent(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	if err := svc.Request(context.Background(), alice, "bob"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := svc.Accept(context.Background(), bob, "alice"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := svc.Unfriend(context.Background(), bob, "alice"); err != nil {
		t.Fatalf("first Unfriend: %v", err)
	}
	err := svc.Unfriend(context.Background(), bob, "alice")
	if !errors.Is(err, ErrNotFriends) {
		t.Fatalf("second Unfriend error = %v, want ErrNotFriends", err)
	}
}

func TestList_Empty(t *testing.T) {
	svc := newTestService(t)
	alice := registerUser(t, svc.db, "alice")

	out, err := svc.List(context.Background(), alice)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out.Friends) != 0 || len(out.Incoming) != 0 {
		t.Errorf("List = %+v, want empty", out)
	}
}

func TestList_FriendsAndIncoming(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")
	carol := registerUser(t, db, "carol")
	registerUser(t, db, "dan")

	// alice <-> bob: accepted friends.
	mustRequest(t, svc, alice, "bob")
	mustAccept(t, svc, bob, "alice")

	// carol -> alice: pending; should appear in alice's incoming.
	mustRequest(t, svc, carol, "alice")

	// alice -> dan: pending outgoing; should NOT appear in alice's incoming,
	// and neither is a friend yet.
	mustRequest(t, svc, alice, "dan")

	out, err := svc.List(context.Background(), alice)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out.Friends) != 1 || out.Friends[0].Username != "bob" {
		t.Errorf("Friends = %+v, want [bob]", out.Friends)
	}
	if len(out.Incoming) != 1 || out.Incoming[0].Username != "carol" {
		t.Errorf("Incoming = %+v, want [carol]", out.Incoming)
	}
}

func TestList_FriendsSortedByUsername(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	zoe := registerUser(t, db, "zoe")
	mallory := registerUser(t, db, "mallory")

	mustRequest(t, svc, alice, "zoe")
	mustAccept(t, svc, zoe, "alice")
	mustRequest(t, svc, alice, "mallory")
	mustAccept(t, svc, mallory, "alice")

	out, err := svc.List(context.Background(), alice)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out.Friends) != 2 {
		t.Fatalf("Friends = %+v, want 2 entries", out.Friends)
	}
	if out.Friends[0].Username != "mallory" || out.Friends[1].Username != "zoe" {
		t.Errorf("Friends order = [%s,%s], want [mallory,zoe]",
			out.Friends[0].Username, out.Friends[1].Username)
	}
}

func TestList_AfterUnfriendDropsFromFriends(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	mustRequest(t, svc, alice, "bob")
	mustAccept(t, svc, bob, "alice")
	mustUnfriend(t, svc, bob, "alice")

	out, err := svc.List(context.Background(), alice)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out.Friends) != 0 {
		t.Errorf("Friends = %+v, want empty after unfriend", out.Friends)
	}
	if len(out.Incoming) != 0 {
		t.Errorf("Incoming = %+v, want empty after unfriend", out.Incoming)
	}
}

func TestAreFriends_OrderIndependent(t *testing.T) {
	svc := newTestService(t)
	db := svc.db
	alice := registerUser(t, db, "alice")
	bob := registerUser(t, db, "bob")

	mustRequest(t, svc, alice, "bob")
	mustAccept(t, svc, bob, "alice")

	ab, err := svc.AreFriends(context.Background(), alice, bob)
	if err != nil {
		t.Fatalf("AreFriends(a,b): %v", err)
	}
	ba, err := svc.AreFriends(context.Background(), bob, alice)
	if err != nil {
		t.Fatalf("AreFriends(b,a): %v", err)
	}
	if !ab || !ba {
		t.Errorf("AreFriends(a,b)=%v AreFriends(b,a)=%v, want both true", ab, ba)
	}
}

// --- helpers ---

func mustRequest(t *testing.T, svc *Service, from int64, to string) {
	t.Helper()
	if err := svc.Request(context.Background(), from, to); err != nil {
		t.Fatalf("Request(%d -> %s): %v", from, to, err)
	}
}
func mustAccept(t *testing.T, svc *Service, addressee int64, from string) {
	t.Helper()
	if err := svc.Accept(context.Background(), addressee, from); err != nil {
		t.Fatalf("Accept(%d <- %s): %v", addressee, from, err)
	}
}
func mustUnfriend(t *testing.T, svc *Service, who int64, target string) {
	t.Helper()
	if err := svc.Unfriend(context.Background(), who, target); err != nil {
		t.Fatalf("Unfriend(%d -> %s): %v", who, target, err)
	}
}
