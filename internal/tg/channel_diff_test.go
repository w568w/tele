package tg

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeUpdatesAPI stands in for the raw client behind the updates manager. Only
// the channel difference call has behavior; the other two satisfy the interface.
type fakeUpdatesAPI struct {
	diff tg.UpdatesChannelDifferenceClass
	err  error
	req  *tg.UpdatesGetChannelDifferenceRequest

	// commonDiff answers the common-state difference call, for the tests about
	// that half. Unset means an account with nothing pending.
	commonDiff tg.UpdatesDifferenceClass
	commonErr  error
}

func (f *fakeUpdatesAPI) UpdatesGetState(context.Context) (*tg.UpdatesState, error) {
	return &tg.UpdatesState{}, nil
}

func (f *fakeUpdatesAPI) UpdatesGetDifference(
	context.Context, *tg.UpdatesGetDifferenceRequest,
) (tg.UpdatesDifferenceClass, error) {
	if f.commonErr != nil {
		return nil, f.commonErr
	}
	if f.commonDiff == nil {
		return &tg.UpdatesDifferenceEmpty{}, nil
	}
	return f.commonDiff, nil
}

func (f *fakeUpdatesAPI) UpdatesGetChannelDifference(
	_ context.Context, req *tg.UpdatesGetChannelDifferenceRequest,
) (tg.UpdatesChannelDifferenceClass, error) {
	f.req = req
	return f.diff, f.err
}

func channelDiffRequest() *tg.UpdatesGetChannelDifferenceRequest {
	return &tg.UpdatesGetChannelDifferenceRequest{
		Channel: &tg.InputChannel{ChannelID: 3834493807, AccessHash: 42},
		Filter:  &tg.ChannelMessagesFilterEmpty{},
		Pts:     2426459,
		Limit:   100,
	}
}

func callChannelDiff(t *testing.T, diff tg.UpdatesChannelDifferenceClass) tg.UpdatesChannelDifferenceClass {
	t.Helper()
	raw := &fakeUpdatesAPI{diff: diff}
	got, err := newChannelDiffAPI(raw, zap.NewNop()).
		UpdatesGetChannelDifference(context.Background(), channelDiffRequest())
	require.NoError(t, err)
	return got
}

func TestChannelDiffAPI_DropsTheCooldownFromADifference(t *testing.T) {
	diff := &tg.UpdatesChannelDifference{
		Final: true,
		Pts:   2426552,
		NewMessages: []tg.MessageClass{
			&tg.Message{ID: 1},
			&tg.Message{ID: 2},
		},
	}
	diff.SetTimeout(300)

	got := callChannelDiff(t, diff)

	out, ok := got.(*tg.UpdatesChannelDifference)
	require.True(t, ok)
	_, hasTimeout := out.GetTimeout()
	assert.False(t, hasTimeout, "the timeout flag must be gone, gotd reads presence from the bit")
	assert.Zero(t, out.Timeout)
	// Everything the manager acts on has to survive untouched.
	assert.True(t, out.Final)
	assert.Equal(t, 2426552, out.Pts)
	assert.Len(t, out.NewMessages, 2)
}

func TestChannelDiffAPI_DropsTheCooldownFromAnEmptyDifference(t *testing.T) {
	diff := &tg.UpdatesChannelDifferenceEmpty{Final: true, Pts: 2426552}
	diff.SetTimeout(300)

	got := callChannelDiff(t, diff)

	out, ok := got.(*tg.UpdatesChannelDifferenceEmpty)
	require.True(t, ok)
	_, hasTimeout := out.GetTimeout()
	assert.False(t, hasTimeout)
	assert.True(t, out.Final)
	assert.Equal(t, 2426552, out.Pts)
}

// The too-long variant is what drives the gap repair, so it has to come back
// intact apart from the timeout.
func TestChannelDiffAPI_DropsTheCooldownFromATooLongDifference(t *testing.T) {
	diff := &tg.UpdatesChannelDifferenceTooLong{
		Dialog: &tg.Dialog{Pts: 2426552},
	}
	diff.SetTimeout(300)

	got := callChannelDiff(t, diff)

	out, ok := got.(*tg.UpdatesChannelDifferenceTooLong)
	require.True(t, ok)
	_, hasTimeout := out.GetTimeout()
	assert.False(t, hasTimeout)
	assert.Equal(t, diff.Dialog, out.Dialog)
}

func TestChannelDiffAPI_LeavesADifferenceWithoutACooldownAlone(t *testing.T) {
	diff := &tg.UpdatesChannelDifference{Final: true, Pts: 2426552}

	got := callChannelDiff(t, diff)

	out, ok := got.(*tg.UpdatesChannelDifference)
	require.True(t, ok)
	_, hasTimeout := out.GetTimeout()
	assert.False(t, hasTimeout)
	assert.True(t, out.Final)
}

func TestChannelDiffAPI_PassesTheRequestThrough(t *testing.T) {
	raw := &fakeUpdatesAPI{diff: &tg.UpdatesChannelDifferenceEmpty{}}
	req := channelDiffRequest()

	_, err := newChannelDiffAPI(raw, zap.NewNop()).UpdatesGetChannelDifference(context.Background(), req)

	require.NoError(t, err)
	assert.Same(t, req, raw.req)
}

func TestChannelDiffAPI_ReturnsTheErrorUntouched(t *testing.T) {
	want := errors.New("get channel difference")
	raw := &fakeUpdatesAPI{err: want}

	_, err := newChannelDiffAPI(raw, zap.NewNop()).UpdatesGetChannelDifference(context.Background(), channelDiffRequest())

	assert.ErrorIs(t, err, want)
}

func TestInputChannelID_YieldsZeroForAnEmptyChannel(t *testing.T) {
	assert.Equal(t, int64(0), inputChannelID(&tg.InputChannelEmpty{}))
	assert.Equal(t, int64(7), inputChannelID(&tg.InputChannel{ChannelID: 7}))
}
