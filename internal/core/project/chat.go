package project

import "github.com/sorokin-vladimir/tele/internal/domain"

// ChatContents is everything a chat subscription currently shows: the message
// window plus the header and per-chat state the pane renders around it.
type ChatContents struct {
	ChatID int64
	Title  string
	IsUser bool

	ThreadRootID int
	// IsGroup covers groups and channels: the message list shows sender names
	// there and not in a 1:1 chat.
	IsGroup         bool
	Online          bool
	Messages        []domain.Message
	AnchorMsgID     int
	HasOlder        bool
	HasNewer        bool
	ReadInboxMaxID  int
	ReadOutboxMaxID int
	// UnreadReactions is the chat's unread-reaction count. It is per-chat state,
	// not per-message: a reaction can land on a message far outside the window,
	// and the client still has to mark it read while the user is looking.
	UnreadReactions int
	Draft           string
	// Outbox is this chat's queued sends, oldest first. It is a separate list
	// rather than synthetic messages because an entry has no message ID and
	// carries what a message does not: attempts, an error kind, a retry time.
	// A pending send is by definition newer than the window, so a client
	// appends rather than merges (#193).
	Outbox []domain.OutboxEntry
}

// BuildChat resolves the window's anchor against the stored history and slices
// out Before..After around it.
//
// HasOlder and HasNewer report what the store holds outside the window; they
// drive the client's scroll affordance and say nothing about Telegram. It never
// fetches: a window that comes back shorter than it asked for is how the core
// learns the store fell short (see Owner.needsBackfill).
func BuildChat(r Reader, w ChatWindow) ChatContents {
	out := ChatContents{ChatID: w.ChatID, ThreadRootID: w.ThreadRootID}
	chat, ok := r.GetChat(w.ChatID)
	if ok {
		out.Title = chat.Title
		out.IsUser = chat.Peer.IsUser()
		out.IsGroup = chat.Peer.IsGroup() || chat.Peer.IsChannel()
		out.Online = chat.Online
		out.ReadInboxMaxID = chat.ReadInboxMaxID
		out.ReadOutboxMaxID = chat.ReadOutboxMaxID
		out.UnreadReactions = chat.UnreadReactionsCount
		out.Draft = chat.Draft
	}
	if w.ThreadRootID != 0 {
		out.Title = w.ThreadTitle
		out.IsUser = false
		out.IsGroup = true
		out.ReadInboxMaxID = w.ThreadReadInboxMaxID
		out.ReadOutboxMaxID = w.ThreadReadOutboxMaxID
		out.Draft = ""
	}
	// Read before the empty-history return below: a chat with nothing stored can
	// still hold a queued send, and that is the only thing it has to show.
	out.Outbox = filterOutbox(r.Outbox(w.ChatID), w.ThreadRootID)

	all := filterThread(r.Messages(w.ChatID), w.ThreadRootID)
	if len(all) == 0 {
		return out
	}

	idx, anchorID := resolveAnchor(all, chat, w.Anchor)
	out.AnchorMsgID = anchorID
	if idx < 0 {
		// The anchor names a message the store does not hold, so the window
		// cannot be sliced at all. HasOlder stays false: it reports what the
		// store holds outside the window, and the store holds nothing usable
		// here. The shortfall is what tells the core to fetch.
		return out
	}

	start := idx - w.Before
	if start < 0 {
		start = 0
	}
	end := idx + w.After + 1
	if w.Anchor.Kind == AnchorFirstUnread {
		// Anchored on the first unread, the window runs to the newest message:
		// everything past the anchor is unread by definition, and a window that
		// stopped at the separator would hide exactly what it was opened for.
		end = len(all)
	}
	if end > len(all) {
		end = len(all)
	}
	out.Messages = all[start:end]
	out.HasOlder = start > 0
	out.HasNewer = end < len(all)
	return out
}

func filterThread(msgs []domain.Message, rootID int) []domain.Message {
	if rootID == 0 {
		return msgs
	}
	out := make([]domain.Message, 0, len(msgs))
	for _, msg := range msgs {
		if msg.ID == rootID || msg.ThreadRootID == rootID || msg.ReplyToMsgID == rootID {
			out = append(out, msg)
		}
	}
	return out
}

func filterOutbox(entries []domain.OutboxEntry, rootID int) []domain.OutboxEntry {
	out := make([]domain.OutboxEntry, 0, len(entries))
	for _, entry := range entries {
		entryRoot := 0
		if entry.Message != nil {
			entryRoot = entry.Message.ThreadRootID
		} else if entry.Media != nil {
			entryRoot = entry.Media.ThreadRootID
		}
		if entryRoot == rootID {
			out = append(out, entry)
		}
	}
	return out
}

// resolveAnchor returns the index of the anchor message in all, oldest first,
// and its id. An index of -1 means the anchor names a message the store does not
// hold.
func resolveAnchor(all []domain.Message, chat domain.Chat, a Anchor) (int, int) {
	switch a.Kind {
	case AnchorMessage:
		for i, m := range all {
			if m.ID == a.MsgID {
				return i, m.ID
			}
		}
		return -1, a.MsgID

	case AnchorFirstUnread:
		// Once pinned by the registry the window stops following the read
		// pointer. A pin that no longer names a stored message (deleted, or
		// trimmed by the store cap) resolves afresh rather than emptying.
		if a.MsgID != 0 {
			for i, m := range all {
				if m.ID == a.MsgID {
					return i, m.ID
				}
			}
		}
		if chat.UnreadCount > 0 {
			storedUnread := 0
			firstUnread := -1
			for i, m := range all {
				if !m.IsOut && m.ID > chat.ReadInboxMaxID {
					storedUnread++
					if firstUnread < 0 {
						firstUnread = i
					}
				}
			}
			if storedUnread < chat.UnreadCount {
				// Show cached history without pinning while the owner fills the gap.
				if firstUnread >= 0 {
					return firstUnread, 0
				}
				return len(all) - 1, 0
			}
			if firstUnread >= 0 {
				return firstUnread, all[firstUnread].ID
			}
		}
		// No unread: the anchor is the newest message.
		fallthrough

	default: // AnchorNewest
		last := len(all) - 1
		return last, all[last].ID
	}
}
