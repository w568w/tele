package state

import (
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
)

// ApplyIncoming records a newly received message. The second result reports
// whether the client needs to hear about it at all; Change.UnreadChanged
// separately reports whether a counter moved.
//
// A message already held is a second delivery of one arrival, which is ordinary
// traffic under at-least-once delivery (ADR 0016), and an arrival happens once.
// Position refuses it before anything is written, which also keeps a recovered
// copy of the original from overwriting the edits that followed it. A copy with
// no position is caught after the fact instead, by the store reporting that it
// held the message already.
func (s *State) ApplyIncoming(msg domain.Message) (Change, bool) {
	if !s.st.AdvanceAppliedPosition(msg.ChatID, msg.ID, msg.AppliedPosition) {
		return Change{}, false
	}
	isNew, unreadChanged := store.ApplyIncomingMessage(s.st, msg)
	if !isNew {
		return Change{}, false
	}
	c := Change{
		Kind:          ChangeNewMessage,
		ChatID:        msg.ChatID,
		Message:       msg,
		MsgID:         msg.ID,
		UnreadChanged: unreadChanged,
	}
	s.commit(c)
	return c, true
}

// ApplyEdit records a message edited on another client.
//
// An edit update carries the message's whole current state, so everything in it
// is applied: the text, the entities and the reaction set. None of the three is
// an alternative to the others, and treating them as such dropped reactions
// until the chat was reopened (#199).
//
// The edit marker is the only conditional part. A message nobody edited carries
// no edit date and must not be given one: in a 1:1 chat an incoming reaction is
// delivered as an edit and nothing else (#160), and it must not flip the
// message to "edited" (#118). Whether an edit that did happen shows the label
// is Telegram's call, carried separately in EditHidden - which is why the text
// lands either way (#269).
//
// An edit at or behind the position the message has already applied is a late
// copy of a change that has already happened. It writes nothing and reports
// nothing, so a newer text can never be replaced by an older one (ADR 0016).
func (s *State) ApplyEdit(msg domain.Message) (Change, bool) {
	if !s.st.AdvanceAppliedPosition(msg.ChatID, msg.ID, msg.AppliedPosition) {
		return Change{}, false
	}
	if msg.IsService {
		for _, old := range s.st.Messages(msg.ChatID) {
			if old.ID != msg.ID {
				continue
			}
			if msg.SenderName == "" {
				msg.SenderName = old.SenderName
			}
			if msg.ReplyPreview == nil && msg.ReplyToMsgID == old.ReplyToMsgID {
				msg.ReplyPreview = old.ReplyPreview
			}
			s.st.ReplaceMessage(msg.ChatID, msg)
			unreadChanged := false
			if msg.HasUnreadReactions {
				unreadChanged = s.st.ApplyUnreadReaction(msg.ChatID, msg.ID, true)
			}
			c := Change{
				Kind:                  ChangeMessageEdited,
				ChatID:                msg.ChatID,
				Message:               msg,
				MsgID:                 msg.ID,
				ReactionsUnread:       msg.HasUnreadReactions,
				UnreadReactionChanged: unreadChanged,
			}
			s.commit(c)
			return c, true
		}
		return Change{}, false
	}
	if msg.EditDate == nil {
		return s.ApplyReactions(msg.ChatID, msg.ID, msg.Reactions, msg.HasUnreadReactions)
	}
	s.st.UpdateMessageText(msg.ChatID, msg.ID, msg.Text, msg.Entities)
	for _, stored := range s.st.Messages(msg.ChatID) {
		if stored.ID == msg.ID {
			stored.HasWebPreview = msg.HasWebPreview
			stored.AppliedPosition = msg.AppliedPosition
			s.st.ReplaceMessage(msg.ChatID, stored)
			break
		}
	}
	s.st.MarkMessageEdited(msg.ChatID, msg.ID, *msg.EditDate, msg.EditHidden)
	s.st.UpdateMessageReactions(msg.ChatID, msg.ID, msg.Reactions)
	unreadChanged := false
	if msg.HasUnreadReactions {
		unreadChanged = s.st.ApplyUnreadReaction(msg.ChatID, msg.ID, true)
	}
	c := Change{
		Kind:                  ChangeMessageEdited,
		ChatID:                msg.ChatID,
		Message:               msg,
		MsgID:                 msg.ID,
		ReactionsUnread:       msg.HasUnreadReactions,
		UnreadReactionChanged: unreadChanged,
	}
	s.commit(c)
	return c, true
}

// ApplyRestore puts a message back after a refused delete. Unlike ApplyIncoming
// it is not an arrival: no counter moves and nothing is notified.
func (s *State) ApplyRestore(msg domain.Message) (Change, bool) {
	s.st.AppendMessage(msg)
	c := Change{Kind: ChangeMessageRestored, ChatID: msg.ChatID, Message: msg, MsgID: msg.ID}
	s.commit(c)
	return c, true
}

// ApplyEditRestore puts a message back as it was before an edit Telegram
// refused, including clearing the EditDate the optimistic version stamped on.
// ApplyEdit cannot do this: it only ever sets the marker, because an update
// that arrives without an edit date says nothing about one that did not.
func (s *State) ApplyEditRestore(msg domain.Message) (Change, bool) {
	s.st.ReplaceMessage(msg.ChatID, msg)
	c := Change{Kind: ChangeMessageEdited, ChatID: msg.ChatID, Message: msg, MsgID: msg.ID}
	s.commit(c)
	return c, true
}

// ApplyReactions records the current reaction set for a message and tracks
// whether the chat's unread-reaction count moved.
func (s *State) ApplyReactions(chatID int64, msgID int, r []domain.Reaction, unread bool) (Change, bool) {
	s.st.UpdateMessageReactions(chatID, msgID, r)
	changed := false
	if unread {
		changed = s.st.ApplyUnreadReaction(chatID, msgID, true)
	}
	c := Change{
		Kind:                  ChangeMessageReactions,
		ChatID:                chatID,
		MsgID:                 msgID,
		ReactionsUnread:       unread,
		UnreadReactionChanged: changed,
	}
	s.commit(c)
	return c, true
}

// ApplyDelete removes messages. A zero chatID means a non-channel delete with
// no peer context: the store resolves each ID to its owning chat through its
// index rather than scanning every chat (#72).
func (s *State) ApplyDelete(chatID int64, msgIDs []int) (Change, bool) {
	if chatID != 0 {
		s.st.RemoveMessages(chatID, msgIDs)
	} else {
		s.st.RemoveMessagesByID(msgIDs)
	}
	c := Change{
		Kind:   ChangeMessagesDeleted,
		ChatID: chatID,
		MsgIDs: msgIDs,
	}
	s.commit(c)
	return c, true
}

// ApplyMediaRef replaces a message's photo and document references, which
// Telegram expires periodically. A nil reference leaves that side untouched.
// The owner is the only caller: it refreshes a reference mid-download and
// records the fresh one here so the next fetch does not repeat the round trip.
func (s *State) ApplyMediaRef(chatID int64, msgID int, photo *domain.PhotoRef, doc *domain.DocumentRef) (Change, bool) {
	s.st.UpdateMessageMedia(chatID, msgID, photo, doc)
	c := Change{Kind: ChangeMediaRef, ChatID: chatID, MsgID: msgID}
	s.commit(c)
	return c, true
}

// ApplyReplyPreview fills the quoted-message fields of a stored reply without
// treating the lookup as a new message or a content edit.
func (s *State) ApplyReplyPreview(chatID int64, msgID int, preview domain.ReplyPreview) (Change, bool) {
	for _, msg := range s.st.Messages(chatID) {
		if msg.ID != msgID || msg.ReplyToMsgID == 0 || msg.ReplyPreview != nil {
			continue
		}
		msg.ReplyPreview = &preview
		s.st.ReplaceMessage(chatID, msg)
		c := Change{Kind: ChangeReplyPreview, ChatID: chatID, Message: msg, MsgID: msgID}
		s.commit(c)
		return c, true
	}
	return Change{}, false
}

// ApplyHistory merges a fetched history snapshot into the stored messages. The
// store performs the merge atomically so a live arrival during the fetch cannot
// be erased by the older snapshot.
func (s *State) ApplyHistory(chatID int64, msgs []domain.Message) (Change, bool) {
	s.st.MergeMessages(chatID, msgs)
	c := Change{Kind: ChangeHistory, ChatID: chatID}
	s.commit(c)
	return c, true
}

// MergeHistory joins a fetched page to a chat's stored history and publishes one
// change, so the chat:<id> projection rebuilds through the same path as every
// other change. The merge itself happens inside the store, under its lock,
// which is what keeps a message arriving mid-fetch from being overwritten.
//
// A page that added nothing publishes nothing: it means the fetch reached
// history the store already had, and rebuilding every window to say so would
// cost a frame for no news.
func (s *State) MergeHistory(chatID int64, msgs []domain.Message) (Change, bool) {
	if s.st.MergeMessages(chatID, msgs) == 0 {
		return Change{}, false
	}
	c := Change{Kind: ChangeHistory, ChatID: chatID}
	s.commit(c)
	return c, true
}

// RepairHistory joins a page fetched to close a gap. It differs from
// MergeHistory in what it promises the store: a repair leaves the chat no
// deeper than it found it, because nobody asked for the page and its cost
// should not outlive the hole it filled.
func (s *State) RepairHistory(chatID int64, msgs []domain.Message) (Change, bool) {
	if s.st.RepairMessages(chatID, msgs) == 0 {
		return Change{}, false
	}
	c := Change{Kind: ChangeHistory, ChatID: chatID}
	s.commit(c)
	return c, true
}
