package core

import (
	"context"
	"os"
	"time"

	"github.com/sorokin-vladimir/tele/internal/core/outbox"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	internaltg "github.com/sorokin-vladimir/tele/internal/tg"
)

func (o *Owner) GetStickerCatalog(ctx context.Context) (domain.StickerCatalog, error) {
	client, ok := o.client.(internaltg.StickerClient)
	if !ok {
		return domain.StickerCatalog{}, &telerr.Error{Kind: telerr.Internal, Op: "messages.getAllStickers", Detail: "no sticker client"}
	}
	return client.GetStickerCatalog(ctx)
}

func (o *Owner) GetStickerPack(ctx context.Context, pack domain.StickerPackRef) ([]domain.StickerRef, error) {
	client, ok := o.client.(internaltg.StickerClient)
	if !ok {
		return nil, &telerr.Error{Kind: telerr.Internal, Op: "messages.getStickerSet", Detail: "no sticker client"}
	}
	return client.GetStickerPack(ctx, pack)
}

// FetchStickerPreview caches a catalogue sticker without requiring it to
// belong to a message already present in the store.
func (o *Owner) FetchStickerPreview(ctx context.Context, sticker domain.StickerRef) (string, error) {
	if o.media.cache == nil {
		return "", &telerr.Error{Kind: telerr.Internal, Op: "fetch sticker", Detail: "no media cache"}
	}
	mediaRef := &domain.MediaRef{Kind: domain.MediaSticker}
	slot, ok := domain.StickerPreviewSlot(mediaRef, &sticker.Document)
	if !ok {
		return "", &telerr.Error{Kind: telerr.NotFound, Op: "fetch sticker", Detail: "sticker has no static preview"}
	}
	ref := mediaRefForSticker(sticker.Document, slot)
	key := ref.cacheKey()
	if path, ok := o.media.cache.Path(key); ok {
		return path, nil
	}
	path, err, _ := o.media.inflight.Do(key, func() (any, error) {
		if path, ok := o.media.cache.Path(key); ok {
			return path, nil
		}
		return o.media.cache.Put(key, func(file *os.File) error {
			return o.media.stream(ctx, ref, file)
		})
	})
	if err != nil {
		return "", err
	}
	return path.(string), nil
}

func mediaRefForSticker(doc domain.DocumentRef, slot domain.MediaSlot) mediaRef {
	return mediaRef{slot: slot, doc: doc, kind: domain.MediaSticker}
}

func (o *Owner) SendSticker(ctx context.Context, chatID int64, peer domain.Peer, sticker domain.StickerRef, replyToMsgID, threadRootID int) error {
	client, ok := o.client.(internaltg.StickerClient)
	if !ok {
		return &telerr.Error{Kind: telerr.Internal, Op: "messages.sendMedia", Detail: "no sticker client"}
	}
	resolved, err := o.sendPeer(chatID, peer)
	if err != nil {
		return err
	}
	id, err := client.SendSticker(ctx, resolved, sticker, replyToMsgID, threadRootID, outbox.RandomIDFor(NewRef()))
	if err != nil {
		return err
	}
	if id == 0 {
		return nil
	}
	sent := domain.Message{
		ID: id, ChatID: chatID, IsOut: true, Date: time.Now(),
		Media:    &domain.MediaRef{Kind: domain.MediaSticker, Emoji: sticker.Emoji},
		Document: &sticker.Document, ReplyToMsgID: replyToMsgID, ThreadRootID: threadRootID,
	}
	if fresh, err := o.client.RefreshMessage(ctx, resolved, id); err == nil {
		fresh.ChatID = chatID
		sent = fresh
	}
	o.state.ApplyIncoming(sent)
	return nil
}
