package tg

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestUsersFromContactsFound_UsersOnlyOrderedDeduped(t *testing.T) {
	found := &tg.ContactsFound{
		// MyResults come first (exact/contacts), then global Results.
		MyResults: []tg.PeerClass{&tg.PeerUser{UserID: 10}},
		Results: []tg.PeerClass{
			&tg.PeerUser{UserID: 20},
			&tg.PeerChannel{ChannelID: 99}, // not a user → skipped
			&tg.PeerUser{UserID: 10},       // dup of MyResults → skipped
		},
		Users: []tg.UserClass{
			&tg.User{ID: 10, AccessHash: 111, FirstName: "Ann"},
			&tg.User{ID: 20, AccessHash: 222, FirstName: "Bob"},
			&tg.User{ID: 30, AccessHash: 333, Self: true}, // self → skipped
		},
	}

	got := usersFromContactsFound(found, 10)

	if len(got) != 2 {
		t.Fatalf("want 2 users, got %d: %+v", len(got), got)
	}
	if got[0].ID != 10 || got[1].ID != 20 {
		t.Fatalf("want order [10,20], got [%d,%d]", got[0].ID, got[1].ID)
	}
	if got[0].Peer.AccessHash != 111 || !got[0].Peer.IsUser() {
		t.Fatalf("peer not mapped correctly: %+v", got[0].Peer)
	}
}

func TestUsersFromContactsFound_RespectsLimit(t *testing.T) {
	found := &tg.ContactsFound{
		Results: []tg.PeerClass{
			&tg.PeerUser{UserID: 1}, &tg.PeerUser{UserID: 2}, &tg.PeerUser{UserID: 3},
		},
		Users: []tg.UserClass{
			&tg.User{ID: 1}, &tg.User{ID: 2}, &tg.User{ID: 3},
		},
	}
	got := usersFromContactsFound(found, 2)
	if len(got) != 2 {
		t.Fatalf("want 2 (limit), got %d", len(got))
	}
}

func TestResolvedChat_MapsSupergroupAccessHash(t *testing.T) {
	chat, ok := resolvedChat(
		&tg.PeerChannel{ChannelID: 99},
		nil,
		[]tg.ChatClass{&tg.Channel{ID: 99, Title: "Group", AccessHash: 77, Megagroup: true}},
	)
	if !ok {
		t.Fatal("resolvedChat returned ok=false")
	}
	if chat.ID != 99 || chat.Title != "Group" || !chat.Peer.IsSuperGroup() || chat.Peer.AccessHash != 77 {
		t.Fatalf("unexpected chat: %+v", chat)
	}
}
