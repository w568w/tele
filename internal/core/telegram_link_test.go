package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/domain"
)

func TestParseTelegramNavigationLink(t *testing.T) {
	tests := []struct {
		url              string
		username         string
		channelID        int64
		msgID, commentID int
	}{
		{"https://t.me/freexzteam_bot", "freexzteam_bot", 0, 0, 0},
		{"https://t.me/archlinuxcn_group/3784385", "archlinuxcn_group", 0, 3784385, 0},
		{"https://t.me/s/archlinuxcn_group/3784385", "archlinuxcn_group", 0, 3784385, 0},
		{"https://t.me/c/12345/678?single", "", 12345, 678, 0},
		{"https://archlinuxcn_group.t.me/3784385?comment=77", "archlinuxcn_group", 0, 3784385, 77},
		{"tg://resolve?domain=archlinuxcn_group&post=3784385", "archlinuxcn_group", 0, 3784385, 0},
		{"tg:privatepost?channel=12345&post=678&comment=77", "", 12345, 678, 77},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got, ok := ParseTelegramLink(tt.url)
			require.True(t, ok)
			assert.Equal(t, TelegramLink{
				Username: tt.username, ChannelID: tt.channelID, MsgID: tt.msgID, CommentID: tt.commentID,
			}, got)
		})
	}
}

func TestParseTelegramNavigationLink_LeavesUnsupportedLinksExternal(t *testing.T) {
	for _, raw := range []string{
		"https://example.com/x",
		"https://t.me/joinchat/secret",
		"https://t.me/addstickers/pack",
		"https://t.me/a_bot?start=token",
		"https://t.me/forum/10/20",
		"https://t.me/channel/4?t=20",
		"tg://proxy?server=example.com",
		"https://t.me/c/nope/3",
	} {
		t.Run(raw, func(t *testing.T) {
			_, ok := ParseTelegramLink(raw)
			assert.False(t, ok)
		})
	}
}

func TestResolveTelegramLink_RemembersTransientPeerWithoutPersistingDialog(t *testing.T) {
	chat := domain.Chat{
		ID: 99, Title: "Arch Linux CN",
		Peer: domain.Peer{ID: 99, Type: domain.PeerSuperGroup, AccessHash: 7},
	}
	c := &stubClient{resolvedChat: chat}
	o, st := newCmdOwner(t, c)

	link, ok := ParseTelegramLink("https://t.me/archlinuxcn_group/42")
	require.True(t, ok)
	got, err := o.ResolveTelegramLink(context.Background(), link)

	require.NoError(t, err)
	assert.Equal(t, "archlinuxcn_group", c.resolvedName)
	assert.Equal(t, 42, got.MsgID)
	assert.Equal(t, chat.Peer, got.Peer)
	_, persisted := st.GetChat(chat.ID)
	assert.False(t, persisted)
	resolved, ok := o.reader().GetChat(chat.ID)
	require.True(t, ok)
	assert.Equal(t, chat.Title, resolved.Title)
}
