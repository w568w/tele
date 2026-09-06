package ui

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
	"github.com/sorokin-vladimir/tele/internal/ui/components"
	"github.com/sorokin-vladimir/tele/internal/ui/keys"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type importantUIOwner struct {
	*ownerStub
	group        domain.Chat
	err          error
	before, root int
	visible      []int
}

func (o *importantUIOwner) LinkedDiscussionGroup(context.Context, int64) (domain.Chat, error) {
	return o.group, o.err
}
func (o *importantUIOwner) RefreshImportant(context.Context, int64) error { return o.err }
func (o *importantUIOwner) PreviousImportant(_ context.Context, _ int64, root, before int) (int, error) {
	o.root, o.before = root, before
	return 5, o.err
}
func (o *importantUIOwner) ReadVisibleImportant(_ context.Context, _ int64, root int, ids []int) error {
	o.root, o.visible = root, ids
	return o.err
}

func importantUIFixture(t *testing.T) (RootModel, *importantUIOwner) {
	t.Helper()
	st := store.NewMemory()
	chat := domain.Chat{ID: 1, Title: "Channel", Peer: domain.Peer{ID: 1, Type: domain.PeerChannel}}
	st.SetChat(chat)
	o := &importantUIOwner{ownerStub: newOwnerStub(st), group: domain.Chat{ID: 2, Title: "Discussion", Peer: domain.Peer{ID: 2, Type: domain.PeerSuperGroup}}}
	m := NewRootModel(st, 20, false).WithOwner(o).WithScreen(ScreenMain)
	m, _ = m.activatePage(project.ChatWindow{ChatID: 1, Peer: chat.Peer, Title: chat.Title, Before: 20}, 1, 0, false)
	m.chat.SetSize(70, 25)
	m.chat.SetMessages([]domain.Message{{ID: 20, ChatID: 1, Text: "visible"}})
	m.chat.SetLoading(false)
	m.importantUnread = domain.ImportantUnread{Mentions: 1, Revision: 1}
	_ = m.chat.View()
	return m, o
}

func TestImportantLinkedGroupNavigationAndReturn(t *testing.T) {
	m, _ := importantUIFixture(t)
	source := m.snapshotPage()
	chat, _ := m.st.GetChat(1)
	m, cmd := m.openLinkedGroup(chat)
	require.NotNil(t, cmd)
	m, _ = m.applyLinkedGroup(cmd().(linkedGroupReadyMsg))
	assert.Equal(t, int64(2), m.currentChatID)
	assert.Zero(t, m.chatWindow.ThreadRootID, "open whole group, not Comments")
	require.Len(t, m.pageHistory, 1)
	m, _ = m.popPage()
	assert.Equal(t, source.window, m.chatWindow)
	assert.Equal(t, source.selectedMsgID, m.pendingJumpMsgID)
}

func TestImportantLinkedGroupFailureAndStaleResult(t *testing.T) {
	m, o := importantUIFixture(t)
	chat, _ := m.st.GetChat(1)
	o.err = errors.New("private group")
	m, cmd := m.openLinkedGroup(chat)
	m, report := m.applyLinkedGroup(cmd().(linkedGroupReadyMsg))
	require.NotNil(t, report)
	assert.Equal(t, int64(1), m.currentChatID)
	assert.Empty(t, m.pageHistory)
	o.err = nil
	m, cmd = m.openLinkedGroup(chat)
	ready := cmd().(linkedGroupReadyMsg)
	m.linkOpenSerial++
	m, _ = m.applyLinkedGroup(ready)
	assert.Equal(t, int64(1), m.currentChatID)
	assert.Empty(t, m.pageHistory)
}

func TestImportantPreviousUsesSelectionAndAnchorWithoutBackStack(t *testing.T) {
	m, o := importantUIFixture(t)
	m.chatWindow.ThreadRootID = 7
	assert.Equal(t, keys.ActionPreviousImportant, m.keyMap.Resolve(keys.ContextChat, "["))
	cmd := m.previousImportant()
	require.NotNil(t, cmd)
	ready := cmd().(previousImportantMsg)
	assert.Equal(t, 20, o.before)
	assert.Equal(t, 7, o.root)
	next, _ := m.updateInner(ready)
	m = next.(RootModel)
	assert.Equal(t, project.AnchorMessage, m.chatWindow.Anchor.Kind)
	assert.Equal(t, 5, m.chatWindow.Anchor.MsgID)
	assert.Equal(t, 7, m.chatWindow.ThreadRootID)
	assert.Empty(t, m.pageHistory)
}

func TestImportantVisibilityRequiresFocusedUnobscuredRenderedBubbles(t *testing.T) {
	m, o := importantUIFixture(t)
	m.chatWindow.ThreadRootID = 7
	for _, obscure := range []func(*RootModel){
		func(m *RootModel) { m.focus = FocusChatList },
		func(m *RootModel) { m.terminalBlurred = true },
		func(m *RootModel) { m.chatMenu = components.NewChatContextMenu(domain.Chat{}, nil, m.keyMap) },
		func(m *RootModel) { m.help = components.NewHelpModal(m.keyMap, 80, 25) },
	} {
		copy := m
		obscure(&copy)
		_, cmd := copy.readVisibleImportant(importantVisibleMsg{m.chatSub})
		assert.Nil(t, cmd)
	}
	m, cmd := m.readVisibleImportant(importantVisibleMsg{m.chatSub})
	require.NotNil(t, cmd)
	done := cmd().(importantReadDoneMsg)
	assert.Equal(t, []int{20}, o.visible)
	assert.Equal(t, 7, o.root)
	next, _ := m.updateInner(done)
	m = next.(RootModel)
	_, cmd = m.readVisibleImportant(importantVisibleMsg{m.chatSub})
	assert.Nil(t, cmd, "same layout and revision should not be reported twice")
}

func TestImportantVisibleFailureSchedulesRetry(t *testing.T) {
	m, o := importantUIFixture(t)
	o.err = errors.New("offline")
	m, cmd := m.readVisibleImportant(importantVisibleMsg{m.chatSub})
	require.NotNil(t, cmd)
	next, _ := m.updateInner(cmd())
	m = next.(RootModel)
	assert.Empty(t, m.importantSeen)
	assert.False(t, m.importantReading)
	assert.NotNil(t, m.importantVisibleTick())
	assert.True(t, m.importantTimer)
}

func TestImportantTitleReservesCountsAndUnknownState(t *testing.T) {
	m, _ := importantUIFixture(t)
	assert.Contains(t, m.importantTitle("A very long channel name", 26), "@ 1 · ♥ 0")
	assert.LessOrEqual(t, ansi.StringWidth(m.importantTitle("A very long channel name", 26)), 22)
	m.importantUnread.Loading = true
	assert.Contains(t, m.importantTitle("Channel", 40), "@ … · ♥ …")
	m.importantUnread = domain.ImportantUnread{Failed: true}
	assert.Contains(t, m.importantTitle("Channel", 40), "@ ? · ♥ ?")
}

func TestImportantInsertModeKeepsBracketAsText(t *testing.T) {
	m, o := importantUIFixture(t)
	next, _ := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	next, _ = next.Update(tea.KeyPressMsg{Code: '[', Text: "["})
	assert.Equal(t, "[", next.(RootModel).chat.ComposerValue())
	assert.Zero(t, o.before)
}
