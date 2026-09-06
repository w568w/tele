package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
)

func TestApplyIncomingMessage_AppendsAndCounts(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1, Title: "A"})

	assert.True(t, store.ApplyIncomingMessage(s, domain.Message{ID: 5, ChatID: 1, Text: "hi"}))

	msgs := s.Messages(1)
	require.Len(t, msgs, 1)
	assert.Equal(t, 5, msgs[0].ID)
	c, _ := s.GetChat(1)
	assert.Equal(t, 1, c.UnreadCount)
}

func TestApplyIncomingMessage_OutgoingAppendsWithoutCounting(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1})

	assert.False(t, store.ApplyIncomingMessage(s, domain.Message{ID: 5, ChatID: 1, IsOut: true}))

	require.Len(t, s.Messages(1), 1)
	c, _ := s.GetChat(1)
	assert.Equal(t, 0, c.UnreadCount)
}

func TestApplyIncomingMessage_CountsMention(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1})

	assert.True(t, store.ApplyIncomingMessage(s, domain.Message{ID: 5, ChatID: 1, Mentioned: true, MediaUnread: true}))

	c, _ := s.GetChat(1)
	assert.Equal(t, 1, c.UnreadCount)
	assert.Equal(t, 1, c.UnreadMentionsCount)
}

// A message already covered by the read pointer still belongs in history, but
// changes no counter — so the caller must not be told to refresh derived views.
func TestApplyIncomingMessage_ReadElsewhereAppendsAndReportsNoChange(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1, ReadInboxMaxID: 10})

	assert.False(t, store.ApplyIncomingMessage(s, domain.Message{ID: 5, ChatID: 1}))

	require.Len(t, s.Messages(1), 1)
	c, _ := s.GetChat(1)
	assert.Equal(t, 0, c.UnreadCount)
}

// The helper takes no viewport: the same message produces the same state
// regardless of what any client has open. This is what #183 requires.
func TestApplyIncomingMessage_ReplayCountsOnce(t *testing.T) {
	s := store.NewMemory()
	s.SetChat(domain.Chat{ID: 1})
	msg := domain.Message{ID: 5, ChatID: 1, Mentioned: true, MediaUnread: true}

	require.True(t, store.ApplyIncomingMessage(s, msg))
	assert.False(t, store.ApplyIncomingMessage(s, msg))

	c, _ := s.GetChat(1)
	assert.Equal(t, 1, c.UnreadCount)
	assert.Equal(t, 1, c.UnreadMentionsCount)
}
