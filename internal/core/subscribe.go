package core

import (
	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/core/state"
)

// projectionReader is what every projection is built from: the store plus the
// send queue. Asserted here so a change to either interface fails at compile
// time rather than at wiring time.
var _ project.Reader = projectionReader{}

// Deltas is the stream every attached client consumes. Raw state changes do not
// reach a client: a client sees only the projections it subscribed to.
func (o *Owner) Deltas() <-chan project.Delta { return o.deltas }

// Subscribe registers a window. The subscription's first delta carries its
// current contents, which is what makes a resubscribe a full resync.
func (o *Owner) Subscribe(w project.Window) project.SubID {
	// Bring the chat's persisted tail into memory before the window is built, so
	// it paints cached history at once instead of waiting on the network — and
	// still shows something when there is no network at all (#139).
	if cw, ok := w.(project.ChatWindow); ok {
		o.state.Store().LoadMessages(cw.ChatID)
	}
	id, deltas := o.registry.Subscribe(w)
	o.publish(deltas)
	o.maybeBackfill(id, w)
	return id
}

// MoveWindow repositions a subscription. It returns immediately: over a socket a
// window move cannot be synchronous, so it is not synchronous here either.
func (o *Owner) MoveWindow(id project.SubID, w project.Window) {
	if cw, ok := w.(project.ChatWindow); ok && cw.Anchor.Kind == project.AnchorMessage && o.client != nil {
		o.queueAnchoredWindow(id, cw)
		return
	}
	o.fetchMu.Lock()
	delete(o.desiredAnchors, id)
	delete(o.pendingAnchors, id)
	o.publish(o.registry.MoveWindow(id, w))
	o.fetchMu.Unlock()
	o.maybeBackfill(id, w)
}

func (o *Owner) Unsubscribe(id project.SubID) {
	o.fetchMu.Lock()
	delete(o.desiredAnchors, id)
	delete(o.pendingAnchors, id)
	o.registry.Unsubscribe(id)
	o.fetchMu.Unlock()
}

// Refresh rebuilds every subscription against current state.
//
// TRANSITIONAL (#193, #195, #196): media still writes to the store directly and
// asks for a rebuild. Commands no longer do — they mutate through state, whose
// commit publishes. The forward preview bump is the one caller inside the owner.
func (o *Owner) Refresh() { o.publish(o.registry.Refresh()) }

// maybeBackfill fetches from Telegram when a chat window asked for more history
// than the store holds, so a client never has to know where data comes from.
func (o *Owner) maybeBackfill(id project.SubID, w project.Window) {
	cw, ok := w.(project.ChatWindow)
	if !ok || o.client == nil {
		return
	}
	contents := project.BuildChat(o.reader(), cw)
	if !needsBackfill(contents, cw) && !needsReplyPreviews(contents.Messages) {
		return
	}
	go o.backfill(o.ctx, id, cw)
}

// needsBackfill reports that the store could not fill the window: it returned
// fewer messages than were asked for and has nothing older left to give. This is
// deliberately not HasOlder, which reports the opposite — that the store does
// hold more, so no fetch is needed.
func needsBackfill(c project.ChatContents, w project.ChatWindow) bool {
	return !c.HasOlder && len(c.Messages) < w.Before+w.After+1
}

// publishChange turns one applied domain change into whatever the current
// subscriptions need to hear. Typing goes out as an event instead: it has no
// persisted state to rebuild a projection from, and nothing would ever clear it
// if it were held as one.
func (o *Owner) publishChange(chg state.Change) {
	if chg.Kind == state.ChangeTyping {
		o.publishTyping(Typing{ChatID: chg.ChatID, Label: chg.Typing.Label()})
		return
	}
	// A message arriving may be one this owner queued. Dropping the entry here,
	// before the rebuild, is what makes the pending bubble and the real message
	// swap inside a single delta rather than across two, with a frame showing
	// neither in between (#193).
	if chg.Kind == state.ChangeNewMessage {
		o.clearSentOutbox(chg.ChatID)
		if chg.Message.ReplyToMsgID != 0 && chg.Message.ReplyPreview == nil {
			go o.hydrateIncomingReply(chg.Message)
		}
	}
	o.publish(o.registry.Refresh())
}

// publish drops deltas rather than blocking when a client is not draining:
// backpressure must never stall the owner's update loop. A dropped delta costs a
// stale window until the next change, and a resubscribe resyncs it.
func (o *Owner) publish(ds []project.Delta) {
	for _, d := range ds {
		select {
		case o.deltas <- d:
		default:
			o.log.Warn("projection delta dropped: client is not draining")
		}
	}
}
