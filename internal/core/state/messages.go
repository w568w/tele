package state

import (
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
)

// ApplyIncoming records a newly received message. The second result reports
// whether the client needs to hear about it at all; Change.UnreadChanged
// separately reports whether a counter moved.
func (s *State) ApplyIncoming(msg domain.Message) (Change, bool) {
	unreadChanged := store.ApplyIncomingMessage(s.st, msg)
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
// Every edit update carries the message's whole current reaction set, so the
// reactions are applied either way. What EditDate decides is only whether the
// text changed too:
//
// A nil EditDate means the converter dropped it as a hidden edit (Telegram
// edit_hide), for example a reaction bump. That is not a content edit and must
// not flip the message to "edited" (#118) — but in 1:1 chats an incoming
// reaction is delivered ONLY as this hidden edit, carrying the message's new
// reactions rather than a separate UpdateMessageReactions (#160). So a hidden
// edit is applied as, and reported as, a reactions change.
//
// A non-nil EditDate is a real content edit — or a reaction on a message that
// was genuinely edited earlier, where edit_date still carries the original edit
// time and edit_hide is false because the "edited" label should keep showing.
// Text and reactions are not alternatives, and treating them as such dropped
// those reactions until the chat was reopened (#199).
func (s *State) ApplyEdit(msg domain.Message) (Change, bool) {
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
	s.st.UpdateMessageText(msg.ChatID, msg.ID, msg.Text, msg.Entities, msg.HasWebPreview, *msg.EditDate)
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
// ApplyEdit cannot do this: a message with no EditDate means "reactions only"
// there, which is right for the update path and wrong for a rollback.
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
