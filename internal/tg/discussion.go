package tg

import (
	"context"
	"sort"

	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// JoinChannel joins only after the UI has confirmed the recovery from
// CHAT_GUEST_SEND_FORBIDDEN. Reading a discussion never calls this method.
func (c *GotdClient) JoinChannel(ctx context.Context, peer domain.Peer) error {
	api, err := c.acquireAPI()
	if err != nil {
		return err
	}
	return WithRetry(ctx, func() error {
		_, err := api.ChannelsJoinChannel(ctx, inputChannel(peer))
		return err
	})
}

func (c *GotdClient) GetDiscussion(ctx context.Context, peer domain.Peer, msgID int, discussionChatID int64) (domain.Discussion, error) {
	api, err := c.acquireAPI()
	if err != nil {
		return domain.Discussion{}, err
	}

	var discussion domain.Discussion
	err = WithRetry(ctx, func() error {
		result, err := api.MessagesGetDiscussionMessage(ctx, &tg.MessagesGetDiscussionMessageRequest{
			Peer:  peerToInput(peer),
			MsgID: msgID,
		})
		if err != nil {
			c.log.Error("MessagesGetDiscussionMessage failed", zap.Error(err))
			return err
		}
		discussion, err = parseDiscussion(result, peer.ID, discussionChatID)
		if err != nil {
			return err
		}
		c.hydrateCustomReactions(ctx, api, discussion.Messages)
		c.cachePeer(discussion.Chat.Peer)
		return nil
	})
	return discussion, err
}

func parseDiscussion(result *tg.MessagesDiscussionMessage, sourceChatID, discussionChatID int64) (domain.Discussion, error) {
	if result == nil {
		return domain.Discussion{}, &telerr.Error{Kind: telerr.NotFound, Op: "get discussion", Detail: "empty result"}
	}

	chats := make(map[int64]domain.Chat)
	for _, raw := range result.Chats {
		if ch, ok := raw.(*tg.Channel); ok {
			if converted, ok := convertChannel(ch); ok {
				chats[converted.ID] = converted
			}
		}
	}
	if discussionChatID == 0 {
		for id, chat := range chats {
			if id != sourceChatID && chat.Peer.IsSuperGroup() {
				discussionChatID = id
				break
			}
		}
	}
	if discussionChatID == 0 {
		for _, raw := range result.Messages {
			if id := extractPeerID(raw); id != 0 && id != sourceChatID {
				discussionChatID = id
				break
			}
		}
	}
	chat, ok := chats[discussionChatID]
	if !ok {
		return domain.Discussion{}, &telerr.Error{Kind: telerr.NotFound, Op: "get discussion", Detail: "linked group not returned"}
	}

	rawMessages := make([]tg.MessageClass, 0, len(result.Messages))
	for _, raw := range result.Messages {
		if extractPeerID(raw) == discussionChatID {
			rawMessages = append(rawMessages, raw)
		}
	}
	parsed := parseHistory(&tg.MessagesMessages{
		Messages: rawMessages,
		Chats:    result.Chats,
		Users:    result.Users,
	}, discussionChatID)
	if len(parsed) == 0 {
		return domain.Discussion{}, &telerr.Error{Kind: telerr.NotFound, Op: "get discussion", Detail: "thread root not returned"}
	}
	sort.SliceStable(parsed, func(i, j int) bool { return parsed[i].ID < parsed[j].ID })
	rootID := parsed[0].ID
	for i := range parsed {
		parsed[i].ThreadRootID = rootID
	}
	parsed[0].ReplyToMsgID = 0
	parsed[0].ReplyPreview = nil

	return domain.Discussion{
		Chat:            chat,
		RootMsgID:       rootID,
		Messages:        parsed,
		ReadInboxMaxID:  result.ReadInboxMaxID,
		ReadOutboxMaxID: result.ReadOutboxMaxID,
	}, nil
}

func (c *GotdClient) GetReplies(ctx context.Context, peer domain.Peer, rootMsgID, offsetID, limit int) ([]domain.Message, error) {
	return c.getReplies(ctx, peer, rootMsgID, &tg.MessagesGetRepliesRequest{
		Peer:     peerToInput(peer),
		MsgID:    rootMsgID,
		OffsetID: offsetID,
		Limit:    limit,
	})
}

func (c *GotdClient) GetRepliesWindow(ctx context.Context, peer domain.Peer, rootMsgID, anchorID, before, after int) ([]domain.Message, error) {
	return c.getReplies(ctx, peer, rootMsgID, &tg.MessagesGetRepliesRequest{
		Peer:      peerToInput(peer),
		MsgID:     rootMsgID,
		OffsetID:  anchorID,
		AddOffset: -(after + 1),
		Limit:     before + after + 1,
	})
}

func (c *GotdClient) getReplies(ctx context.Context, peer domain.Peer, rootMsgID int, req *tg.MessagesGetRepliesRequest) ([]domain.Message, error) {
	api, err := c.acquireAPI()
	if err != nil {
		return nil, err
	}
	var msgs []domain.Message
	err = WithRetry(ctx, func() error {
		result, err := api.MessagesGetReplies(ctx, req)
		if err != nil {
			c.log.Error("MessagesGetReplies failed", zap.Error(err))
			return err
		}
		msgs = parseHistory(result, peer.ID)
		for i := range msgs {
			msgs[i].ThreadRootID = rootMsgID
			if msgs[i].ID == rootMsgID {
				msgs[i].ReplyToMsgID = 0
				msgs[i].ReplyPreview = nil
			}
		}
		c.hydrateCustomReactions(ctx, api, msgs)
		return nil
	})
	return msgs, err
}

func (c *GotdClient) MarkDiscussionRead(ctx context.Context, peer domain.Peer, rootMsgID, maxID int) error {
	api, err := c.acquireAPI()
	if err != nil {
		return err
	}
	return WithRetry(ctx, func() error {
		_, err := api.MessagesReadDiscussion(ctx, &tg.MessagesReadDiscussionRequest{
			Peer:      peerToInput(peer),
			MsgID:     rootMsgID,
			ReadMaxID: maxID,
		})
		return err
	})
}
