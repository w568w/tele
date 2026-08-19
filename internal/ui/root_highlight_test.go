package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	"github.com/sorokin-vladimir/tele/internal/ui/components"
	"github.com/sorokin-vladimir/tele/internal/ui/screens"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rootOnOpenChatWithMsg builds a root on an open chat (id 1) holding one
// incoming message with the given id, ready for jump-to-highlight tests.
func rootOnOpenChatWithMsg(t *testing.T, msgID int) RootModel {
	t.Helper()
	st := store.NewMemory()
	st.SetChat(domain.Chat{ID: 1, Title: "Alice", Peer: domain.Peer{ID: 1, Type: domain.PeerUser}})
	m := newRootInternal(st, 50)
	m = m.WithScreen(ScreenMain)
	newM, _ := m.Update(screens.OpenChatMsg{ChatID: 1, Title: "Alice"})
	m = newM.(RootModel)
	newM, _ = applyEventInternal(t, m, st, store.Event{Kind: store.EventNewMessage,
		Message: domain.Message{ID: msgID, ChatID: 1, Text: "target", Date: time.Now()}})
	return newM.(RootModel)
}

func TestRoot_JumpToMsg_StartsHighlight(t *testing.T) {
	m := rootOnOpenChatWithMsg(t, 5)

	newM, cmd := m.Update(components.JumpToMsgRequest{MsgID: 5})
	root := newM.(RootModel)

	assert.Equal(t, 5, root.Chat().HighlightedMsgID())
	assert.Equal(t, components.HighlightInitialStep, root.Chat().HighlightStep())
	require.NotNil(t, cmd, "a fade tick command should be scheduled")
}

func TestRoot_JumpToMsg_LoadsAnOutOfBufferAnchorThenHighlightsIt(t *testing.T) {
	st := store.NewMemory()
	st.SetChat(domain.Chat{ID: 1, Title: "Alice", Peer: domain.Peer{ID: 1, Type: domain.PeerUser}})
	m := newRootInternal(st, 50).WithScreen(ScreenMain)
	newM, _ := m.Update(screens.OpenChatMsg{ChatID: 1, Title: "Alice"})
	m = newM.(RootModel)
	newM, _ = applyEventInternal(t, m, st, store.Event{Kind: store.EventNewMessage,
		Message: domain.Message{ID: 100, ChatID: 1, Text: "reply", ReplyToMsgID: 1, Date: time.Now()}})
	m = newM.(RootModel)

	newM, _ = m.Update(components.JumpToMsgRequest{MsgID: 1})
	m = newM.(RootModel)
	w, ok := m.owner.(*ownerStub).reg.Window(m.chatSub)
	require.True(t, ok)
	assert.Equal(t, project.Anchor{Kind: project.AnchorMessage, MsgID: 1}, w.(project.ChatWindow).Anchor)

	m.owner.(*ownerStub).state.ApplyHistory(1, []domain.Message{
		{ID: 1, ChatID: 1, Text: "target", Date: time.Unix(1, 0)},
		{ID: 100, ChatID: 1, Text: "reply", ReplyToMsgID: 1, Date: time.Unix(100, 0)},
	})
	newM, cmd := m.owner.(*ownerStub).drain(m)
	m = newM.(RootModel)

	assert.Equal(t, 1, m.Chat().SelectedMessageID())
	assert.Equal(t, 1, m.Chat().HighlightedMsgID())
	assert.NotNil(t, cmd)

	after := m.chatWindow.After
	newM, _ = m.Update(screens.LoadNewerMsg{ChatID: 1, OffsetID: 1})
	m = newM.(RootModel)
	assert.Equal(t, after+50, m.chatWindow.After)
}

func TestRoot_MsgHighlightFade_DecrementsOnTick(t *testing.T) {
	m := rootOnOpenChatWithMsg(t, 5)
	newM, _ := m.Update(components.JumpToMsgRequest{MsgID: 5})
	m = newM.(RootModel)
	require.Equal(t, components.HighlightInitialStep, m.Chat().HighlightStep())

	// One fade tick (matching serial) decrements the step.
	newM, cmd := m.Update(msgHighlightFadeMsg{serial: m.msgHighlightSerial})
	m = newM.(RootModel)
	assert.Equal(t, components.HighlightInitialStep-1, m.Chat().HighlightStep())
	assert.NotNil(t, cmd, "still active, so another tick is scheduled")
}

func TestRoot_MsgHighlightFade_StaleSerialIgnored(t *testing.T) {
	m := rootOnOpenChatWithMsg(t, 5)
	newM, _ := m.Update(components.JumpToMsgRequest{MsgID: 5})
	m = newM.(RootModel)
	before := m.Chat().HighlightStep()

	// A tick from a superseded highlight (wrong serial) must not decrement.
	newM, cmd := m.Update(msgHighlightFadeMsg{serial: m.msgHighlightSerial - 1})
	m = newM.(RootModel)
	assert.Equal(t, before, m.Chat().HighlightStep())
	assert.Nil(t, cmd)
}

func TestRoot_IncomingMsg_HighlightsNonOpenChat(t *testing.T) {
	m, st := newRootWithTwoChatsInternal(t)

	newM, cmd := applyEventInternal(t, m, st, store.Event{Kind: store.EventNewMessage,
		Message: domain.Message{ID: 1, ChatID: 2, Text: "hi", Date: time.Now()}})
	root := newM.(RootModel)

	assert.Equal(t, int64(2), root.ChatList().HighlightedChatID())
	assert.Equal(t, components.HighlightInitialStep, root.ChatList().HighlightStep())
	require.NotNil(t, cmd, "a chat fade tick should be scheduled")
}

func TestRoot_IncomingMsg_NoHighlightForOpenChat(t *testing.T) {
	st := store.NewMemory()
	st.SetChat(domain.Chat{ID: 1, Title: "Alice", Peer: domain.Peer{ID: 1, Type: domain.PeerUser}})
	m := newRootInternal(st, 50)
	m = m.WithScreen(ScreenMain)
	newM, _ := m.Update(screens.OpenChatMsg{ChatID: 1, Title: "Alice"})
	m = newM.(RootModel)

	newM, _ = applyEventInternal(t, m, st, store.Event{Kind: store.EventNewMessage,
		Message: domain.Message{ID: 9, ChatID: 1, Text: "hi", Date: time.Now()}})
	root := newM.(RootModel)

	assert.Equal(t, int64(0), root.ChatList().HighlightedChatID(),
		"the currently open chat must not be highlighted")
}

func TestRoot_IncomingMsg_NoHighlightForOutgoing(t *testing.T) {
	m, st := newRootWithTwoChatsInternal(t)

	newM, _ := applyEventInternal(t, m, st, store.Event{Kind: store.EventNewMessage,
		Message: domain.Message{ID: 1, ChatID: 2, Text: "hi", IsOut: true, Date: time.Now()}})
	root := newM.(RootModel)

	assert.Equal(t, int64(0), root.ChatList().HighlightedChatID(),
		"an outgoing message must not highlight the chat row")
}

func TestRoot_ChatHighlightFade_DecrementsAndStaleIgnored(t *testing.T) {
	m, st := newRootWithTwoChatsInternal(t)
	newM, _ := applyEventInternal(t, m, st, store.Event{Kind: store.EventNewMessage,
		Message: domain.Message{ID: 1, ChatID: 2, Text: "hi", Date: time.Now()}})
	m = newM.(RootModel)

	// Matching serial decrements.
	newM, cmd := m.Update(chatHighlightFadeMsg{serial: m.chatHighlightSerial})
	m = newM.(RootModel)
	assert.Equal(t, components.HighlightInitialStep-1, m.ChatList().HighlightStep())
	assert.NotNil(t, cmd)

	// Stale serial is ignored.
	before := m.ChatList().HighlightStep()
	newM, cmd = m.Update(chatHighlightFadeMsg{serial: m.chatHighlightSerial - 1})
	m = newM.(RootModel)
	assert.Equal(t, before, m.ChatList().HighlightStep())
	assert.Nil(t, cmd)
}

// drainBatch expands a tea.BatchMsg into the messages its commands produce; a
// non-batch message is returned as a single-element slice.
func drainBatch(msg tea.Msg) []tea.Msg {
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, c())
	}
	return out
}

func TestRoot_DeleteMsgFailed_StartsErrorHighlight(t *testing.T) {
	m := rootOnOpenChatWithMsg(t, 5) // open chat is 1

	newM, cmd := m.Update(deleteMsgFailedMsg{
		chatID: 1, msgID: 5, err: &telerr.Error{Kind: telerr.Forbidden},
	})
	root := newM.(RootModel)

	assert.Equal(t, 5, root.Chat().HighlightedMsgID())
	assert.Equal(t, components.HighlightError, root.Chat().HighlightKind())
	assert.Equal(t, components.HighlightInitialStep, root.Chat().HighlightStep())
	require.NotNil(t, cmd)

	var sawToast bool
	for _, mm := range drainBatch(cmd()) {
		if _, ok := mm.(StatusErrMsg); ok {
			sawToast = true
		}
	}
	assert.True(t, sawToast, "the failure toast must still be emitted alongside the highlight")
}

func TestRoot_EditMsgFailed_StartsErrorHighlight(t *testing.T) {
	m := rootOnOpenChatWithMsg(t, 7)

	newM, cmd := m.Update(editMsgFailedMsg{
		chatID: 1, msgID: 7, err: &telerr.Error{Kind: telerr.Forbidden},
	})
	root := newM.(RootModel)

	assert.Equal(t, 7, root.Chat().HighlightedMsgID())
	assert.Equal(t, components.HighlightError, root.Chat().HighlightKind())
	require.NotNil(t, cmd)
}

func TestRoot_MsgFailed_NoHighlightForOtherChat(t *testing.T) {
	m := rootOnOpenChatWithMsg(t, 5) // open chat is 1

	newM, cmd := m.Update(deleteMsgFailedMsg{
		chatID: 2, msgID: 5, err: &telerr.Error{Kind: telerr.Forbidden},
	})
	root := newM.(RootModel)

	assert.Equal(t, 0, root.Chat().HighlightedMsgID(),
		"a rollback in another chat must not highlight the open chat")
	require.NotNil(t, cmd, "the failure toast is still emitted")
}

// newRootWithTwoChatsInternal mirrors newRootWithTwoChats from root_test.go for
// use inside package ui (where that external helper is not visible).
func newRootWithTwoChatsInternal(t *testing.T) (RootModel, store.Store) {
	t.Helper()
	st := store.NewMemory()
	st.SetChat(domain.Chat{ID: 1, Title: "Alice"})
	st.SetChat(domain.Chat{ID: 2, Title: "Bob"})
	m := newRootInternal(st, 50)
	m = m.WithScreen(ScreenMain)
	newM, _ := m.Update(screens.TransitionToMainMsg{})
	return newM.(RootModel), st
}
