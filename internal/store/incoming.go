package store

import "github.com/sorokin-vladimir/tele/internal/domain"

// ApplyIncomingMessage records a newly received message and updates the
// account-level unread state it implies. It takes no viewport: unread is a
// property of the account, not of whichever chat a client happens to have open,
// so the same event produces the same state with several clients attached or
// none at all (#189, groundwork for #183).
//
// isNew reports whether the message was not already held, which is how the
// second delivery of one arrival is told from the first. counted reports whether
// a chat's unread or mention count moved, so callers can refresh derived views
// such as folder counts.
func ApplyIncomingMessage(s Store, msg domain.Message) (isNew, counted bool) {
	isNew = s.AppendMessage(msg)
	if msg.IsOut {
		return isNew, false
	}
	counted = s.ApplyUnreadMessage(msg.ChatID, msg.ID)
	if msg.Mentioned && msg.MediaUnread && s.ApplyUnreadMention(msg.ChatID, msg.ID, true) {
		counted = true
	}
	return isNew, counted
}
