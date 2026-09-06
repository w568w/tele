package core

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	internaltg "github.com/sorokin-vladimir/tele/internal/tg"
)

type importantItem struct {
	root              int
	rootKnown         bool
	mention, reaction bool
	version           uint64
}

type importantIndex struct {
	items    map[int]importantItem
	reading  map[int]bool
	loaded   bool
	err      error
	done     chan struct{}
	at       time.Time
	revision uint64
}

func (o *Owner) importantLocked(chatID int64) *importantIndex {
	if o.important == nil {
		o.important = make(map[int64]*importantIndex)
	}
	idx := o.important[chatID]
	if idx == nil {
		idx = &importantIndex{items: make(map[int]importantItem), reading: make(map[int]bool)}
		o.important[chatID] = idx
	}
	return idx
}

func (o *Owner) LinkedDiscussionGroup(ctx context.Context, chatID int64) (domain.Chat, error) {
	peer, err := o.peer(chatID)
	if err != nil {
		return domain.Chat{}, err
	}
	client, ok := o.client.(internaltg.LinkedGroupClient)
	if !ok {
		return domain.Chat{}, &telerr.Error{Kind: telerr.Internal, Op: "open discussion group"}
	}
	if !peer.IsChannel() {
		return domain.Chat{}, &telerr.Error{Kind: telerr.NotFound, Op: "open discussion group", Detail: "not a broadcast channel"}
	}
	chat, err := client.GetLinkedGroup(ctx, peer)
	if err == nil {
		o.rememberTransientChat(chat)
	}
	return chat, err
}

// RefreshImportant scans only Telegram's unread lists, never the entire chat
// history. Concurrent callers share one scan. Updates newer than the scan are
// overlaid, including tombstones, so late replies cannot resurrect read items.
func (o *Owner) RefreshImportant(ctx context.Context, chatID int64) error {
	client, ok := o.client.(internaltg.ImportantClient)
	if !ok {
		return nil
	}
	peer, err := o.peer(chatID)
	if err != nil {
		return err
	}
	o.importantMu.Lock()
	idx := o.importantLocked(chatID)
	if idx.done != nil {
		done := idx.done
		o.importantMu.Unlock()
		select {
		case <-done:
			o.importantMu.Lock()
			err := idx.err
			o.importantMu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if time.Since(idx.at) < 30*time.Second && idx.loaded && idx.err == nil {
		o.importantMu.Unlock()
		return nil
	}
	idx.done = make(chan struct{})
	done, start := idx.done, idx.revision
	o.importantMu.Unlock()
	o.Refresh()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	items, err := scanImportant(ctx, client, peer)
	o.importantMu.Lock()
	if err == nil {
		for id, entry := range idx.items {
			if entry.version > start {
				if !entry.rootKnown && items[id].rootKnown {
					entry.root, entry.rootKnown = items[id].root, true
				}
				items[id] = entry
			}
		}
		idx.items, idx.loaded, idx.at = items, true, time.Now()
	}
	idx.err = err
	idx.revision++
	idx.done = nil
	close(done)
	o.importantMu.Unlock()
	o.Refresh()
	return err
}

func scanImportant(ctx context.Context, client internaltg.ImportantClient, peer domain.Peer) (map[int]importantItem, error) {
	items := make(map[int]importantItem)
	// Broadcast channels do not have personal mention/reaction notifications.
	if peer.IsChannel() || peer.Type == domain.PeerSelf {
		return items, nil
	}
	for _, kind := range []domain.ImportantKind{domain.ImportantMention, domain.ImportantReaction} {
		if kind == domain.ImportantMention && !peer.IsGroup() {
			continue
		}
		offset := 0
		for {
			page, err := client.GetUnreadImportant(ctx, peer, kind, offset)
			if err != nil {
				return nil, err
			}
			if len(page.Messages) == 0 {
				if offset == 0 && page.Count > 0 {
					return nil, fmt.Errorf("unread targets not yet available")
				}
				break
			}
			next := 0
			for _, msg := range page.Messages {
				if msg.ID <= 0 {
					continue
				}
				if next == 0 || msg.ID < next {
					next = msg.ID
				}
				item := items[msg.ID]
				item.root = msg.ThreadRootID
				item.rootKnown = true
				if item.root == 0 {
					item.root = msg.ReplyToMsgID
				}
				if kind == domain.ImportantMention {
					item.mention = true
				} else {
					item.reaction = true
				}
				items[msg.ID] = item
			}
			if next == 0 || (offset != 0 && next >= offset) {
				return nil, fmt.Errorf("unread list pagination did not advance")
			}
			offset = next
		}
	}
	return items, nil
}

func (o *Owner) ImportantUnread(chatID int64, rootID int) domain.ImportantUnread {
	if _, ok := o.client.(internaltg.ImportantClient); !ok {
		return domain.ImportantUnread{}
	}
	o.importantMu.Lock()
	defer o.importantMu.Unlock()
	idx := o.important[chatID]
	if idx == nil {
		return domain.ImportantUnread{Loading: true}
	}
	result := domain.ImportantUnread{Loading: !idx.loaded && idx.err == nil, Failed: idx.err != nil, Revision: idx.revision}
	for id, item := range idx.items {
		if rootID != 0 && !item.rootKnown && (item.mention || item.reaction) {
			result.Loading = true
		}
		if rootID != 0 && item.root != rootID && id != rootID {
			continue
		}
		if item.mention {
			result.Mentions++
		}
		if item.reaction {
			result.Reactions++
		}
	}
	return result
}

func (o *Owner) PreviousImportant(ctx context.Context, chatID int64, rootID, beforeID int) (int, error) {
	if err := o.RefreshImportant(ctx, chatID); err != nil {
		return 0, err
	}
	o.importantMu.Lock()
	defer o.importantMu.Unlock()
	idx := o.importantLocked(chatID)
	previous, newest := 0, 0
	for id, item := range idx.items {
		if (!item.mention && !item.reaction) || idx.reading[id] || (rootID != 0 && item.root != rootID && id != rootID) {
			continue
		}
		newest = max(newest, id)
		if beforeID > id {
			previous = max(previous, id)
		}
	}
	if previous != 0 {
		return previous, nil
	}
	return newest, nil
}

func (o *Owner) ReadVisibleImportant(ctx context.Context, chatID int64, rootID int, ids []int) error {
	client, ok := o.client.(internaltg.ImportantClient)
	if !ok {
		return nil
	}
	peer, err := o.peer(chatID)
	if err != nil {
		return err
	}
	o.importantMu.Lock()
	idx := o.importantLocked(chatID)
	claimed := make(map[int]uint64)
	for _, id := range ids {
		item := idx.items[id]
		if idx.reading[id] || (!item.mention && !item.reaction) || (rootID != 0 && item.root != rootID && id != rootID) {
			continue
		}
		claimed[id], idx.reading[id] = item.version, true
	}
	o.importantMu.Unlock()
	if len(claimed) == 0 {
		return nil
	}
	batch := make([]int, 0, len(claimed))
	for id := range claimed {
		batch = append(batch, id)
	}
	sort.Ints(batch)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	confirmed := make(map[int]bool)
	for start := 0; start < len(batch); start += 100 {
		part := batch[start:min(start+100, len(batch))]
		if err = client.ReadImportantContents(ctx, peer, part); err != nil {
			break
		}
		for _, id := range part {
			confirmed[id] = true
		}
	}
	o.importantMu.Lock()
	idx.revision++
	for id, version := range claimed {
		delete(idx.reading, id)
		if confirmed[id] && idx.items[id].version == version {
			idx.items[id] = importantItem{version: idx.revision}
		}
	}
	o.importantMu.Unlock()
	o.Refresh()
	return err
}

func (o *Owner) applyImportantEvent(evt store.Event) (unknownRoot bool) {
	chatID := evt.ChatID
	if evt.Kind == store.EventNewMessage || evt.Kind == store.EventEditMessage {
		chatID = evt.Message.ChatID
	}
	o.importantMu.Lock()
	defer o.importantMu.Unlock()
	for id, idx := range o.important {
		if chatID != 0 && id != chatID {
			continue
		}
		// A zero chat ID belongs to the common message box, not channels.
		if chatID == 0 {
			chat, ok := o.state.Store().GetChat(id)
			if !ok || chat.Peer.IsChannel() || chat.Peer.IsSuperGroup() {
				continue
			}
		}
		idx.revision++
		switch evt.Kind {
		case store.EventReadContents, store.EventDeleteMessages:
			for _, msgID := range evt.MsgIDs {
				idx.items[msgID] = importantItem{version: idx.revision}
			}
		case store.EventNewMessage, store.EventEditMessage:
			msg := evt.Message
			root := msg.ThreadRootID
			if root == 0 {
				root = msg.ReplyToMsgID
			}
			idx.items[msg.ID] = importantItem{root: root, rootKnown: true, mention: msg.Mentioned && msg.MediaUnread, reaction: msg.HasUnreadReactions, version: idx.revision}
		case store.EventReactionsUpdate:
			item := idx.items[evt.MsgID]
			item.reaction, item.version = evt.ReactionsUnread, idx.revision
			if evt.ThreadRootID != 0 {
				item.root, item.rootKnown = evt.ThreadRootID, true
			}
			if item.reaction && !item.rootKnown {
				unknownRoot = true
				idx.at = time.Time{}
			}
			idx.items[evt.MsgID] = item
		}
	}
	return unknownRoot
}

// Reaction updates may omit thread metadata for a message outside history.
// Fetch only its metadata; never insert this isolated result into history.
func (o *Owner) hydrateImportantRoot(chatID int64, msgID int) {
	peer, err := o.peer(chatID)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(o.ctx, 15*time.Second)
	defer cancel()
	msg, err := o.client.RefreshMessage(ctx, peer, msgID)
	if err != nil || msg.ID != msgID {
		return
	}
	o.importantMu.Lock()
	idx := o.importantLocked(chatID)
	item := idx.items[msgID]
	if !item.rootKnown && (item.mention || item.reaction) {
		item.root, item.rootKnown = msg.ThreadRootID, true
		if item.root == 0 {
			item.root = msg.ReplyToMsgID
		}
		idx.revision++
		item.version = idx.revision
		idx.items[msgID] = item
	}
	o.importantMu.Unlock()
	o.Refresh()
}
