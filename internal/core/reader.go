package core

import (
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
)

// projectionReader is what the projection registry reads from. The store
// supplies chats, messages and folders; the owner's queue supplies pending
// sends.
//
// The two have different owners on purpose: the queue is not part of the
// account cache, so store.Store does not grow outbox methods and MemoryStore is
// not asked to implement queue semantics (#193).
//
// It holds the owner rather than the queue because the queue is set after the
// registry is built, and rebuilding the registry would drop live subscriptions.
type projectionReader struct {
	store.Store
	owner *Owner
}

// GetChat overlays peers resolved from links and cross-chat replies onto the
// persisted dialogs. Chats() deliberately still comes from Store, so these
// transient peers never acquire a chat-list row.
func (r projectionReader) GetChat(chatID int64) (domain.Chat, bool) {
	if chat, ok := r.Store.GetChat(chatID); ok {
		counts := r.ImportantUnread(chatID, 0)
		if counts.Revision != 0 && !counts.Loading && !counts.Failed {
			chat.UnreadMentionsCount, chat.UnreadReactionsCount = counts.Mentions, counts.Reactions
		}
		return chat, true
	}
	if r.owner != nil {
		return r.owner.transientChat(chatID)
	}
	return domain.Chat{}, false
}

func (r projectionReader) ImportantUnread(chatID int64, rootID int) domain.ImportantUnread {
	if r.owner == nil {
		return domain.ImportantUnread{}
	}
	return r.owner.ImportantUnread(chatID, rootID)
}

func (r projectionReader) Chats() []domain.Chat {
	chats := r.Store.Chats()
	for i := range chats {
		if chat, ok := r.GetChat(chats[i].ID); ok {
			chats[i] = chat
		}
	}
	return chats
}

// reader is the owner's own projection reader, for the places that build a
// projection outside the registry.
func (o *Owner) reader() projectionReader {
	return projectionReader{Store: o.state.Store(), owner: o}
}

// Outbox returns a chat's queued sends, or nothing while no queue is set —
// the case for an owner built without one, as tests do.
func (r projectionReader) Outbox(chatID int64) []domain.OutboxEntry {
	if r.owner == nil || r.owner.outbox == nil {
		return nil
	}
	return r.owner.outbox.ForChat(chatID)
}
