package ui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sorokin-vladimir/tele/internal/core/project"
)

type importantOwner interface {
	RefreshImportant(context.Context, int64) error
	PreviousImportant(context.Context, int64, int, int) (int, error)
	ReadVisibleImportant(context.Context, int64, int, []int) error
}

type importantPollMsg struct{ sub project.SubID }
type importantVisibleMsg struct{ sub project.SubID }
type importantReadDoneMsg struct {
	sub project.SubID
	err error
}
type previousImportantMsg struct {
	sub project.SubID
	id  int
	err error
}

func (m RootModel) importantPollTick() tea.Cmd {
	if _, ok := m.owner.(importantOwner); !ok || m.chatSub == 0 {
		return nil
	}
	sub := m.chatSub
	return tea.Tick(30*time.Second, func(time.Time) tea.Msg { return importantPollMsg{sub} })
}

func (m RootModel) pollImportant(msg importantPollMsg) (RootModel, tea.Cmd) {
	owner, ok := m.owner.(importantOwner)
	if !ok || msg.sub != m.chatSub || m.currentChatID == 0 {
		return m, nil
	}
	ctx, chatID := m.ctx, m.currentChatID
	return m, tea.Batch(m.importantPollTick(), func() tea.Msg {
		_ = owner.RefreshImportant(ctx, chatID)
		return nil
	})
}

func (m RootModel) importantVisible() bool {
	return m.screen == ScreenMain && m.currentChatID != 0 && m.focus == FocusChat &&
		!m.terminalBlurred && !m.chat.IsLoading() && !m.importantUnread.Loading && !m.importantUnread.Failed &&
		m.searchModel == nil && m.contextMenu == nil && m.chatMenu == nil &&
		m.reactionPicker == nil && m.mentionPopup == nil && m.openPicker == nil &&
		m.filePicker == nil && m.stickerPicker == nil && m.videoPlayer == nil &&
		m.photoViewer == nil && m.help == nil && m.settings == nil && m.profile == nil &&
		m.joinPrompt == nil && !m.noticeActive() && (m.toasts == nil || m.toasts.Empty())
}

// Wait for the updated bubble layout to be rendered before reporting IDs.
func (m *RootModel) importantVisibleTick() tea.Cmd {
	if _, ok := m.owner.(importantOwner); !ok || m.importantTimer || m.importantReading ||
		!m.importantVisible() || m.importantUnread.Mentions+m.importantUnread.Reactions == 0 {
		return nil
	}
	m.importantTimer = true
	sub := m.chatSub
	delay := max(200*time.Millisecond, time.Until(m.importantRetryAt))
	return tea.Tick(delay, func(time.Time) tea.Msg { return importantVisibleMsg{sub} })
}

func (m RootModel) readVisibleImportant(msg importantVisibleMsg) (RootModel, tea.Cmd) {
	if msg.sub != m.chatSub {
		return m, nil
	}
	m.importantTimer = false
	owner, ok := m.owner.(importantOwner)
	if !ok || !m.importantVisible() || m.importantReading {
		return m, nil
	}
	ids := m.chat.VisibleReadIDs()
	signature := fmt.Sprint(m.importantUnread.Revision, ":", ids)
	if len(ids) == 0 || signature == m.importantSeen {
		return m, nil
	}
	m.importantSeen, m.importantReading = signature, true
	ctx, chatID, root := m.ctx, m.currentChatID, m.chatWindow.ThreadRootID
	return m, func() tea.Msg {
		return importantReadDoneMsg{msg.sub, owner.ReadVisibleImportant(ctx, chatID, root, ids)}
	}
}

func (m *RootModel) previousImportant() tea.Cmd {
	owner, ok := m.owner.(importantOwner)
	if !ok || m.currentChatID == 0 || m.pendingJumpMsgID != 0 || m.importantJumping {
		return nil
	}
	m.importantJumping = true
	ctx, sub, chatID, root, before := m.ctx, m.chatSub, m.currentChatID, m.chatWindow.ThreadRootID, m.chat.SelectedMessageID()
	return func() tea.Msg {
		id, err := owner.PreviousImportant(ctx, chatID, root, before)
		return previousImportantMsg{sub, id, err}
	}
}

func (m RootModel) importantTitle(title string, width int) string {
	if m.currentChatID == 0 {
		return title
	}
	counts := fmt.Sprintf("@ %d · ♥ %d", m.importantUnread.Mentions, m.importantUnread.Reactions)
	if m.importantUnread.Loading {
		counts = "@ … · ♥ …"
	} else if m.importantUnread.Failed {
		counts = "@ ? · ♥ ?"
	}
	return ansi.Truncate(ansi.Truncate(title, max(0, width-ansi.StringWidth(counts)-7), "…")+" "+counts, max(0, width-4), "")
}
