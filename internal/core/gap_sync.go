package core

import (
	"context"

	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
)

// fill repairs an account-wide or channel gap before doing the ordinary window
// backfill. The gap work is independent of a subscription, while backfill keeps
// the existing per-subscription anchor and reply-preview behavior.
func (o *Owner) fill(ctx context.Context, id project.SubID, w project.ChatWindow) {
	o.forwardFill(ctx, w.ChatID)
	o.backfill(ctx, id, w)
}

func (o *Owner) recordGap(chatID int64) {
	st := o.state.Store()
	tail := st.TailMessageID(chatID)
	if tail == 0 {
		return
	}
	st.MarkGap(chatID, tail)
	o.log.Info("gap recorded", zap.Int64("chat", chatID), zap.Int("after", tail))
	if o.focus.focused(chatID) {
		go o.forwardFill(o.ctx, chatID)
	}
}

func (o *Owner) scanForGaps(ctx context.Context) {
	chats, err := o.client.GetDialogs(ctx)
	if err != nil {
		o.log.Warn("gap scan could not reload the dialog list", zap.Error(err))
		return
	}
	o.state.SetDialogs(chats)
	st := o.state.Store()
	marked := 0
	for _, chat := range chats {
		tail := st.TailMessageID(chat.ID)
		if tail == 0 || chat.TopMessageID <= tail {
			continue
		}
		st.MarkGap(chat.ID, tail)
		marked++
		if o.focus.focused(chat.ID) {
			go o.forwardFill(ctx, chat.ID)
		}
	}
	o.log.Info("gap scan done", zap.Int("dialogs", len(chats)), zap.Int("marked", marked))
}

func (o *Owner) forwardFill(ctx context.Context, chatID int64) {
	st := o.state.Store()
	if _, open := st.Gap(chatID); !open {
		return
	}
	chat, ok := st.GetChat(chatID)
	if !ok || !o.beginRepair(chatID) {
		return
	}
	defer o.endRepair(chatID)

	limit := o.Config().UI.HistoryLimit
	budget := store.MaxMessagesPerChat
	for {
		after, open := st.Gap(chatID)
		if !open {
			return
		}
		page, err := o.client.GetHistoryAfter(ctx, chat.Peer, after, limit)
		if err != nil {
			o.log.Warn("gap repair failed", zap.Int64("chat", chatID), zap.Int("after", after), zap.Error(err))
			return
		}
		newest := 0
		if len(page) > 0 {
			newest = page[len(page)-1].ID
		}
		if newest <= after {
			st.CloseGap(chatID, after)
			return
		}
		o.state.RepairHistory(chatID, page)
		if !st.AdvanceGap(chatID, after, newest) {
			continue
		}
		if chat.TopMessageID > 0 && newest >= chat.TopMessageID {
			st.CloseGap(chatID, newest)
			return
		}
		budget -= len(page)
		if budget <= 0 {
			o.tailReload(ctx, chat)
			return
		}
	}
}

func (o *Owner) tailReload(ctx context.Context, chat domain.Chat) {
	page, err := o.client.GetHistory(ctx, chat.Peer, 0, o.Config().UI.HistoryLimit)
	if err != nil || len(page) == 0 {
		if err != nil {
			o.log.Warn("tail reload failed", zap.Int64("chat", chat.ID), zap.Error(err))
		}
		return
	}
	o.state.Store().SetMessages(chat.ID, page)
	o.Refresh()
	o.state.Store().ClearGap(chat.ID)
}

func (o *Owner) beginRepair(chatID int64) bool {
	o.fetchMu.Lock()
	defer o.fetchMu.Unlock()
	if o.repairing[chatID] {
		return false
	}
	o.repairing[chatID] = true
	return true
}

func (o *Owner) endRepair(chatID int64) {
	o.fetchMu.Lock()
	delete(o.repairing, chatID)
	o.fetchMu.Unlock()
}
