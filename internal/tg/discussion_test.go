package tg

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDiscussion_UsesLinkedSupergroupRoot(t *testing.T) {
	result := &tg.MessagesDiscussionMessage{
		Messages: []tg.MessageClass{
			&tg.Message{ID: 11, PeerID: &tg.PeerChannel{ChannelID: 200}, Date: 1700000000, Message: "root"},
			&tg.Message{ID: 7, PeerID: &tg.PeerChannel{ChannelID: 100}, Date: 1700000000, Message: "source"},
		},
		Chats: []tg.ChatClass{
			&tg.Channel{ID: 100, Title: "News", AccessHash: 1},
			&tg.Channel{ID: 200, Title: "News chat", AccessHash: 2, Megagroup: true},
		},
		ReadInboxMaxID:  15,
		ReadOutboxMaxID: 14,
	}

	got, err := parseDiscussion(result, 100, 200)
	require.NoError(t, err)
	assert.Equal(t, int64(200), got.Chat.ID)
	assert.True(t, got.Chat.Peer.IsSuperGroup())
	assert.Equal(t, 11, got.RootMsgID)
	require.Len(t, got.Messages, 1)
	assert.Equal(t, 11, got.Messages[0].ThreadRootID)
	assert.Equal(t, 15, got.ReadInboxMaxID)
}
