package core

import (
	"context"
	"reflect"
	"sort"

	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// MergeOlder merges an older history chunk in front of the messages already
// held, dropping chunk entries whose IDs are already present. Duplicate
// in-flight loads or overlapping server pages would otherwise seed duplicates
// that render as a repeating date range (issue #120).
//
// Moved here from internal/ui: a client no longer knows whether a window is
// filled from disk or from the network, so the dedup belongs on this side.
func MergeOlder(older, existing []domain.Message) []domain.Message {
	if len(existing) == 0 {
		return older
	}
	seen := make(map[int]struct{}, len(existing))
	for _, m := range existing {
		seen[m.ID] = struct{}{}
	}
	combined := make([]domain.Message, 0, len(older)+len(existing))
	for _, m := range older {
		if _, dup := seen[m.ID]; dup {
			continue
		}
		combined = append(combined, m)
	}
	return append(combined, existing...)
}

func (o *Owner) windowPeer(w project.ChatWindow) (domain.Peer, bool) {
	if w.ThreadRootID != 0 && w.ThreadPeer.ID != 0 {
		return w.ThreadPeer, true
	}
	if chat, ok := o.reader().GetChat(w.ChatID); ok {
		return chat.Peer, true
	}
	return w.Peer, w.Peer.ID != 0
}

// backfill fetches older history for a chat subscription whose window the store
// could not fill and applies it to state; the registry then emits the resulting
// delta through the same path as any other change. One fetch per subscription is
// in flight at a time (issue #120).
func (o *Owner) backfill(ctx context.Context, id project.SubID, w project.ChatWindow) {
	if !o.beginFetch(id) {
		return
	}
	defer o.endFetch(id)

	peer, ok := o.windowPeer(w)
	if !ok {
		return
	}
	chat, chatKnown := o.state.Store().GetChat(w.ChatID)
	existing := o.state.Store().Messages(w.ChatID)
	contents := project.BuildChat(o.reader(), w)
	merged := existing
	candidates := contents.Messages
	historyChanged := false
	offsetID := 0
	gapLowerID, gapUpperID, fillGap := recoverableHistoryGap(contents.Messages, chat.Peer, o.Config().UI.HistoryLimit)
	fillUnread := w.ThreadRootID == 0 && w.Anchor.Kind == project.AnchorFirstUnread &&
		unreadHistoryIncomplete(existing, chat)

	if needsBackfill(contents, w) || fillGap || fillUnread {
		var fetched []domain.Message
		var err error
		switch {
		case fillGap:
			offsetID = gapUpperID
			fetched, err = o.fetchHistoryUntil(ctx, peer, gapUpperID, gapLowerID, nil)
		case fillUnread:
			fetched, err = o.fetchUnreadHistory(ctx, peer, chat, existing)
		default:
			fetched, offsetID, err = o.fetchHistoryPage(ctx, peer, w, existing)
		}
		if err != nil {
			o.log.Warn("history backfill failed", zap.Int64("chat", w.ChatID), zap.Error(err))
			// The client asked for a window it cannot fill itself, so it has to be
			// told: otherwise the pane waits on a load that will never arrive.
			o.publishFailure(Failure{ChatID: w.ChatID, Op: "load history", Err: err})
			return
		}
		if w.ThreadRootID != 0 || fillUnread || fillGap {
			merged = mergeHistoryRanges(fetched, existing)
		} else {
			merged = MergeOlder(fetched, existing)
		}
		historyChanged = len(merged) != len(existing)
		candidates = append(candidates, fetched...)
	}

	// Cached channel posts written by an older build have no comment metadata:
	// those fields did not exist when their JSON was persisted. Refresh only the
	// visible cached messages so opening a channel upgrades its posts without
	// clearing history or walking the entire cache.
	if w.ThreadRootID == 0 && chatKnown && chat.Peer.IsChannel() && len(contents.Messages) > 0 {
		ids := make([]int, 0, len(contents.Messages))
		for _, msg := range contents.Messages {
			if msg.ID > 0 {
				ids = append(ids, msg.ID)
			}
		}
		if len(ids) > 0 {
			refreshed, refreshErr := o.client.RefreshMessages(ctx, chat.Peer, ids)
			if refreshErr != nil {
				o.log.Warn("channel metadata refresh failed", zap.Int64("chat", w.ChatID), zap.Error(refreshErr))
			} else {
				var changed bool
				merged, changed = mergeRefreshedMessages(merged, refreshed)
				historyChanged = historyChanged || changed
				candidates = append(candidates, refreshed...)
			}
		}
	}

	var previewChanged bool
	o.rememberMessageTargets(candidates)
	merged, previewChanged, err := o.hydrateReplyPreviews(ctx, peer, merged, candidates)
	if err != nil {
		o.log.Warn("reply preview backfill failed", zap.Int64("chat", w.ChatID), zap.Error(err))
	}
	if !historyChanged && !previewChanged {
		return
	}
	o.log.Debug("history backfill",
		zap.Int64("chat", w.ChatID),
		zap.Int("offset_id", offsetID),
		zap.Int("held", len(existing)),
		zap.Int("merged", len(merged)),
		zap.Int("want", w.Before+w.After+1))
	o.state.ApplyHistory(w.ChatID, merged)
}

// fetchHistoryPage pages normally from the oldest held message. A message
// anchor that is not held is fetched directly first, then prefixed with its
// older context; walking every intervening page would make a jump arbitrarily
// slow in a long chat.
func (o *Owner) fetchHistoryPage(ctx context.Context, peer domain.Peer, w project.ChatWindow, existing []domain.Message) ([]domain.Message, int, error) {
	if w.Anchor.Kind == project.AnchorMessage && !hasMessage(existing, w.Anchor.MsgID) {
		window, err := o.fetchAnchorWindow(ctx, peer, w)
		return window, w.Anchor.MsgID, err
	}
	if w.ThreadRootID != 0 {
		offsetID := oldestThreadReplyID(existing, w.ThreadRootID)
		fetched, err := o.client.GetReplies(ctx, peer, w.ThreadRootID, offsetID, o.Config().UI.HistoryLimit)
		return fetched, offsetID, err
	}

	offsetID := 0
	if len(existing) > 0 {
		offsetID = existing[0].ID
	}
	fetched, err := o.client.GetHistory(ctx, peer, offsetID, o.Config().UI.HistoryLimit)
	return fetched, offsetID, err
}

func (o *Owner) fetchUnreadHistory(ctx context.Context, peer domain.Peer, chat domain.Chat, existing []domain.Message) ([]domain.Message, error) {
	return o.fetchHistoryUntil(ctx, peer, 0, chat.ReadInboxMaxID, func(fetched []domain.Message) bool {
		return !unreadHistoryIncomplete(mergeHistoryRanges(fetched, existing), chat)
	})
}

func (o *Owner) fetchHistoryUntil(ctx context.Context, peer domain.Peer, offsetID, stopID int, complete func([]domain.Message) bool) ([]domain.Message, error) {
	limit := o.Config().UI.HistoryLimit
	var fetched []domain.Message
	for {
		page, err := o.client.GetHistory(ctx, peer, offsetID, limit)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		fetched = mergeHistoryRanges(page, fetched)
		oldestID := page[0].ID
		if oldestID <= stopID || oldestID == offsetID || complete != nil && complete(fetched) {
			break
		}
		offsetID = oldestID
	}
	return fetched, nil
}

// Only channels and supergroups have conversation-local message IDs; a gap in a
// private or basic-group timeline says nothing is missing.
func recoverableHistoryGap(msgs []domain.Message, peer domain.Peer, minSize int) (lowerID, upperID int, ok bool) {
	if (!peer.IsChannel() && !peer.IsSuperGroup()) || minSize <= 0 {
		return 0, 0, false
	}
	for i := len(msgs) - 1; i > 0; i-- {
		lower, upper := msgs[i-1].ID, msgs[i].ID
		if upper-lower-1 >= minSize {
			return lower, upper, true
		}
	}
	return 0, 0, false
}

func unreadHistoryIncomplete(msgs []domain.Message, chat domain.Chat) bool {
	if chat.UnreadCount == 0 {
		return false
	}
	storedUnread := 0
	for _, msg := range msgs {
		if !msg.IsOut && msg.ID > chat.ReadInboxMaxID {
			storedUnread++
		}
	}
	return storedUnread < chat.UnreadCount
}

func (o *Owner) fetchAnchorWindow(ctx context.Context, peer domain.Peer, w project.ChatWindow) ([]domain.Message, error) {
	var (
		window []domain.Message
		err    error
	)
	if w.ThreadRootID != 0 {
		window, err = o.client.GetRepliesWindow(ctx, peer, w.ThreadRootID, w.Anchor.MsgID, w.Before, w.After)
	} else {
		window, err = o.client.GetHistoryWindow(ctx, peer, w.Anchor.MsgID, w.Before, w.After)
	}
	if err != nil || hasMessage(window, w.Anchor.MsgID) {
		return window, err
	}
	target, err := o.client.RefreshMessage(ctx, peer, w.Anchor.MsgID)
	if err != nil {
		return nil, err
	}
	if w.ThreadRootID != 0 {
		target.ThreadRootID = w.ThreadRootID
	}
	return mergeHistoryRanges([]domain.Message{target}, window), nil
}

// moveAnchoredWindow fills only contiguous pages around a message anchor, then
// moves the projection. Deferring the move matters: the store may also contain
// a distant live tail, which must not be rendered as the next message across an
// unfetched gap.
func (o *Owner) moveAnchoredWindow(ctx context.Context, id project.SubID, w project.ChatWindow) {
	defer o.endFetch(id)

	peer, ok := o.windowPeer(w)
	if !ok {
		return
	}
	current, ok := o.registry.Window(id)
	if !ok {
		return
	}

	var segment []domain.Message
	if prev, ok := current.(project.ChatWindow); ok && prev.ChatID == w.ChatID &&
		prev.Anchor.Kind == project.AnchorMessage && prev.Anchor.MsgID == w.Anchor.MsgID {
		contents := project.BuildChat(o.reader(), prev)
		if contents.AnchorMsgID == w.Anchor.MsgID {
			segment = contents.Messages
		}
	}

	var fetched []domain.Message
	if len(segment) == 0 {
		window, err := o.fetchAnchorWindow(ctx, peer, w)
		if err != nil {
			o.failAnchoredWindow(w, err)
			return
		}
		fetched = window
		segment = fetched
	} else {
		before, after, _ := anchorCounts(segment, w.Anchor.MsgID)
		if w.Before > before {
			limit := min(w.Before-before, o.Config().UI.HistoryLimit)
			if limit <= 0 {
				limit = w.Before - before
			}
			var older []domain.Message
			var err error
			if w.ThreadRootID != 0 {
				offsetID := oldestThreadReplyID(segment, w.ThreadRootID)
				older, err = o.client.GetReplies(ctx, peer, w.ThreadRootID, offsetID, limit)
			} else {
				older, err = o.client.GetHistory(ctx, peer, segment[0].ID, limit)
			}
			if err != nil {
				o.failAnchoredWindow(w, err)
				return
			}
			fetched = append(fetched, older...)
		}
		if w.After > after {
			limit := min(w.After-after, o.Config().UI.HistoryLimit)
			if limit <= 0 {
				limit = w.After - after
			}
			var newer []domain.Message
			var err error
			if w.ThreadRootID != 0 {
				newer, err = o.client.GetRepliesWindow(ctx, peer, w.ThreadRootID, segment[len(segment)-1].ID, 0, limit)
			} else {
				newer, err = o.client.GetHistoryWindow(ctx, peer, segment[len(segment)-1].ID, 0, limit)
			}
			if err != nil {
				o.failAnchoredWindow(w, err)
				return
			}
			fetched = append(fetched, newer...)
		}
		segment = mergeHistoryRanges(fetched, segment)
	}

	before, after, ok := anchorCounts(segment, w.Anchor.MsgID)
	if !ok {
		o.failAnchoredWindow(w, &telerr.Error{Kind: telerr.NotFound, Op: "load message anchor"})
		return
	}
	safeWindow := w
	safeWindow.Before = min(w.Before, before)
	safeWindow.After = min(w.After, after)

	existing := o.state.Store().Messages(w.ChatID)
	merged := mergeHistoryRanges(segment, existing)
	var previewChanged bool
	var err error
	merged, previewChanged, err = o.hydrateReplyPreviews(ctx, peer, merged, segment)
	if err != nil {
		o.log.Warn("reply preview backfill failed", zap.Int64("chat", w.ChatID), zap.Error(err))
	}
	if len(merged) != len(existing) || previewChanged {
		o.state.ApplyHistory(w.ChatID, merged)
	}
	o.publishAnchoredWindow(id, safeWindow)
}

func (o *Owner) queueAnchoredWindow(id project.SubID, w project.ChatWindow) {
	o.fetchMu.Lock()
	o.desiredAnchors[id] = w
	if o.fetching[id] {
		o.pendingAnchors[id] = w
		o.fetchMu.Unlock()
		return
	}
	o.fetching[id] = true
	o.fetchMu.Unlock()
	go o.moveAnchoredWindow(o.ctx, id, w)
}

func (o *Owner) publishAnchoredWindow(id project.SubID, w project.ChatWindow) {
	o.fetchMu.Lock()
	desired, ok := o.desiredAnchors[id]
	if ok && desired.ChatID == w.ChatID && desired.Anchor == w.Anchor {
		o.projectionMu.Lock()
		o.publish(o.registry.MoveWindow(id, w))
		o.projectionMu.Unlock()
	}
	o.fetchMu.Unlock()
}

func (o *Owner) failAnchoredWindow(w project.ChatWindow, err error) {
	o.log.Warn("message anchor load failed", zap.Int64("chat", w.ChatID),
		zap.Int("msg_id", w.Anchor.MsgID), zap.Error(err))
	o.publishFailure(Failure{ChatID: w.ChatID, Op: "load history", Err: err})
}

func anchorCounts(msgs []domain.Message, anchorID int) (before, after int, ok bool) {
	for i, msg := range msgs {
		if msg.ID == anchorID {
			return i, len(msgs) - i - 1, true
		}
	}
	return 0, 0, false
}

func mergeHistoryRanges(incoming, existing []domain.Message) []domain.Message {
	seen := make(map[int]struct{}, len(existing))
	out := append([]domain.Message(nil), existing...)
	for _, msg := range existing {
		seen[msg.ID] = struct{}{}
	}
	for _, msg := range incoming {
		if _, ok := seen[msg.ID]; ok {
			continue
		}
		seen[msg.ID] = struct{}{}
		out = append(out, msg)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Date.Equal(out[j].Date) {
			return out[i].Date.Before(out[j].Date)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// mergeRefreshedMessages replaces matching cached messages with current server
// values while preserving a locally hydrated reply preview the refresh does not
// carry. It never inserts a message outside the requested visible window.
func mergeRefreshedMessages(existing, refreshed []domain.Message) ([]domain.Message, bool) {
	byID := make(map[int]domain.Message, len(refreshed))
	for _, msg := range refreshed {
		byID[msg.ID] = msg
	}
	out := append([]domain.Message(nil), existing...)
	changed := false
	for i, old := range out {
		fresh, ok := byID[old.ID]
		if !ok {
			continue
		}
		if fresh.ReplyPreview == nil {
			fresh.ReplyPreview = old.ReplyPreview
		}
		if !reflect.DeepEqual(old, fresh) {
			out[i] = fresh
			changed = true
		}
	}
	return out, changed
}

func hasMessage(msgs []domain.Message, id int) bool {
	for _, msg := range msgs {
		if msg.ID == id {
			return true
		}
	}
	return false
}

func oldestThreadReplyID(msgs []domain.Message, rootID int) int {
	oldest := 0
	for _, msg := range msgs {
		if msg.ID == rootID || (msg.ThreadRootID != rootID && msg.ReplyToMsgID != rootID) {
			continue
		}
		if oldest == 0 || msg.ID < oldest {
			oldest = msg.ID
		}
	}
	return oldest
}

func needsReplyPreviews(msgs []domain.Message) bool {
	for _, msg := range msgs {
		if msg.ReplyToMsgID != 0 && msg.ReplyPreview == nil &&
			(msg.ReplyTarget != nil || !hasMessage(msgs, msg.ReplyToMsgID)) {
			return true
		}
	}
	return false
}

type messageKey struct {
	chatID int64
	msgID  int
}

func replyLookup(msg domain.Message, currentPeer domain.Peer) (messageKey, domain.Peer) {
	if target := msg.ReplyTarget; target != nil {
		return messageKey{chatID: target.ChatID, msgID: target.MsgID}, target.Peer
	}
	return messageKey{chatID: msg.ChatID, msgID: msg.ReplyToMsgID}, currentPeer
}

// hydrateReplyPreviews resolves only the originals referenced by candidates.
// The originals are not inserted into history: doing so would turn two distant
// ranges into one apparently contiguous range and break paging.
func (o *Owner) hydrateReplyPreviews(ctx context.Context, peer domain.Peer, all, candidates []domain.Message) ([]domain.Message, bool, error) {
	originals := make(map[messageKey]domain.Message, len(all))
	for _, msg := range all {
		originals[messageKey{chatID: msg.ChatID, msgID: msg.ID}] = msg
	}
	candidateIDs := make(map[int]struct{}, len(candidates))
	missing := make(map[domain.Peer]map[int]struct{})
	for _, msg := range candidates {
		candidateIDs[msg.ID] = struct{}{}
		if msg.ReplyToMsgID == 0 || msg.ReplyPreview != nil {
			continue
		}
		key, targetPeer := replyLookup(msg, peer)
		if _, ok := originals[key]; ok || targetPeer.ID == 0 {
			continue
		}
		if missing[targetPeer] == nil {
			missing[targetPeer] = make(map[int]struct{})
		}
		missing[targetPeer][key.msgID] = struct{}{}
	}

	var fetchErr error
	for targetPeer, set := range missing {
		ids := make([]int, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		fetched, err := o.client.RefreshMessages(ctx, targetPeer, ids)
		if err != nil {
			if fetchErr == nil {
				fetchErr = err
			}
			continue
		}
		for _, msg := range fetched {
			originals[messageKey{chatID: targetPeer.ID, msgID: msg.ID}] = msg
		}
	}

	changed := false
	for i := range all {
		if _, ok := candidateIDs[all[i].ID]; !ok || all[i].ReplyPreview != nil {
			continue
		}
		key, _ := replyLookup(all[i], peer)
		original, ok := originals[key]
		if !ok {
			continue
		}
		all[i].ReplyPreview = replyPreview(original)
		changed = true
	}
	return all, changed, fetchErr
}

func replyPreview(msg domain.Message) *domain.ReplyPreview {
	return &domain.ReplyPreview{
		SenderID: msg.SenderID, SenderName: msg.SenderName, Text: msg.Text, IsOut: msg.IsOut,
	}
}

func (o *Owner) hydrateIncomingReply(msg domain.Message) {
	if o.client == nil {
		return
	}
	var original domain.Message
	found := false
	targetKey, targetPeer := replyLookup(msg, domain.Peer{})
	if targetPeer.ID == 0 {
		chat, ok := o.reader().GetChat(msg.ChatID)
		if !ok {
			return
		}
		targetPeer = chat.Peer
	}
	for _, stored := range o.state.Store().Messages(targetKey.chatID) {
		if stored.ID == targetKey.msgID {
			original, found = stored, true
			break
		}
	}
	if !found {
		var err error
		original, err = o.client.RefreshMessage(o.ctx, targetPeer, targetKey.msgID)
		if err != nil {
			o.log.Warn("reply preview fetch failed",
				zap.Int64("chat", targetKey.chatID), zap.Int("msg_id", targetKey.msgID), zap.Error(err))
			return
		}
	}
	o.state.ApplyReplyPreview(msg.ChatID, msg.ID, *replyPreview(original))
}

func (o *Owner) beginFetch(id project.SubID) bool {
	o.fetchMu.Lock()
	defer o.fetchMu.Unlock()
	if o.fetching[id] {
		return false
	}
	o.fetching[id] = true
	return true
}

func (o *Owner) endFetch(id project.SubID) {
	o.fetchMu.Lock()
	if next, ok := o.pendingAnchors[id]; ok {
		delete(o.pendingAnchors, id)
		o.fetchMu.Unlock()
		go o.moveAnchoredWindow(o.ctx, id, next)
		return
	}
	delete(o.fetching, id)
	o.fetchMu.Unlock()
}
