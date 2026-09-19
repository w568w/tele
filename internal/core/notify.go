package core

import (
	"time"

	"github.com/sorokin-vladimir/tele/internal/store"
)

// Notifier sends desktop notifications: the sink that reaches the operating
// system's own notification service.
type Notifier interface {
	Notify(title, body string) error
}

// NotifyFreshnessWindow bounds how old an event may be and still raise a
// notification. Catch-up messages recovered by getDifference after an idle
// period carry their original (old) send time, so anything older than this
// window is treated as backlog and stays silent (#123). Live delivery latency is
// typically well under it.
//
// It lives here rather than in store because freshness is policy, not storage.
const NotifyFreshnessWindow = 10 * time.Second

// Notification is the one decision: this event deserves the user's attention.
// It is rendered once, here, and handed unchanged to every sink — the OS
// notifier and the attached clients. Nothing downstream re-judges or re-renders
// it, which is what makes it structurally impossible for a toast and an OS
// banner to disagree about the same event (#192).
type Notification struct {
	ChatID int64
	Title  string
	Body   string
}

// decideNotification is the whole notification policy. It is pure: the store,
// the clients' focus and the clock all arrive as arguments, so every rule is a
// table test with no owner in sight.
//
// focused reports that some attached client is showing that chat. With nobody
// attached nothing is focused, which is what lets a daemon notify with no client
// running at all (#182).
func decideNotification(
	st store.Store,
	evt store.Event,
	focused func(int64) bool,
	preview bool,
	now time.Time,
) (Notification, bool) {
	chatID, at, body, ok := trigger(evt, preview)
	if !ok {
		return Notification{}, false
	}
	if focused(chatID) {
		// You are looking at it; a banner would tell you nothing.
		return Notification{}, false
	}
	chat, ok := st.GetChat(chatID)
	if !ok || chat.IsMuted || chat.IsArchived {
		return Notification{}, false
	}
	if at.IsZero() || now.Sub(at) > NotifyFreshnessWindow {
		return Notification{}, false
	}
	return Notification{ChatID: chatID, Title: chat.Title, Body: body}, true
}

// trigger reduces an event to what a notification would be made of, or reports
// that this kind of event never notifies. It is the only place a body is
// rendered, so the privacy rule (#80) is written exactly once - and applies to
// every sink, the toast included, because they are all handed this one body.
func trigger(evt store.Event, preview bool) (chatID int64, at time.Time, body string, ok bool) {
	switch evt.Kind {
	case store.EventNewMessage:
		if evt.Message.IsOut {
			return 0, time.Time{}, "", false
		}
		body := truncate(evt.Message.Text, 100)
		if !preview {
			// Some platforms persist notification bodies (macOS Notification
			// Center, the systemd journal), so omit the text (#80).
			body = "New message"
		}
		return evt.Message.ChatID, evt.Message.Date, body, true
	case store.EventReactionsUpdate:
		// A peer reacted to one of our messages in a group or channel.
		if !evt.ReactionsUnread {
			return 0, time.Time{}, "", false
		}
		return evt.ChatID, evt.ReactionDate, reactionBody(evt.ReactionEmoji), true
	case store.EventEditMessage:
		// A 1:1 peer's reaction arrives as a hidden edit; a real text edit
		// carries no unread reactions and must not notify.
		if !evt.Message.HasUnreadReactions {
			return 0, time.Time{}, "", false
		}
		return evt.Message.ChatID, evt.ReactionDate, reactionBody(evt.ReactionEmoji), true
	}
	return 0, time.Time{}, "", false
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// reactionBody renders the body for a reaction, including the emoji when there
// is one — custom-emoji reactions carry none.
func reactionBody(emoji string) string {
	if emoji == "" {
		return "reacted to your message"
	}
	return "reacted " + emoji + " to your message"
}
