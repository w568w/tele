package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/sorokin-vladimir/tele/internal/core/project"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ui/components"
	"github.com/sorokin-vladimir/tele/internal/ui/keys"
)

type joinDiscussionPrompt struct {
	chatID int64
	ref    string
}

type joinDiscussionRetryMsg struct {
	err error
}

type discussionOpenedMsg struct {
	fromChatID int64
	source     domain.MessageTarget
	commentID  int
	discussion domain.Discussion
	err        error
}

func openDiscussionCmd(ctx context.Context, owner Owner, fromChatID int64, source domain.MessageTarget, discussionChatID int64, commentID int) tea.Cmd {
	return func() tea.Msg {
		discussion, err := owner.OpenDiscussion(ctx, source.ChatID, source.MsgID, discussionChatID)
		return discussionOpenedMsg{
			fromChatID: fromChatID,
			source:     source,
			commentID:  commentID,
			discussion: discussion,
			err:        err,
		}
	}
}

func (m RootModel) openDiscussion(sourceMsgID int, discussionChatID int64) (RootModel, tea.Cmd) {
	return m.openDiscussionTarget(m.currentMessageTarget(sourceMsgID), discussionChatID, 0)
}

func (m RootModel) openDiscussionTarget(source domain.MessageTarget, discussionChatID int64, commentID int) (RootModel, tea.Cmd) {
	m.contextMenu = nil
	if m.owner == nil || m.currentChatID == 0 || source.ChatID == 0 || source.MsgID == 0 {
		return m, nil
	}
	m.chat.SetLoading(true)
	return m, openDiscussionCmd(m.ctx, m.owner, m.currentChatID, source, discussionChatID, commentID)
}

func (m RootModel) applyDiscussionOpened(msg discussionOpenedMsg) (RootModel, tea.Cmd) {
	if msg.fromChatID != m.currentChatID {
		return m, nil
	}
	if msg.err != nil {
		m.chat.SetLoading(false)
		return m, func() tea.Msg { return errStatus("open comments", msg.err) }
	}
	title := "Comments · " + msg.source.Title
	window := project.ChatWindow{
		ChatID:                msg.discussion.Chat.ID,
		Peer:                  msg.discussion.Chat.Peer,
		Title:                 title,
		Anchor:                project.Anchor{Kind: project.AnchorNewest},
		Before:                m.historyLimit,
		ThreadRootID:          msg.discussion.RootMsgID,
		ThreadPeer:            msg.discussion.Chat.Peer,
		ThreadTitle:           title,
		ThreadReadInboxMaxID:  msg.discussion.ReadInboxMaxID,
		ThreadReadOutboxMaxID: msg.discussion.ReadOutboxMaxID,
	}
	if msg.commentID != 0 {
		window.Anchor = project.Anchor{Kind: project.AnchorMessage, MsgID: msg.commentID}
		window.Before, window.After = anchorSides(m.historyLimit)
	}
	return m.activatePage(window, msg.source.ChatID, msg.commentID, true)
}

func (m RootModel) handleJoinDiscussionPromptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := keys.NormalizeKey(msg.String())
	switch key {
	case "n", "esc":
		m.joinPrompt = nil
		return m, nil
	case "y", "enter":
		prompt := *m.joinPrompt
		m.joinPrompt = nil
		if m.owner == nil {
			return m, nil
		}
		ctx, owner := m.ctx, m.owner
		return m, func() tea.Msg {
			return joinDiscussionRetryMsg{err: owner.JoinDiscussionAndRetry(ctx, prompt.chatID, prompt.ref)}
		}
	default:
		return m, nil
	}
}

func (m RootModel) handleJoinDiscussionRetry(msg joinDiscussionRetryMsg) (RootModel, tea.Cmd) {
	if msg.err != nil {
		return m, func() tea.Msg { return errStatus("join discussion", msg.err) }
	}
	m.statusBar.SetStatus("joined discussion group; retrying send")
	return m, nil
}

func (m RootModel) joinDiscussionPromptView(content string) string {
	maxW := m.width - 10
	if maxW > 68 {
		maxW = 68
	}
	if maxW < 26 {
		maxW = 26
	}
	box := components.RenderConfirmBox(
		"Join discussion group?",
		"Telegram refused guest sending. Join this group and retry the comment?",
		"join and retry",
		maxW,
	)
	return overlayCenter(dimBackground(content), box, m.width, m.height)
}
