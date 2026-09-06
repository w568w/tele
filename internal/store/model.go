package store

import (
	"time"

	"github.com/sorokin-vladimir/tele/internal/domain"
)

type EventKind int

const (
	EventNewMessage EventKind = iota
	EventReadInbox
	EventReadOutbox
	EventReactionsUpdate
	EventDeleteMessages
	EventUserPresence
	EventTyping
	// EventMuteUpdate reports that a chat's mute state changed server-side
	// (e.g. muted/unmuted from another device).
	EventMuteUpdate
	// EventEditMessage reports that a message was edited on another client.
	// The updated message is carried in Event.Message.
	EventEditMessage
	// EventDraftMessage reports that a chat's draft changed server-side (e.g.
	// edited on another device, or cleared on send). The text is in Event.Draft.
	EventDraftMessage
	EventReadContents
)

type Event struct {
	Kind      EventKind
	Message   domain.Message
	ChatID    int64
	ReadMaxID int
	MsgID     int
	MsgIDs    []int
	Reactions []domain.Reaction
	// ReactionsUnread reports that an EventReactionsUpdate carries at least one
	// unread recent reaction (from UpdateMessageReactions).
	ReactionsUnread bool
	ThreadRootID    int
	// ReactionEmoji is the emoji of the newest unread reaction (for notifications);
	// empty for a custom-emoji reaction. ReactionDate is when that reaction was
	// added, used to suppress stale catch-up reactions after an idle reconnect.
	ReactionEmoji string
	ReactionDate  time.Time
	Online        bool
	TypingAction  domain.TypingAction
	Muted         bool
	// Draft carries the new draft text for EventDraftMessage.
	Draft string
}
