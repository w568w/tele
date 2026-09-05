package tg

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/gotd/td/tg"
	"github.com/sorokin-vladimir/tele/internal/domain"
)

func buildNameMap(users []tg.UserClass) map[int64]string {
	m := make(map[int64]string, len(users))
	for _, u := range users {
		user, ok := u.(*tg.User)
		if !ok {
			continue
		}
		name := strings.TrimSpace(user.FirstName + " " + user.LastName)
		if name == "" {
			name = fmt.Sprintf("User %d", user.ID)
		}
		m[user.ID] = name
	}
	return m
}

func channelTitle(ch *tg.Channel) string {
	if ch.Title != "" {
		return ch.Title
	}
	return fmt.Sprintf("Channel %d", ch.ID)
}

func groupTitle(chat *tg.Chat) string {
	if chat.Title != "" {
		return chat.Title
	}
	return fmt.Sprintf("Chat %d", chat.ID)
}

func buildChatNameMap(chats []tg.ChatClass) map[int64]string {
	m := make(map[int64]string, len(chats))
	for _, c := range chats {
		switch v := c.(type) {
		case *tg.Channel:
			m[v.ID] = channelTitle(v)
		case *tg.Chat:
			m[v.ID] = groupTitle(v)
		}
	}
	return m
}

// forwardInfo builds forward display info from a message's forward header.
// resolve maps an origin peer id to a display name; it may return "" when the
// peer is not present in the entity set. Resolution order: origin peer name,
// then the saved FromName (hidden senders), then the channel post author.
func forwardInfo(fwd tg.MessageFwdHeader, resolve func(int64) string) *domain.ForwardInfo {
	from := ""
	if id, ok := fwd.GetFromID(); ok {
		from = resolve(peerIDFromPeer(id))
	}
	if from == "" {
		if name, ok := fwd.GetFromName(); ok {
			from = name
		}
	}
	if from == "" {
		if author, ok := fwd.GetPostAuthor(); ok {
			from = author
		}
	}
	return &domain.ForwardInfo{From: from}
}

// selectMessageByID returns the message with the given id from a parsed slice.
func selectMessageByID(msgs []domain.Message, id int) (domain.Message, bool) {
	for _, m := range msgs {
		if m.ID == id {
			return m, true
		}
	}
	return domain.Message{}, false
}

func parseHistory(result tg.MessagesMessagesClass, chatID int64) []domain.Message {
	var rawMsgs []tg.MessageClass
	var rawUsers []tg.UserClass
	var rawChats []tg.ChatClass

	switch v := result.(type) {
	case *tg.MessagesMessages:
		rawMsgs, rawUsers, rawChats = v.Messages, v.Users, v.Chats
	case *tg.MessagesMessagesSlice:
		rawMsgs, rawUsers, rawChats = v.Messages, v.Users, v.Chats
	case *tg.MessagesChannelMessages:
		rawMsgs, rawUsers, rawChats = v.Messages, v.Users, v.Chats
	default:
		return nil
	}

	nameMap := buildNameMap(rawUsers)
	chatNameMap := buildChatNameMap(rawChats)
	targetMap := buildMessageTargetMap(rawUsers, rawChats)

	out := make([]domain.Message, 0, len(rawMsgs))
	for _, raw := range rawMsgs {
		if msg, ok := convertMessage(raw, chatID); ok {
			applyExternalReplyTarget(&msg, raw, chatID, func(peer tg.PeerClass) domain.MessageTarget {
				return targetMap[peerIDFromPeer(peer)]
			})
			msg.SenderName = nameMap[msg.SenderID]
			if msg.SenderName == "" {
				msg.SenderName = chatNameMap[msg.SenderID]
			}
			if msg.SenderName == "" && msg.SenderID == 0 && !msg.IsOut {
				// nil FromID: sender is the chat peer (private chat → user)
				// or the chat entity itself (channel/group anonymous post)
				if name := nameMap[chatID]; name != "" {
					msg.SenderName = name
				} else {
					msg.SenderName = chatNameMap[chatID]
				}
			}
			if m, ok := raw.(*tg.Message); ok {
				if fwd, ok := m.GetFwdFrom(); ok {
					msg.Forward = forwardInfo(fwd, func(id int64) string {
						if name := nameMap[id]; name != "" {
							return name
						}
						return chatNameMap[id]
					})
				}
			}
			out = append(out, msg)
		}
	}
	// Telegram API returns newest-first; reverse to chronological order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func buildMessageTargetMap(users []tg.UserClass, chats []tg.ChatClass) map[int64]domain.MessageTarget {
	targets := make(map[int64]domain.MessageTarget, len(users)+len(chats))
	for _, raw := range users {
		if user, ok := raw.(*tg.User); ok {
			if chat, ok := convertUser(user); ok {
				targets[chat.ID] = domain.MessageTarget{ChatID: chat.ID, Peer: chat.Peer, Title: chat.Title}
			}
		}
	}
	for _, raw := range chats {
		var chat domain.Chat
		var ok bool
		switch raw := raw.(type) {
		case *tg.Chat:
			chat, ok = convertGroupChat(raw)
		case *tg.Channel:
			chat, ok = convertChannel(raw)
		}
		if ok {
			targets[chat.ID] = domain.MessageTarget{ChatID: chat.ID, Peer: chat.Peer, Title: chat.Title}
		}
	}
	return targets
}

func applyExternalReplyTarget(msg *domain.Message, raw tg.MessageClass, chatID int64, resolve func(tg.PeerClass) domain.MessageTarget) {
	if msg == nil || msg.ReplyToMsgID == 0 {
		return
	}
	var header *tg.MessageReplyHeader
	switch raw := raw.(type) {
	case *tg.Message:
		header, _ = raw.ReplyTo.(*tg.MessageReplyHeader)
	case *tg.MessageService:
		header, _ = raw.ReplyTo.(*tg.MessageReplyHeader)
	}
	if header == nil {
		return
	}
	peer, ok := header.GetReplyToPeerID()
	if !ok || peerIDFromPeer(peer) == 0 || peerIDFromPeer(peer) == chatID {
		return
	}
	target := resolve(peer)
	if target.ChatID == 0 {
		target.ChatID = peerIDFromPeer(peer)
		target.Peer = domainPeer(peer)
		target.Title = fmt.Sprintf("Chat %d", target.ChatID)
	}
	target.MsgID = msg.ReplyToMsgID
	msg.ReplyTarget = &target
}

func domainPeer(peer tg.PeerClass) domain.Peer {
	switch peer := peer.(type) {
	case *tg.PeerUser:
		return domain.Peer{ID: peer.UserID, Type: domain.PeerUser}
	case *tg.PeerChat:
		return domain.Peer{ID: peer.ChatID, Type: domain.PeerGroup}
	case *tg.PeerChannel:
		return domain.Peer{ID: peer.ChannelID, Type: domain.PeerChannel}
	default:
		return domain.Peer{}
	}
}

func messageTargetFromEntities(entities tg.Entities, peer tg.PeerClass) domain.MessageTarget {
	id := peerIDFromPeer(peer)
	var chat domain.Chat
	var ok bool
	switch peer.(type) {
	case *tg.PeerUser:
		chat, ok = convertUser(entities.Users[id])
	case *tg.PeerChat:
		chat, ok = convertGroupChat(entities.Chats[id])
	case *tg.PeerChannel:
		chat, ok = convertChannel(entities.Channels[id])
	}
	if !ok {
		return domain.MessageTarget{}
	}
	return domain.MessageTarget{ChatID: chat.ID, Peer: chat.Peer, Title: chat.Title}
}

// photoSizeDimensions returns the downloadable size's type and dimensions.
// Progressive photos are ordinary download locations too; their Sizes field
// lists usable JPEG prefixes, not separate thumbnail types.
func photoSizeDimensions(size tg.PhotoSizeClass) (string, int, int, bool) {
	switch size := size.(type) {
	case *tg.PhotoSize:
		return size.Type, size.W, size.H, true
	case *tg.PhotoSizeProgressive:
		return size.Type, size.W, size.H, true
	default:
		return "", 0, 0, false
	}
}

// largestPhotoSize returns the downloadable PhotoSize type with the greatest
// pixel area.
func largestPhotoSize(sizes []tg.PhotoSizeClass) string {
	best := ""
	bestArea := 0
	for _, s := range sizes {
		typ, w, h, ok := photoSizeDimensions(s)
		if ok && w*h > bestArea {
			bestArea = w * h
			best = typ
		}
	}
	return best
}

// pickFullThumbSize returns the largest available PhotoSize type for full-quality download.
func pickFullThumbSize(sizes []tg.PhotoSizeClass) string {
	return largestPhotoSize(sizes)
}

// pickThumbSize returns the best thumb type string for inline display.
// Prefers "m" (320px), then largest available PhotoSize by area.
func pickThumbSize(sizes []tg.PhotoSizeClass) string {
	for _, s := range sizes {
		if typ, _, _, ok := photoSizeDimensions(s); ok && typ == "m" {
			return "m"
		}
	}
	return largestPhotoSize(sizes)
}

// classifyMedia maps a Telegram media object to a display-level MediaRef.
// Returns nil when there is no media to show.
func classifyMedia(media tg.MessageMediaClass) *domain.MediaRef {
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		return &domain.MediaRef{Kind: domain.MediaPhoto}
	case *tg.MessageMediaDocument:
		return classifyDocument(m)
	case *tg.MessageMediaWebPage:
		return nil
	case *tg.MessageMediaGeo, *tg.MessageMediaGeoLive, *tg.MessageMediaVenue:
		return &domain.MediaRef{Kind: domain.MediaLocation}
	case nil:
		return nil
	default:
		return &domain.MediaRef{Kind: domain.MediaOther}
	}
}

// classifyDocument inspects document attributes by strict priority to decide
// the media kind. A document can carry several attributes at once.
func classifyDocument(m *tg.MessageMediaDocument) *domain.MediaRef {
	doc, ok := m.Document.(*tg.Document)
	if !ok {
		return &domain.MediaRef{Kind: domain.MediaOther}
	}
	var (
		sticker  *tg.DocumentAttributeSticker
		video    *tg.DocumentAttributeVideo
		audio    *tg.DocumentAttributeAudio
		animated bool
	)
	for _, a := range doc.Attributes {
		switch at := a.(type) {
		case *tg.DocumentAttributeSticker:
			sticker = at
		case *tg.DocumentAttributeVideo:
			video = at
		case *tg.DocumentAttributeAudio:
			audio = at
		case *tg.DocumentAttributeAnimated:
			animated = true
		}
	}
	switch {
	case sticker != nil:
		return &domain.MediaRef{Kind: domain.MediaSticker, Emoji: sticker.Alt}
	case animated:
		return &domain.MediaRef{Kind: domain.MediaGIF}
	case video != nil && video.RoundMessage:
		return &domain.MediaRef{Kind: domain.MediaVideoNote, Duration: int(video.Duration)}
	case video != nil:
		return &domain.MediaRef{Kind: domain.MediaVideo, Duration: int(video.Duration)}
	case audio != nil && audio.Voice:
		return &domain.MediaRef{
			Kind:     domain.MediaVoice,
			Duration: audio.Duration,
			Waveform: audio.Waveform,
		}
	case audio != nil:
		return &domain.MediaRef{
			Kind:      domain.MediaAudio,
			Duration:  audio.Duration,
			Title:     audio.Title,
			Performer: audio.Performer,
		}
	default:
		return &domain.MediaRef{Kind: domain.MediaFile}
	}
}

// buildDocumentRef builds a download-capable reference from a Telegram document,
// picking the best thumbnail PhotoSize and the filename attribute when present.
func buildDocumentRef(doc *tg.Document) *domain.DocumentRef {
	ref := &domain.DocumentRef{
		ID:            doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
		DCID:          doc.DCID,
		MimeType:      doc.MimeType,
		Size:          doc.Size,
		ThumbSize:     pickThumbSize(doc.Thumbs),
	}
	for _, a := range doc.Attributes {
		if fn, ok := a.(*tg.DocumentAttributeFilename); ok {
			ref.FileName = fn.FileName
			break
		}
	}
	return ref
}

func convertMessage(raw tg.MessageClass, chatID int64) (domain.Message, bool) {
	if msg, ok := raw.(*tg.MessageService); ok {
		return convertServiceMessage(msg, chatID), true
	}
	msg, ok := raw.(*tg.Message)
	if !ok {
		return domain.Message{}, false
	}
	senderID := int64(0)
	if from, ok := msg.FromID.(*tg.PeerUser); ok {
		senderID = from.UserID
	}
	out := domain.Message{
		ID:       msg.ID,
		ChatID:   chatID,
		SenderID: senderID,
		Text:     msg.Message,
		Date:     time.Unix(int64(msg.Date), 0),
		IsOut:    msg.Out,
		Entities: convertEntities(msg.Entities),
	}
	_, out.HasWebPreview = msg.Media.(*tg.MessageMediaWebPage)
	out.Mentioned = msg.Mentioned
	out.GroupedID = msg.GroupedID
	out.Media = classifyMedia(msg.Media)
	if hdr, ok := msg.ReplyTo.(*tg.MessageReplyHeader); ok {
		out.ReplyToMsgID = hdr.ReplyToMsgID
		out.ThreadRootID = hdr.ReplyToTopID
	}
	if replies, ok := msg.GetReplies(); ok {
		out.HasComments = replies.Comments
		out.RepliesCount = replies.Replies
		if discussionChatID, ok := replies.GetChannelID(); ok {
			out.DiscussionChatID = discussionChatID
		}
	}
	// EditHide is set when edit_date changes for a non-content reason (e.g. a
	// reaction bump): Telegram tells clients to hide the "edited" label. Honor
	// it so reactions don't mark the message as edited (issue #118).
	if msg.EditDate != 0 && !msg.EditHide {
		t := time.Unix(int64(msg.EditDate), 0)
		out.EditDate = &t
	}
	if msg.Reactions.Results != nil {
		out.Reactions = convertReactions(msg.Reactions)
		out.HasUnreadReactions = reactionsHaveUnread(msg.Reactions)
	}
	if media, ok := msg.Media.(*tg.MessageMediaPhoto); ok {
		if photo, ok := media.Photo.(*tg.Photo); ok && len(photo.Sizes) > 0 {
			thumb := pickThumbSize(photo.Sizes)
			if thumb != "" {
				out.Photo = &domain.PhotoRef{
					ID:            photo.ID,
					AccessHash:    photo.AccessHash,
					FileReference: photo.FileReference,
					DCID:          photo.DCID,
					ThumbSize:     thumb,
					FullThumbSize: pickFullThumbSize(photo.Sizes),
				}
			}
		}
	}
	if media, ok := msg.Media.(*tg.MessageMediaDocument); ok {
		if doc, ok := media.Document.(*tg.Document); ok {
			out.Document = buildDocumentRef(doc)
			if out.Media != nil {
				out.Media.Size = doc.Size
				if out.Document.FileName != "" {
					out.Media.FileName = out.Document.FileName
				}
			}
		}
	}
	return out, true
}

func convertServiceMessage(msg *tg.MessageService, chatID int64) domain.Message {
	out := domain.Message{
		ID:        msg.ID,
		ChatID:    chatID,
		SenderID:  peerIDFromPeer(msg.FromID),
		Text:      serviceActionText(msg.Action),
		Date:      time.Unix(int64(msg.Date), 0),
		IsOut:     msg.Out,
		IsService: true,
		Mentioned: msg.Mentioned,
	}
	if hdr, ok := msg.ReplyTo.(*tg.MessageReplyHeader); ok {
		out.ReplyToMsgID = hdr.ReplyToMsgID
		out.ThreadRootID = hdr.ReplyToTopID
	}
	if msg.Reactions.Results != nil {
		out.Reactions = convertReactions(msg.Reactions)
		out.HasUnreadReactions = reactionsHaveUnread(msg.Reactions)
	}
	return out
}

func serviceActionText(action tg.MessageActionClass) string {
	switch a := action.(type) {
	case *tg.MessageActionChatCreate:
		return fmt.Sprintf("created the group %q", a.Title)
	case *tg.MessageActionChannelCreate:
		return fmt.Sprintf("created the channel %q", a.Title)
	case *tg.MessageActionChatEditTitle:
		return fmt.Sprintf("changed the title to %q", a.Title)
	case *tg.MessageActionTopicCreate:
		return fmt.Sprintf("created the topic %q", a.Title)
	case *tg.MessageActionTopicEdit:
		if a.Title != "" {
			return fmt.Sprintf("changed the topic title to %q", a.Title)
		}
	case *tg.MessageActionCustomAction:
		if a.Message != "" {
			return a.Message
		}
	}
	if action == nil {
		return "service message"
	}
	name := strings.TrimPrefix(action.TypeName(), "messageAction")
	if name == "" || name == "Empty" {
		return "service message"
	}
	runes := []rune(name)
	var text strings.Builder
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) &&
			(unicode.IsLower(runes[i-1]) || i+1 < len(runes) && unicode.IsLower(runes[i+1])) {
			text.WriteByte(' ')
		}
		text.WriteRune(unicode.ToLower(r))
	}
	return text.String()
}

func messageDate(raw tg.MessageClass) int {
	if msg, ok := raw.(interface{ GetDate() int }); ok {
		return msg.GetDate()
	}
	return 0
}

// reactionsHaveUnread reports whether any recent reaction on the message is
// flagged unread (a not-yet-viewed reaction on one of our messages).
func reactionsHaveUnread(mr tg.MessageReactions) bool {
	for _, r := range mr.RecentReactions {
		if r.Unread {
			return true
		}
	}
	return false
}

// newestUnreadReaction returns the emoji and timestamp of the most recent unread
// reaction on the message that was not sent by us. ok is false when there is no
// such reaction. A custom-emoji reaction yields ok=true with an empty emoji (it
// has no unicode emoticon to display).
func newestUnreadReaction(mr tg.MessageReactions) (emoji string, date time.Time, ok bool) {
	var best *tg.MessagePeerReaction
	for i := range mr.RecentReactions {
		r := &mr.RecentReactions[i]
		if !r.Unread || r.My {
			continue
		}
		if best == nil || r.Date > best.Date {
			best = r
		}
	}
	if best == nil {
		return "", time.Time{}, false
	}
	date = time.Unix(int64(best.Date), 0)
	if e, isEmoji := best.Reaction.(*tg.ReactionEmoji); isEmoji {
		return e.Emoticon, date, true
	}
	return "", date, true
}

func convertReactions(mr tg.MessageReactions) []domain.Reaction {
	out := make([]domain.Reaction, 0, len(mr.Results))
	for _, rc := range mr.Results {
		reaction := domain.Reaction{Count: rc.Count}
		switch v := rc.Reaction.(type) {
		case *tg.ReactionEmoji:
			reaction.Emoji = v.Emoticon
		case *tg.ReactionCustomEmoji:
			reaction.Emoji = "◈"
			reaction.CustomEmojiID = v.DocumentID
		default:
			continue
		}
		_, isChosen := rc.GetChosenOrder()
		reaction.IsChosen = isChosen
		out = append(out, reaction)
	}
	return out
}

func convertEntities(entities []tg.MessageEntityClass) []domain.MessageEntity {
	if len(entities) == 0 {
		return nil
	}
	out := make([]domain.MessageEntity, 0, len(entities))
	for _, e := range entities {
		switch v := e.(type) {
		case *tg.MessageEntityBold:
			out = append(out, domain.MessageEntity{Type: "bold", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityItalic:
			out = append(out, domain.MessageEntity{Type: "italic", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityCode:
			out = append(out, domain.MessageEntity{Type: "code", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityPre:
			out = append(out, domain.MessageEntity{Type: "pre", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityStrike:
			out = append(out, domain.MessageEntity{Type: "strike", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityUnderline:
			out = append(out, domain.MessageEntity{Type: "underline", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityTextURL:
			out = append(out, domain.MessageEntity{Type: "text_url", Offset: v.Offset, Length: v.Length, URL: v.URL})
		case *tg.MessageEntityURL:
			out = append(out, domain.MessageEntity{Type: "url", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityEmail:
			out = append(out, domain.MessageEntity{Type: "email", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityPhone:
			out = append(out, domain.MessageEntity{Type: "phone", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityBankCard:
			out = append(out, domain.MessageEntity{Type: "bank_card", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityMention:
			out = append(out, domain.MessageEntity{Type: "mention", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityMentionName:
			out = append(out, domain.MessageEntity{Type: "mention_name", Offset: v.Offset, Length: v.Length, UserID: v.UserID})
		case *tg.MessageEntityHashtag:
			out = append(out, domain.MessageEntity{Type: "hashtag", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityCashtag:
			out = append(out, domain.MessageEntity{Type: "cashtag", Offset: v.Offset, Length: v.Length})
		case *tg.MessageEntityBotCommand:
			out = append(out, domain.MessageEntity{Type: "bot_command", Offset: v.Offset, Length: v.Length})
		}
	}
	return out
}
