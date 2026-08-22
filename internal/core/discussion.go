package core

import (
	"context"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// OpenDiscussion resolves a channel post to its linked-supergroup thread and
// seeds that thread into the same message store used by chat projections.
func (o *Owner) OpenDiscussion(ctx context.Context, sourceChatID int64, msgID int, discussionChatID int64) (domain.Discussion, error) {
	source, ok := o.state.Store().GetChat(sourceChatID)
	if !ok {
		return domain.Discussion{}, &telerr.Error{Kind: telerr.NotFound, Op: "open discussion", Detail: "source chat not found"}
	}
	discussion, err := o.client.GetDiscussion(ctx, source.Peer, msgID, discussionChatID)
	if err != nil {
		return domain.Discussion{}, err
	}
	if existingChat, ok := o.state.Store().GetChat(discussion.Chat.ID); ok {
		existingChat.Title = discussion.Chat.Title
		existingChat.Peer = discussion.Chat.Peer
		discussion.Chat = existingChat
	}
	existing := o.state.Store().Messages(discussion.Chat.ID)
	merged := mergeHistoryRanges(discussion.Messages, existing)
	o.state.ApplyHistory(discussion.Chat.ID, merged)
	return discussion, nil
}

func (o *Owner) MarkDiscussionRead(ctx context.Context, peer domain.Peer, rootMsgID, maxID int) error {
	return o.client.MarkDiscussionRead(ctx, peer, rootMsgID, maxID)
}

// JoinDiscussionAndRetry is the explicit recovery for a guest comment Telegram
// refused. It validates that ref is a failed send in this discussion, joins,
// and only then requeues the same idempotent send.
func (o *Owner) JoinDiscussionAndRetry(ctx context.Context, chatID int64, ref string) error {
	if o.outbox == nil {
		return &telerr.Error{Kind: telerr.Internal, Op: "join discussion", Detail: "no outbox configured"}
	}
	entry, ok := o.outbox.Get(ref)
	if !ok || entry.ChatID != chatID || entry.State != domain.OutboxFailed || outboxThreadRoot(entry) == 0 {
		return &telerr.Error{Kind: telerr.NotFound, Op: "join discussion", Detail: "failed discussion send not found"}
	}
	if entry.ErrReason != telerr.ReasonGuestSendForbidden {
		return &telerr.Error{Kind: telerr.Forbidden, Op: "join discussion", Detail: "send does not require membership"}
	}
	peer, err := o.outboxPeer(entry)
	if err != nil {
		return err
	}
	if err := o.client.JoinChannel(ctx, peer); err != nil {
		return err
	}
	return o.RetryOutbox(ref)
}

func outboxThreadRoot(entry domain.OutboxEntry) int {
	if entry.Message != nil {
		return entry.Message.ThreadRootID
	}
	if entry.Media != nil {
		return entry.Media.ThreadRootID
	}
	return 0
}
