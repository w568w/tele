package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	"github.com/sorokin-vladimir/tele/internal/ui/components"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
)

func TestThreadSendAddressesRootAndSelectedReply(t *testing.T) {
	peer := domain.Peer{ID: 200, Type: domain.PeerSuperGroup, AccessHash: 20}
	st := store.NewMemory()
	m := newRootInternal(st, 20)
	m.currentChatID = 200
	m.discussion = &discussionNav{rootMsgID: 40, peer: peer}

	next, cmd := m.handleSendMsg(screens.SendMsgRequest{Text: "nested", ReplyToMsgID: 45})
	require.NotNil(t, cmd)
	assert.Nil(t, cmd())
	o := next.owner.(*ownerStub)
	require.Len(t, o.sent, 1)
	assert.Equal(t, 45, o.sent[0].ReplyToMsgID)
	assert.Equal(t, 40, o.sent[0].ThreadRootID)
	assert.Equal(t, peer, o.sent[0].Peer)
}

func TestThreadSendWithoutSelectionRepliesToRoot(t *testing.T) {
	peer := domain.Peer{ID: 200, Type: domain.PeerSuperGroup, AccessHash: 20}
	st := store.NewMemory()
	m := newRootInternal(st, 20)
	m.currentChatID = 200
	m.discussion = &discussionNav{rootMsgID: 40, peer: peer}

	next, cmd := m.handleSendMsg(screens.SendMsgRequest{Text: "comment"})
	require.NotNil(t, cmd)
	assert.Nil(t, cmd())
	o := next.owner.(*ownerStub)
	require.Len(t, o.sent, 1)
	assert.Equal(t, 40, o.sent[0].ReplyToMsgID)
	assert.Equal(t, 40, o.sent[0].ThreadRootID)
	assert.Equal(t, peer, o.sent[0].Peer)
}

func TestApplyDiscussionOpenedCarriesPeerWithoutCreatingAChat(t *testing.T) {
	st := store.NewMemory()
	source := domain.Chat{ID: 100, Title: "Channel", Peer: domain.Peer{ID: 100, Type: domain.PeerChannel}}
	st.SetChat(source)
	m := newRootInternal(st, 20)
	m.currentChatID = 100
	peer := domain.Peer{ID: 200, Type: domain.PeerSuperGroup, AccessHash: 20}

	next, _ := m.applyDiscussionOpened(discussionOpenedMsg{
		sourceChatID: 100,
		sourceMsgID:  7,
		discussion: domain.Discussion{
			Chat: domain.Chat{ID: 200, Title: "Linked group", Peer: peer}, RootMsgID: 40,
		},
	})

	assert.Equal(t, peer, next.chatWindow.ThreadPeer)
	assert.Equal(t, peer, next.discussion.peer)
	_, exists := st.GetChat(200)
	assert.False(t, exists)
}

func TestGuestSendForbidden_PromptsBeforeJoiningAndRetriesOnlyAfterConfirm(t *testing.T) {
	st := store.NewMemory()
	m := newRootInternal(st, 20)
	m.currentChatID = 200
	m.discussion = &discussionNav{rootMsgID: 40}
	failure := core.Failure{
		ChatID: 200,
		Ref:    "comment-1",
		Op:     core.OpSend,
		Err: &telerr.Error{
			Kind: telerr.Forbidden, Reason: telerr.ReasonGuestSendForbidden,
		},
	}

	next, cmd := m.handleFailure(failure)
	require.Nil(t, cmd)
	require.NotNil(t, next.joinPrompt)
	o := next.owner.(*ownerStub)
	assert.Empty(t, o.joined, "showing the prompt must not join")

	confirmed, cmd := next.handleJoinDiscussionPromptKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.Nil(t, confirmed.(RootModel).joinPrompt)
	result, ok := cmd().(joinDiscussionRetryMsg)
	require.True(t, ok)
	assert.NoError(t, result.err)
	assert.Equal(t, []string{"comment-1"}, o.joined)
}

func TestGuestSendForbidden_CancelNeverJoins(t *testing.T) {
	st := store.NewMemory()
	m := newRootInternal(st, 20)
	m.currentChatID = 200
	m.discussion = &discussionNav{rootMsgID: 40}
	m.joinPrompt = &joinDiscussionPrompt{chatID: 200, ref: "comment-1"}
	o := m.owner.(*ownerStub)

	next, cmd := m.handleJoinDiscussionPromptKey(tea.KeyPressMsg{Code: tea.KeyEsc})

	require.Nil(t, cmd)
	assert.Nil(t, next.(RootModel).joinPrompt)
	assert.Empty(t, o.joined)
}

func TestOpenDiscussionRequest_IsRoutedToTheAsyncOwnerCall(t *testing.T) {
	st := store.NewMemory()
	st.SetChat(domain.Chat{ID: 100, Title: "Channel", Peer: domain.Peer{ID: 100, Type: domain.PeerChannel}})
	m := newRootInternal(st, 20)
	m.currentChatID = 100

	next, cmd := m.Update(components.OpenDiscussionRequest{MsgID: 7, DiscussionChatID: 200})

	require.NotNil(t, cmd)
	assert.True(t, next.(RootModel).chat.IsLoading())
}
