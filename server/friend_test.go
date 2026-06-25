package server

import (
	"net/http"
	"testing"
)

func TestFriend_Request_RequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := doJSON(t, &http.Client{}, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "alice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestFriend_Request_Success(t *testing.T) {
	ts, _ := newTestServer(t)
	alice, _ := registerAndLogin(t, ts, "alice", "password123", "browser")
	registerAndLogin(t, ts, "bob", "password123", "browser")

	resp := doJSON(t, alice, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "bob",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["status"] != "requested" {
		t.Errorf("status = %v, want requested", body["status"])
	}
}

func TestFriend_Request_UnknownUser(t *testing.T) {
	ts, _ := newTestServer(t)
	alice, _ := registerAndLogin(t, ts, "alice", "password123", "browser")

	resp := doJSON(t, alice, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "ghost",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestFriend_Request_Self(t *testing.T) {
	ts, _ := newTestServer(t)
	alice, _ := registerAndLogin(t, ts, "alice", "password123", "browser")

	resp := doJSON(t, alice, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "alice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestFriend_Request_AlreadyPending(t *testing.T) {
	ts, _ := newTestServer(t)
	alice, _ := registerAndLogin(t, ts, "alice", "password123", "browser")
	registerAndLogin(t, ts, "bob", "password123", "browser")

	if resp := doJSON(t, alice, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "bob",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first request: status = %d, want 201", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	resp := doJSON(t, alice, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "bob",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

func TestFriend_Request_ReversePending(t *testing.T) {
	ts, _ := newTestServer(t)
	alice, _ := registerAndLogin(t, ts, "alice", "password123", "browser")
	bob, _ := registerAndLogin(t, ts, "bob", "password123", "browser")

	if resp := doJSON(t, alice, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "bob",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("alice request: status = %d, want 201", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	resp := doJSON(t, bob, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "alice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

func TestFriend_List_ShowsIncomingOnly(t *testing.T) {
	ts, _ := newTestServer(t)
	alice, _ := registerAndLogin(t, ts, "alice", "password123", "browser")
	registerAndLogin(t, ts, "bob", "password123", "browser")
	carol, _ := registerAndLogin(t, ts, "carol", "password123", "browser")

	// alice -> bob pending; carol -> alice pending (incoming for alice).
	if resp := doJSON(t, alice, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "bob",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("alice request bob: status = %d, want 201", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := doJSON(t, carol, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "alice",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("carol request alice: status = %d, want 201", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	resp := doJSON(t, alice, http.MethodGet, ts.URL+"/api/friends", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, want 200", resp.StatusCode)
	}
	body := decodeBody(t, resp)

	friends, _ := body["friends"].([]any)
	if len(friends) != 0 {
		t.Errorf("friends = %v, want empty (only pending requests so far)", friends)
	}
	requests, _ := body["requests"].([]any)
	if len(requests) != 1 {
		t.Fatalf("requests = %v, want 1 entry (carol)", requests)
	}
	r0, _ := requests[0].(map[string]any)
	if r0["username"] != "carol" {
		t.Errorf("requests[0].username = %v, want carol", r0["username"])
	}
}

func TestFriend_Accept_Success(t *testing.T) {
	ts, _ := newTestServer(t)
	alice, _ := registerAndLogin(t, ts, "alice", "password123", "browser")
	bob, _ := registerAndLogin(t, ts, "bob", "password123", "browser")

	doJSON(t, alice, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "bob",
	}).Body.Close()

	resp := doJSON(t, bob, http.MethodPost, ts.URL+"/api/friends/accept", map[string]string{
		"username": "alice",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["status"] != "accepted" {
		t.Errorf("status = %v, want accepted", body["status"])
	}

	// Alice's friend list should now include bob.
	list := doJSON(t, alice, http.MethodGet, ts.URL+"/api/friends", nil)
	defer list.Body.Close()
	listBody := decodeBody(t, list)
	friends, _ := listBody["friends"].([]any)
	if len(friends) != 1 {
		t.Fatalf("friends = %v, want 1 entry", friends)
	}
	f0, _ := friends[0].(map[string]any)
	if f0["username"] != "bob" {
		t.Errorf("friends[0].username = %v, want bob", f0["username"])
	}
}

func TestFriend_Accept_NoPending(t *testing.T) {
	ts, _ := newTestServer(t)
	registerAndLogin(t, ts, "alice", "password123", "browser")
	bob, _ := registerAndLogin(t, ts, "bob", "password123", "browser")

	resp := doJSON(t, bob, http.MethodPost, ts.URL+"/api/friends/accept", map[string]string{
		"username": "alice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestFriend_Deny_Success(t *testing.T) {
	ts, _ := newTestServer(t)
	alice, _ := registerAndLogin(t, ts, "alice", "password123", "browser")
	bob, _ := registerAndLogin(t, ts, "bob", "password123", "browser")

	doJSON(t, alice, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "bob",
	}).Body.Close()

	resp := doJSON(t, bob, http.MethodPost, ts.URL+"/api/friends/deny", map[string]string{
		"username": "alice",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["status"] != "denied" {
		t.Errorf("status = %v, want denied", body["status"])
	}

	// Alice's friend list should be empty after deny; incoming also empty.
	list := doJSON(t, alice, http.MethodGet, ts.URL+"/api/friends", nil)
	defer list.Body.Close()
	listBody := decodeBody(t, list)
	friends, _ := listBody["friends"].([]any)
	if len(friends) != 0 {
		t.Errorf("friends = %v, want empty after deny", friends)
	}
}

func TestFriend_Unfriend_Success(t *testing.T) {
	ts, _ := newTestServer(t)
	alice, _ := registerAndLogin(t, ts, "alice", "password123", "browser")
	bob, _ := registerAndLogin(t, ts, "bob", "password123", "browser")

	doJSON(t, alice, http.MethodPost, ts.URL+"/api/friends/request", map[string]string{
		"target_username": "bob",
	}).Body.Close()
	doJSON(t, bob, http.MethodPost, ts.URL+"/api/friends/accept", map[string]string{
		"username": "alice",
	}).Body.Close()

	resp := doJSON(t, bob, http.MethodPost, ts.URL+"/api/friends/unfriend", map[string]string{
		"username": "alice",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["status"] != "unfriended" {
		t.Errorf("status = %v, want unfriended", body["status"])
	}

	list := doJSON(t, bob, http.MethodGet, ts.URL+"/api/friends", nil)
	defer list.Body.Close()
	listBody := decodeBody(t, list)
	friends, _ := listBody["friends"].([]any)
	if len(friends) != 0 {
		t.Errorf("friends = %v, want empty after unfriend", friends)
	}
}

func TestFriend_Unfriend_NotFriends(t *testing.T) {
	ts, _ := newTestServer(t)
	registerAndLogin(t, ts, "alice", "password123", "browser")
	bob, _ := registerAndLogin(t, ts, "bob", "password123", "browser")

	resp := doJSON(t, bob, http.MethodPost, ts.URL+"/api/friends/unfriend", map[string]string{
		"username": "alice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestCall_BlockedBetweenNonFriends(t *testing.T) {
	ts, _ := newTestServer(t)
	caller, _ := registerAndLogin(t, ts, "bob", "password123", "browser")
	registerAndLogin(t, ts, "alice", "password123", "browser")

	resp := doJSON(t, caller, http.MethodPost, ts.URL+"/api/call", map[string]string{
		"target_username": "alice",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (not friends)", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["error"] != "not friends" {
		t.Errorf("error = %v, want %q", body["error"], "not friends")
	}
}

func TestCall_UnknownTarget(t *testing.T) {
	ts, _ := newTestServer(t)
	caller, _ := registerAndLogin(t, ts, "bob", "password123", "browser")

	resp := doJSON(t, caller, http.MethodPost, ts.URL+"/api/call", map[string]string{
		"target_username": "ghost",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (unknown target)", resp.StatusCode)
	}
}
