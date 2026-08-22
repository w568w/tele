package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/domain"
)

func TestOpenDiscussion_StoresTheThreadWithoutCreatingADialog(t *testing.T) {
	peer := domain.Peer{ID: 2, Type: domain.PeerSuperGroup, AccessHash: 22}
	c := &stubClient{discussion: domain.Discussion{
		Chat:      domain.Chat{ID: 2, Title: "Linked group", Peer: peer},
		RootMsgID: 40,
		Messages:  []domain.Message{{ID: 40, ChatID: 2, ThreadRootID: 40}},
	}}
	o, st := newCmdOwner(t, c)

	got, err := o.OpenDiscussion(context.Background(), 1, 7, 2)

	require.NoError(t, err)
	assert.Equal(t, peer, got.Chat.Peer)
	_, exists := st.GetChat(2)
	assert.False(t, exists, "a linked discussion is not an account dialog")
	require.Len(t, st.Messages(2), 1)
	assert.Equal(t, 40, st.Messages(2)[0].ID)
}
