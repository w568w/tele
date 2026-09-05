package ui_test

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenKey_OnSingleLinkMessage_OpensURL(t *testing.T) {
	var got string
	defer ui.SetURLOpenerForTest(func(u string) { got = u })()

	m, st := newRootOnChat(t)
	st.AppendMessage(domain.Message{ID: 3, ChatID: 1, Date: time.Now(),
		Text:     "see https://example.com now",
		Entities: []domain.MessageEntity{{Type: "url", Offset: 4, Length: 19}}})
	nm, _ := applyHistory(t, m, st, 1)
	m = nm.(ui.RootModel)
	m.View()

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	require.NotNil(t, cmd, "pressing o on a single-link message must open it")
	drainMsgs(cmd())
	assert.Equal(t, "https://example.com", got)
}

func TestOpenKey_MultipleTargets_OpensPickerThenTarget(t *testing.T) {
	var got string
	defer ui.SetURLOpenerForTest(func(u string) { got = u })()

	m, st := newRootOnChat(t)
	// Photo with a caption link -> two open targets (Photo + link).
	st.AppendMessage(domain.Message{ID: 5, ChatID: 1, Date: time.Now(),
		Text:     "pic https://example.com",
		Photo:    &domain.PhotoRef{ID: 1, FullThumbSize: "y"},
		Entities: []domain.MessageEntity{{Type: "url", Offset: 4, Length: 19}}})
	nm, _ := applyHistory(t, m, st, 1)
	m = nm.(ui.RootModel)
	m.View()

	nm, _ = m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	m = nm.(ui.RootModel)
	require.True(t, m.OpenPickerOpen(), "two targets must open the picker")

	// Pick the second entry (the link) by digit; the picker emits a chosen msg
	// that the event loop routes back to open the target.
	nm, cmd := m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	m = nm.(ui.RootModel)
	assert.False(t, m.OpenPickerOpen(), "choosing closes the picker")
	require.NotNil(t, cmd)
	for _, msg := range drainMsgs(cmd()) {
		nm, cmd2 := m.Update(msg)
		m = nm.(ui.RootModel)
		if cmd2 != nil {
			drainMsgs(cmd2())
		}
	}
	assert.Equal(t, "https://example.com", got)
}

func TestOpenKey_OnPlainTextMessage_NoURLOpened(t *testing.T) {
	var called bool
	defer ui.SetURLOpenerForTest(func(string) { called = true })()

	m, st := newRootOnChat(t)
	st.AppendMessage(domain.Message{ID: 4, ChatID: 1, Date: time.Now(), Text: "just text"})
	nm, _ := applyHistory(t, m, st, 1)
	m = nm.(ui.RootModel)
	m.View()

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	if cmd != nil {
		drainMsgs(cmd())
	}
	assert.False(t, called, "plain text has no link to open")
}

func TestOpenKey_OnTelegramMessageLinkNavigatesInApp(t *testing.T) {
	m, st := newRootOnChat(t)
	peer := domain.Peer{ID: 2, Type: domain.PeerSuperGroup, AccessHash: 20}
	st.SetChat(domain.Chat{ID: 2, Title: "Arch Linux CN", Peer: peer})
	st.SetMessages(2, []domain.Message{{ID: 42, ChatID: 2, Text: "target", Date: time.Now()}})
	st.AppendMessage(domain.Message{
		ID: 3, ChatID: 1, Date: time.Now(), Text: "https://t.me/archlinuxcn_group/42",
		Entities: []domain.MessageEntity{{Type: "url", Offset: 0, Length: 40}},
	})
	nm, _ := applyHistory(t, m, st, 1)
	m = nm.(ui.RootModel)
	owner := m.Owner().(*testOwner)
	owner.linkTarget = domain.MessageTarget{
		ChatID: 2, Peer: peer, Title: "Arch Linux CN", MsgID: 42,
	}

	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	m = nm.(ui.RootModel)
	require.NotNil(t, cmd)
	nm, _ = m.Update(cmd())
	m = nm.(ui.RootModel)
	m = drainOwner(t, m)

	assert.Equal(t, int64(2), m.CurrentChatID())
	assert.Equal(t, 42, m.Chat().SelectedMessageID())
}

func TestOpenKey_OnUnsupportedTelegramLinkUsesSystemHandler(t *testing.T) {
	var got string
	defer ui.SetURLOpenerForTest(func(raw string) { got = raw })()
	m, st := newRootOnChat(t)
	raw := "https://t.me/addstickers/example"
	st.AppendMessage(domain.Message{
		ID: 3, ChatID: 1, Date: time.Now(), Text: raw,
		Entities: []domain.MessageEntity{{Type: "url", Offset: 0, Length: len(raw)}},
	})
	nm, _ := applyHistory(t, m, st, 1)
	m = nm.(ui.RootModel)

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	require.NotNil(t, cmd)
	drainMsgs(cmd())
	assert.Equal(t, raw, got)
}

func TestOpenKey_OnTelegramCommentLinkOpensAnchoredDiscussion(t *testing.T) {
	m, st := newRootOnChat(t)
	raw := "https://t.me/channel/42?comment=77"
	st.AppendMessage(domain.Message{
		ID: 3, ChatID: 1, Date: time.Now(), Text: raw,
		Entities: []domain.MessageEntity{{Type: "url", Offset: 0, Length: len(raw)}},
	})
	nm, _ := applyHistory(t, m, st, 1)
	m = nm.(ui.RootModel)
	owner := m.Owner().(*testOwner)
	sourcePeer := domain.Peer{ID: 2, Type: domain.PeerChannel, AccessHash: 20}
	discussionPeer := domain.Peer{ID: 3, Type: domain.PeerSuperGroup, AccessHash: 30}
	owner.linkTarget = domain.MessageTarget{ChatID: 2, Peer: sourcePeer, Title: "Channel", MsgID: 42}
	owner.discussion = domain.Discussion{
		Chat:      domain.Chat{ID: 3, Title: "Discussion", Peer: discussionPeer},
		RootMsgID: 40,
		Messages:  []domain.Message{{ID: 77, ChatID: 3, ThreadRootID: 40, Text: "comment", Date: time.Now()}},
	}

	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	m = nm.(ui.RootModel)
	require.NotNil(t, cmd)
	nm, cmd = m.Update(cmd())
	m = nm.(ui.RootModel)
	require.NotNil(t, cmd)
	nm, _ = m.Update(cmd())
	m = nm.(ui.RootModel)
	m = drainOwner(t, m)

	assert.Equal(t, int64(2), owner.discussionFrom)
	assert.Equal(t, 42, owner.discussionMsgID)
	assert.Equal(t, int64(3), m.CurrentChatID())
	assert.Equal(t, 77, m.Chat().SelectedMessageID())
}
