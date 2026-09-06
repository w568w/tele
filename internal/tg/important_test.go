package tg

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type importantInvoker func(bin.Encoder) (bin.Encoder, error)

func (f importantInvoker) Invoke(_ context.Context, in bin.Encoder, out bin.Decoder) error {
	result, err := f(in)
	if err != nil {
		return err
	}
	var buffer bin.Buffer
	if err := result.Encode(&buffer); err != nil {
		return err
	}
	return out.Decode(&buffer)
}

func TestImportantUnreadRequestsUse100AndNoForumFilter(t *testing.T) {
	c := testClient()
	c.api = tg.NewClient(importantInvoker(func(in bin.Encoder) (bin.Encoder, error) {
		switch req := in.(type) {
		case *tg.MessagesGetUnreadMentionsRequest:
			assert.Equal(t, 100, req.Limit)
			assert.Equal(t, 50, req.OffsetID)
			assert.Zero(t, req.TopMsgID)
		case *tg.MessagesGetUnreadReactionsRequest:
			assert.Equal(t, 100, req.Limit)
			assert.Equal(t, 50, req.OffsetID)
			assert.Zero(t, req.TopMsgID)
		default:
			t.Fatalf("unexpected RPC %T", in)
		}
		return &tg.MessagesMessagesSlice{Count: 1, Messages: []tg.MessageClass{&tg.Message{
			ID: 40, PeerID: &tg.PeerChannel{ChannelID: 1}, Mentioned: true, MediaUnread: true,
			ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 20, ReplyToTopID: 5},
		}}}, nil
	}))
	for _, kind := range []domain.ImportantKind{domain.ImportantMention, domain.ImportantReaction} {
		page, err := c.GetUnreadImportant(context.Background(), domain.Peer{ID: 1, Type: domain.PeerSuperGroup}, kind, 50)
		require.NoError(t, err)
		require.Len(t, page.Messages, 1)
		assert.True(t, page.Messages[0].MediaUnread)
		assert.Equal(t, 5, page.Messages[0].ThreadRootID)
	}
}

func TestImportantReadContentsRPCIsPeerScoped(t *testing.T) {
	c := testClient()
	c.api = tg.NewClient(importantInvoker(func(in bin.Encoder) (bin.Encoder, error) {
		switch req := in.(type) {
		case *tg.MessagesReadMessageContentsRequest:
			assert.Equal(t, []int{5, 9}, req.ID)
			return &tg.MessagesAffectedMessages{}, nil
		case *tg.ChannelsReadMessageContentsRequest:
			assert.Equal(t, []int{5, 9}, req.ID)
			assert.Equal(t, int64(7), req.Channel.(*tg.InputChannel).ChannelID)
			return &tg.BoolTrue{}, nil
		default:
			t.Fatalf("unexpected whole-chat RPC %T", in)
			return nil, nil
		}
	}))
	for _, kind := range []domain.PeerType{domain.PeerUser, domain.PeerSuperGroup} {
		require.NoError(t, c.ReadImportantContents(context.Background(), domain.Peer{ID: 7, Type: kind}, []int{5, 9}))
	}
}

func TestImportantLinkedGroupChecksAccessWithoutJoining(t *testing.T) {
	for _, denied := range []bool{false, true} {
		c := testClient()
		c.peers = make(map[int64]domain.Peer)
		calls := 0
		c.api = tg.NewClient(importantInvoker(func(in bin.Encoder) (bin.Encoder, error) {
			req, ok := in.(*tg.ChannelsGetFullChannelRequest)
			require.True(t, ok, "must not join or read history")
			calls++
			id := req.Channel.(*tg.InputChannel).ChannelID
			if id == 2 && denied {
				return nil, errors.New("CHANNEL_PRIVATE")
			}
			full := &tg.ChannelFull{ID: id, ChatPhoto: &tg.PhotoEmpty{}, NotifySettings: tg.PeerNotifySettings{}}
			if id == 1 {
				full.SetLinkedChatID(2)
			}
			return &tg.MessagesChatFull{FullChat: full, Chats: []tg.ChatClass{&tg.Channel{ID: 2, Megagroup: true, AccessHash: 22, Photo: &tg.ChatPhotoEmpty{}, Title: "Linked"}}}, nil
		}))
		chat, err := c.GetLinkedGroup(context.Background(), domain.Peer{ID: 1, Type: domain.PeerChannel})
		if denied {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			assert.Equal(t, int64(2), chat.ID)
		}
		assert.Equal(t, 2, calls)
	}
}
