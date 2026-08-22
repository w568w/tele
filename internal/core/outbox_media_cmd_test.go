package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/telerr"
)

// writeFile creates a real file, because submission stats what it is given.
func writeFile(t *testing.T, name string, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, make([]byte, size), 0o600))
	return path
}

func TestSendMedia_QueuesOneEntryPerAlbumGroup(t *testing.T) {
	o, _ := newCmdOwner(t, &stubClient{})
	q := newOutboxStore(t)
	o.SetOutbox(q)

	var files []MediaFile
	for i := 0; i < 12; i++ {
		files = append(files, MediaFile{Path: writeFile(t, "p.jpg", 10)})
	}
	require.NoError(t, o.SendMedia(context.Background(), MediaSendRequest{
		Ref: "r1", ChatID: 1, Files: files, Caption: "hi", ReplyToMsgID: 5,
	}))

	entries := q.ForChat(1)
	require.Len(t, entries, 2, "ten parts is a full album; the rest is a second one")
	assert.Equal(t, "r1#0", entries[0].Ref)
	assert.Equal(t, "r1#1", entries[1].Ref)
	require.Len(t, entries[0].Media.Parts, 10)
	require.Len(t, entries[1].Media.Parts, 2)
	assert.Equal(t, domain.OutboxMedia, entries[0].Kind)
	assert.Equal(t, domain.OutboxQueued, entries[0].State)
}

func TestSendMedia_CaptionAndReplyGoOnTheFirstGroupOnly(t *testing.T) {
	o, _ := newCmdOwner(t, &stubClient{})
	q := newOutboxStore(t)
	o.SetOutbox(q)

	require.NoError(t, o.SendMedia(context.Background(), MediaSendRequest{
		Ref: "r1", ChatID: 1, Caption: "hi", ReplyToMsgID: 5,
		Files: []MediaFile{
			{Path: writeFile(t, "a.jpg", 3)},
			{Path: writeFile(t, "b.pdf", 4)},
		},
	}))

	entries := q.ForChat(1)
	require.Len(t, entries, 2, "Telegram will not mix a photo and a document in one album")
	assert.Equal(t, "hi", entries[0].Media.Caption)
	assert.Equal(t, 5, entries[0].Media.ReplyToMsgID)
	assert.Empty(t, entries[1].Media.Caption)
	assert.Zero(t, entries[1].Media.ReplyToMsgID)
}

func TestSendMedia_ThreadAddressAppliesToEveryAlbumGroup(t *testing.T) {
	o, _ := newCmdOwner(t, &stubClient{})
	q := newOutboxStore(t)
	o.SetOutbox(q)

	require.NoError(t, o.SendMedia(context.Background(), MediaSendRequest{
		Ref: "r1", ChatID: 1, ThreadRootID: 40,
		Files: []MediaFile{
			{Path: writeFile(t, "a.jpg", 3)},
			{Path: writeFile(t, "b.pdf", 4)},
		},
	}))

	entries := q.ForChat(1)
	require.Len(t, entries, 2)
	assert.Equal(t, 40, entries[0].Media.ThreadRootID)
	assert.Equal(t, 40, entries[1].Media.ThreadRootID)
}

func TestSendMedia_RecordsNameAndSizeFromDisk(t *testing.T) {
	o, _ := newCmdOwner(t, &stubClient{})
	q := newOutboxStore(t)
	o.SetOutbox(q)
	path := writeFile(t, "holiday.jpg", 4096)

	require.NoError(t, o.SendMedia(context.Background(), MediaSendRequest{
		Ref: "r1", ChatID: 1, Files: []MediaFile{{Path: path}},
	}))

	part := q.ForChat(1)[0].Media.Parts[0]
	assert.Equal(t, "holiday.jpg", part.Name)
	assert.Equal(t, int64(4096), part.Size)
}

func TestSendMedia_AnUnreadableFileQueuesNothing(t *testing.T) {
	o, _ := newCmdOwner(t, &stubClient{})
	q := newOutboxStore(t)
	o.SetOutbox(q)

	err := o.SendMedia(context.Background(), MediaSendRequest{
		Ref: "r1", ChatID: 1, Files: []MediaFile{
			{Path: writeFile(t, "a.jpg", 3)},
			{Path: "/definitely/not/here.jpg"},
		},
	})

	require.Error(t, err)
	assert.Equal(t, telerr.NotFound, telerr.Of(err))
	te, ok := telerr.As(err)
	require.True(t, ok)
	assert.Contains(t, te.Detail, "here.jpg")
	assert.Empty(t, q.ForChat(1), "one submission is one action: all of it or none")
}

func TestSendMedia_RefusesAnUnknownChat(t *testing.T) {
	o, _ := newCmdOwner(t, &stubClient{})
	q := newOutboxStore(t)
	o.SetOutbox(q)

	err := o.SendMedia(context.Background(), MediaSendRequest{
		Ref: "r1", ChatID: 999, Files: []MediaFile{{Path: writeFile(t, "a.jpg", 3)}},
	})

	require.Error(t, err)
	assert.Empty(t, q.All())
}

func TestSendMedia_QueuesAnExplicitPeerWithoutALocalChat(t *testing.T) {
	o, _ := newCmdOwner(t, &stubClient{})
	q := newOutboxStore(t)
	o.SetOutbox(q)
	peer := domain.Peer{ID: 2, Type: domain.PeerSuperGroup, AccessHash: 22}

	require.NoError(t, o.SendMedia(context.Background(), MediaSendRequest{
		Ref: "r1", ChatID: 2, Peer: peer,
		Files: []MediaFile{{Path: writeFile(t, "a.jpg", 3)}}, ThreadRootID: 40,
	}))

	entry := q.ForChat(2)[0]
	assert.Equal(t, peer, entry.Media.Peer)
	assert.Equal(t, 40, entry.Media.ThreadRootID)
}

func TestSendMedia_IsIdempotentPerRef(t *testing.T) {
	o, _ := newCmdOwner(t, &stubClient{})
	q := newOutboxStore(t)
	o.SetOutbox(q)
	req := MediaSendRequest{Ref: "r1", ChatID: 1, Files: []MediaFile{{Path: writeFile(t, "a.jpg", 3)}}}

	require.NoError(t, o.SendMedia(context.Background(), req))
	require.NoError(t, o.SendMedia(context.Background(), req))

	assert.Len(t, q.ForChat(1), 1)
}

func TestPartitionMedia_SplitsByClassThenKeepsOrder(t *testing.T) {
	parts := []domain.OutboxMediaPart{
		{Name: "a.jpg", SendAs: domain.MediaPhoto},
		{Name: "b.pdf", SendAs: domain.MediaFile},
		{Name: "c.mp4", SendAs: domain.MediaVideo},
	}

	got := partitionMedia(parts)

	require.Len(t, got, 2)
	assert.Equal(t, []string{"a.jpg", "c.mp4"}, namesOf(got[0]), "photo and video are one class")
	assert.Equal(t, []string{"b.pdf"}, namesOf(got[1]))
}

func TestPartitionMedia_ChunksAtTen(t *testing.T) {
	var parts []domain.OutboxMediaPart
	for i := 0; i < 23; i++ {
		parts = append(parts, domain.OutboxMediaPart{SendAs: domain.MediaPhoto})
	}

	got := partitionMedia(parts)

	require.Len(t, got, 3)
	assert.Len(t, got[0], 10)
	assert.Len(t, got[1], 10)
	assert.Len(t, got[2], 3)
}

func TestPartitionMedia_SendAsFileMovesAPhotoToTheDocumentClass(t *testing.T) {
	parts := []domain.OutboxMediaPart{
		{Name: "a.jpg", SendAs: domain.MediaPhoto},
		{Name: "b.jpg", SendAs: domain.MediaFile},
	}

	got := partitionMedia(parts)

	require.Len(t, got, 2, "a photo sent as a file travels with the documents")
	assert.Equal(t, []string{"a.jpg"}, namesOf(got[0]))
	assert.Equal(t, []string{"b.jpg"}, namesOf(got[1]))
}

func namesOf(parts []domain.OutboxMediaPart) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.Name)
	}
	return out
}
