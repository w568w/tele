package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
)

func observedStore(t *testing.T) (*store.SQLiteStore, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.WarnLevel)
	s, err := store.NewSQLite(":memory:", zap.New(core))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s, logs
}

// A message whose text goes from something to nothing is legitimate: a photo
// can lose its caption. It is also what a broken delivery looks like, and the
// two are indistinguishable from here, so the store says so rather than
// guessing. The text itself is never logged (#80).
func TestSQLite_UpdateMessageText_ReportsTextGoingEmpty(t *testing.T) {
	s, logs := observedStore(t)
	s.AppendMessage(domain.Message{
		ID: 1, ChatID: 5, Text: "a long streamed answer",
		Photo:           &domain.PhotoRef{ID: 7},
		AppliedPosition: 20,
	})

	s.UpdateMessageText(5, 1, "", nil)

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, "stored text replaced by an empty one", entries[0].Message)
	assert.Equal(t, map[string]any{
		"chat_id":  int64(5),
		"msg_id":   int64(1),
		"was_len":  int64(22),
		"now_len":  int64(0),
		"media":    true,
		"position": int64(20),
	}, entries[0].ContextMap())
}

func TestSQLite_UpdateMessageText_SaysNothingAboutOrdinaryEdits(t *testing.T) {
	s, logs := observedStore(t)
	s.AppendMessage(domain.Message{ID: 1, ChatID: 5, Text: "before"})

	s.UpdateMessageText(5, 1, "after", nil)
	s.UpdateMessageText(5, 1, "", nil)
	s.UpdateMessageText(5, 1, "", nil)

	assert.Len(t, logs.All(), 1, "only the transition is worth a line, not every empty write")
}
