package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/sorokin-vladimir/tele/internal/domain"
)

// mergeMsgs builds a page whose dates follow its ids, which is the ordinary
// case and keeps the expected order readable as a list of ids.
func mergeMsgs(ids ...int) []domain.Message {
	out := make([]domain.Message, 0, len(ids))
	for _, id := range ids {
		out = append(out, domain.Message{ID: id, Date: time.Unix(int64(id), 0)})
	}
	return out
}

func mergeIDs(ms []domain.Message) []int {
	out := make([]int, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func TestMergeMessages_PrependsAnOlderPage(t *testing.T) {
	got := domain.MergeMessages(mergeMsgs(3, 4), mergeMsgs(1, 2))

	assert.Equal(t, []int{1, 2, 3, 4}, mergeIDs(got))
}

func TestMergeMessages_AppendsANewerPage(t *testing.T) {
	got := domain.MergeMessages(mergeMsgs(1, 2), mergeMsgs(3, 4))

	assert.Equal(t, []int{1, 2, 3, 4}, mergeIDs(got))
}

func TestMergeMessages_FillsAGapInTheMiddle(t *testing.T) {
	// What closing a gap looks like: the stored history already runs past the
	// hole because live messages resumed while the missed range never arrived.
	got := domain.MergeMessages(mergeMsgs(1, 2, 9), mergeMsgs(3, 4))

	assert.Equal(t, []int{1, 2, 3, 4, 9}, mergeIDs(got))
}

func TestMergeMessages_DropsOverlappingIDs(t *testing.T) {
	// Overlapping server pages would otherwise stack into a repeating date
	// range - issue #120.
	got := domain.MergeMessages(mergeMsgs(3, 4), mergeMsgs(1, 2, 3))

	assert.Equal(t, []int{1, 2, 3, 4}, mergeIDs(got))
}

func TestMergeMessages_KeepsTheFetchedCopyOfADuplicate(t *testing.T) {
	// A message edited while this client was not listening: the page carries
	// the current text and the store carries the text from before the gap.
	stored := []domain.Message{{ID: 1, Date: time.Unix(1, 0), Text: "before"}}
	fetched := []domain.Message{{ID: 1, Date: time.Unix(1, 0), Text: "after"}}

	got := domain.MergeMessages(stored, fetched)

	assert.Equal(t, []int{1}, mergeIDs(got))
	assert.Equal(t, "after", got[0].Text)
}

func TestMergeMessages_OrdersASharedDateByID(t *testing.T) {
	// Telegram stamps every part of an album with one date.
	at := time.Unix(100, 0)
	stored := []domain.Message{{ID: 7, Date: at}}
	fetched := []domain.Message{{ID: 5, Date: at}, {ID: 6, Date: at}}

	got := domain.MergeMessages(stored, fetched)

	assert.Equal(t, []int{5, 6, 7}, mergeIDs(got))
}

func TestMergeMessages_EmptyFetchedPageChangesNothing(t *testing.T) {
	got := domain.MergeMessages(mergeMsgs(1, 2), nil)

	assert.Equal(t, []int{1, 2}, mergeIDs(got))
}

func TestMergeMessages_EmptyStoreTakesThePage(t *testing.T) {
	got := domain.MergeMessages(nil, mergeMsgs(1, 2))

	assert.Equal(t, []int{1, 2}, mergeIDs(got))
}

func TestMergeMessages_FullyDuplicatePageChangesTheLengthOfNothing(t *testing.T) {
	got := domain.MergeMessages(mergeMsgs(3, 4), mergeMsgs(3, 4))

	assert.Equal(t, []int{3, 4}, mergeIDs(got))
}
