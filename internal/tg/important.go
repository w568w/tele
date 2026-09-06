package tg

import (
	"context"

	"github.com/gotd/td/tg"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

func (c *GotdClient) GetLinkedGroup(ctx context.Context, peer domain.Peer) (domain.Chat, error) {
	api, err := c.acquireAPI()
	if err != nil {
		return domain.Chat{}, err
	}
	full, err := api.ChannelsGetFullChannel(ctx, inputChannel(peer))
	if err != nil {
		return domain.Chat{}, err
	}
	channel, ok := full.FullChat.(*tg.ChannelFull)
	if !ok || channel.LinkedChatID == 0 {
		return domain.Chat{}, &telerr.Error{Kind: telerr.NotFound, Op: "open discussion group", Detail: "channel has no linked discussion group"}
	}
	chat, ok := resolvedChat(&tg.PeerChannel{ChannelID: channel.LinkedChatID}, full.Users, full.Chats)
	if !ok {
		chat, err = c.ResolveChannel(ctx, channel.LinkedChatID)
		if err != nil {
			return domain.Chat{}, err
		}
	}
	// Verify access before changing pages, without joining the linked group.
	if _, err := api.ChannelsGetFullChannel(ctx, inputChannel(chat.Peer)); err != nil {
		return domain.Chat{}, err
	}
	c.cachePeer(chat.Peer)
	return chat, nil
}

func (c *GotdClient) GetUnreadImportant(ctx context.Context, peer domain.Peer, kind domain.ImportantKind, offsetID int) (domain.UnreadPage, error) {
	api, err := c.acquireAPI()
	if err != nil {
		return domain.UnreadPage{}, err
	}
	var result tg.MessagesMessagesClass
	if kind == domain.ImportantMention {
		result, err = api.MessagesGetUnreadMentions(ctx, &tg.MessagesGetUnreadMentionsRequest{Peer: peerToInput(peer), OffsetID: offsetID, Limit: 100})
	} else {
		result, err = api.MessagesGetUnreadReactions(ctx, &tg.MessagesGetUnreadReactionsRequest{Peer: peerToInput(peer), OffsetID: offsetID, Limit: 100})
	}
	if err != nil {
		return domain.UnreadPage{}, err
	}
	page := domain.UnreadPage{Messages: parseHistory(result, peer.ID)}
	switch result := result.(type) {
	case *tg.MessagesMessages:
		page.Count = len(result.Messages)
	case *tg.MessagesMessagesSlice:
		page.Count = result.Count
	case *tg.MessagesChannelMessages:
		page.Count = result.Count
	default:
		return domain.UnreadPage{}, &telerr.Error{Kind: telerr.Internal, Op: "get important unread", Detail: "unexpected unread list response"}
	}
	return page, nil
}

func (c *GotdClient) ReadImportantContents(ctx context.Context, peer domain.Peer, ids []int) error {
	api, err := c.acquireAPI()
	if err != nil {
		return err
	}
	if isChannelPeer(peer) {
		var read bool
		read, err = api.ChannelsReadMessageContents(ctx, &tg.ChannelsReadMessageContentsRequest{Channel: inputChannel(peer), ID: ids})
		if err == nil && !read {
			return &telerr.Error{Kind: telerr.Rejected, Op: "read important contents", Detail: "server did not confirm content read"}
		}
	} else {
		_, err = api.MessagesReadMessageContents(ctx, ids)
	}
	return err
}
