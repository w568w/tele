package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type importantStub struct {
	*stubClient
	pages   map[domain.ImportantKind]map[int][]domain.Message
	get     func(domain.ImportantKind, int) (domain.UnreadPage, error)
	read    func([]int) error
	readIDs []int
}

func (s *importantStub) GetUnreadImportant(_ context.Context, _ domain.Peer, kind domain.ImportantKind, offset int) (domain.UnreadPage, error) {
	if s.get != nil {
		return s.get(kind, offset)
	}
	return domain.UnreadPage{Messages: s.pages[kind][offset]}, nil
}
func (s *importantStub) ReadImportantContents(_ context.Context, _ domain.Peer, ids []int) error {
	s.readIDs = append(s.readIDs, ids...)
	if s.read != nil {
		return s.read(ids)
	}
	return nil
}
func importantFixture(t *testing.T) (*Owner, *importantStub) {
	t.Helper()
	c := &importantStub{stubClient: &stubClient{}, pages: map[domain.ImportantKind]map[int][]domain.Message{
		domain.ImportantMention:  {0: {{ID: 90, ThreadRootID: 5}, {ID: 50, ThreadRootID: 5}}, 50: {{ID: 10, ThreadRootID: 7}}},
		domain.ImportantReaction: {0: {{ID: 90, ThreadRootID: 5}, {ID: 30, ReplyToMsgID: 5}}},
	}}
	o, state := newOwnerWithClient(t, c)
	state.Store().SetChat(domain.Chat{ID: 1, Peer: domain.Peer{ID: 1, Type: domain.PeerSuperGroup}})
	return o, c
}

func TestImportantPaginationUnionAndThreadNavigation(t *testing.T) {
	o, _ := importantFixture(t)
	ctx := context.Background()
	assert.True(t, o.ImportantUnread(1, 0).Loading)
	require.NoError(t, o.RefreshImportant(ctx, 1))
	whole, thread := o.ImportantUnread(1, 0), o.ImportantUnread(1, 5)
	assert.Equal(t, 3, whole.Mentions)
	assert.Equal(t, 2, whole.Reactions)
	assert.Equal(t, 2, thread.Mentions)
	assert.Equal(t, 2, thread.Reactions)
	assert.Empty(t, o.state.Store().Messages(1), "unread results must not split continuous history")
	before := 100
	for _, want := range []int{90, 50, 30, 90} {
		id, err := o.PreviousImportant(ctx, 1, 5, before)
		require.NoError(t, err)
		assert.Equal(t, want, id)
		before = id
	}
}

func TestImportantVisibleReadFailureRetryAndConcurrentReaction(t *testing.T) {
	o, c := importantFixture(t)
	ctx := context.Background()
	require.NoError(t, o.RefreshImportant(ctx, 1))
	c.read = func([]int) error { return errors.New("offline") }
	require.Error(t, o.ReadVisibleImportant(ctx, 1, 5, []int{90, 90, 10, 999}))
	assert.Equal(t, []int{90}, c.readIDs)
	assert.Equal(t, 2, o.ImportantUnread(1, 5).Mentions)
	c.read = func(ids []int) error {
		// An overlapping client must not send a duplicate acknowledgement.
		require.NoError(t, o.ReadVisibleImportant(ctx, 1, 5, ids))
		return nil
	}
	require.NoError(t, o.ReadVisibleImportant(ctx, 1, 5, []int{90}))
	assert.Equal(t, []int{90, 90}, c.readIDs)
	assert.Equal(t, 1, o.ImportantUnread(1, 5).Mentions)
	assert.Equal(t, 1, o.ImportantUnread(1, 5).Reactions)
	c.read = func([]int) error {
		o.applyImportantEvent(store.Event{Kind: store.EventReactionsUpdate, ChatID: 1, MsgID: 30, ReactionsUnread: true})
		return nil
	}
	require.NoError(t, o.ReadVisibleImportant(ctx, 1, 5, []int{30}))
	assert.Equal(t, 1, o.ImportantUnread(1, 5).Reactions, "new reaction during RPC must survive")
}

func TestImportantScanCannotResurrectReadOrDeletedTargets(t *testing.T) {
	o, c := importantFixture(t)
	c.get = func(kind domain.ImportantKind, offset int) (domain.UnreadPage, error) {
		if kind == domain.ImportantMention && offset == 0 {
			o.applyImportantEvent(store.Event{Kind: store.EventReadContents, ChatID: 1, MsgIDs: []int{90}})
			o.applyImportantEvent(store.Event{Kind: store.EventDeleteMessages, ChatID: 1, MsgIDs: []int{50}})
		}
		return domain.UnreadPage{Messages: c.pages[kind][offset]}, nil
	}
	require.NoError(t, o.RefreshImportant(context.Background(), 1))
	assert.Equal(t, 1, o.ImportantUnread(1, 0).Mentions)
	assert.Equal(t, 1, o.ImportantUnread(1, 0).Reactions)
	o.applyImportantEvent(store.Event{Kind: store.EventNewMessage, Message: domain.Message{ID: 110, ChatID: 1, Mentioned: true}})
	assert.Equal(t, 1, o.ImportantUnread(1, 0).Mentions, "Mentioned alone is not unread")
	o.applyImportantEvent(store.Event{Kind: store.EventNewMessage, Message: domain.Message{ID: 111, ChatID: 1, Mentioned: true, MediaUnread: true}})
	assert.Equal(t, 2, o.ImportantUnread(1, 0).Mentions)
}

func TestImportantFailedScanAndNonAdvancingPage(t *testing.T) {
	o, c := importantFixture(t)
	c.get = func(domain.ImportantKind, int) (domain.UnreadPage, error) {
		return domain.UnreadPage{}, errors.New("offline")
	}
	_, err := o.PreviousImportant(context.Background(), 1, 0, 100)
	require.Error(t, err)
	assert.True(t, o.ImportantUnread(1, 0).Failed)
	c.get = func(domain.ImportantKind, int) (domain.UnreadPage, error) {
		return domain.UnreadPage{Messages: []domain.Message{{ID: 90}}}, nil
	}
	require.ErrorContains(t, o.RefreshImportant(context.Background(), 1), "did not advance")
	c.get = nil
	require.NoError(t, o.RefreshImportant(context.Background(), 1))
	assert.False(t, o.ImportantUnread(1, 0).Failed)
}

func TestImportantOtherClientReadIsPeerScoped(t *testing.T) {
	o, _ := importantFixture(t)
	require.NoError(t, o.RefreshImportant(context.Background(), 1))
	o.state.Store().SetChat(domain.Chat{ID: 2, Peer: domain.Peer{ID: 2, Type: domain.PeerUser}})
	o.important[2] = &importantIndex{loaded: true, items: map[int]importantItem{90: {reaction: true, rootKnown: true}}}
	o.applyImportantEvent(store.Event{Kind: store.EventReadContents, MsgIDs: []int{90}})
	assert.Equal(t, 0, o.ImportantUnread(2, 0).Reactions)
	assert.Equal(t, 2, o.ImportantUnread(1, 0).Reactions)
	o.applyImportantEvent(store.Event{Kind: store.EventReadContents, ChatID: 1, MsgIDs: []int{90}})
	assert.Equal(t, 1, o.ImportantUnread(1, 0).Reactions)
}

func TestImportantUnknownReactionRootAndRefresh(t *testing.T) {
	o, c := importantFixture(t)
	require.NoError(t, o.RefreshImportant(context.Background(), 1))
	assert.True(t, o.applyImportantEvent(store.Event{Kind: store.EventReactionsUpdate, ChatID: 1, MsgID: 120, ReactionsUnread: true}))
	assert.True(t, o.ImportantUnread(1, 5).Loading)
	c.pages[domain.ImportantReaction][0] = []domain.Message{{ID: 120, ThreadRootID: 7}}
	require.NoError(t, o.RefreshImportant(context.Background(), 1))
	assert.False(t, o.ImportantUnread(1, 5).Loading)
	assert.Equal(t, 0, o.ImportantUnread(1, 5).Reactions)
	assert.Equal(t, 1, o.ImportantUnread(1, 7).Reactions)
}

func TestImportantRefreshWaiterSharesFailure(t *testing.T) {
	o, _ := importantFixture(t)
	idx := o.importantLocked(1)
	idx.done = make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- o.RefreshImportant(ctx, 1) }()
	o.importantMu.Lock()
	idx.err = errors.New("failed scan")
	close(idx.done)
	o.importantMu.Unlock()
	require.ErrorContains(t, <-result, "failed scan")
}
