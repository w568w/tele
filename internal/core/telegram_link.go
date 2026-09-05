package core

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	internaltg "github.com/sorokin-vladimir/tele/internal/tg"
)

// TelegramLink is one peer/message link tele can navigate internally.
type TelegramLink struct {
	Username  string
	ChannelID int64
	MsgID     int
	CommentID int
}

var reservedTelegramPaths = map[string]struct{}{
	"a": {}, "addemoji": {}, "addlist": {}, "addstickers": {}, "addstyle": {},
	"addtheme": {}, "auction": {}, "auth": {}, "boost": {}, "call": {},
	"confirmphone": {}, "contact": {}, "giftcode": {}, "invoice": {}, "joinchat": {},
	"k": {}, "login": {}, "m": {}, "nft": {}, "proxy": {}, "setlanguage": {},
	"share": {}, "socks": {}, "web": {}, "z": {},
}

// ResolveTelegramLink resolves the peer addressed by a parsed Telegram link.
func (o *Owner) ResolveTelegramLink(ctx context.Context, link TelegramLink) (domain.MessageTarget, error) {
	resolver, ok := o.client.(internaltg.LinkResolver)
	if !ok {
		return domain.MessageTarget{}, &telerr.Error{Kind: telerr.Internal, Op: "resolve link", Detail: "peer resolver unavailable"}
	}

	var chat domain.Chat
	var err error
	if link.Username != "" {
		chat, err = resolver.ResolveUsername(ctx, link.Username)
	} else if known, exists := o.reader().GetChat(link.ChannelID); exists && (known.Peer.IsChannel() || known.Peer.IsSuperGroup()) {
		chat = known
	} else {
		chat, err = resolver.ResolveChannel(ctx, link.ChannelID)
	}
	if err != nil {
		return domain.MessageTarget{}, err
	}
	if link.MsgID != 0 && !chat.Peer.IsChannel() && !chat.Peer.IsSuperGroup() {
		return domain.MessageTarget{}, &telerr.Error{Kind: telerr.NotFound, Op: "resolve link", Detail: "message links require a channel or group"}
	}
	o.rememberTransientChat(chat)
	return domain.MessageTarget{ChatID: chat.ID, Peer: chat.Peer, Title: chat.Title, MsgID: link.MsgID}, nil
}

// ParseTelegramLink accepts only peer/message links tele can represent fully.
// Unsupported Telegram features return ok=false and stay with the OS handler.
func ParseTelegramLink(raw string) (TelegramLink, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 3 && strings.EqualFold(raw[:3], "tg:") && !strings.HasPrefix(raw[3:], "//") {
		raw = "tg://" + raw[3:]
	}
	u, err := url.Parse(raw)
	if err != nil {
		return TelegramLink{}, false
	}
	switch strings.ToLower(u.Scheme) {
	case "tg":
		return parseTGNavigationLink(u)
	case "http", "https":
		return parseTelegramWebLink(u)
	default:
		return TelegramLink{}, false
	}
}

func parseTelegramWebLink(u *url.URL) (TelegramLink, bool) {
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	segments := linkPathSegments(u.Path)
	query := lowerQueryKeys(u.Query())
	if strings.HasSuffix(host, ".t.me") && host != "www.t.me" {
		username := strings.TrimSuffix(host, ".t.me")
		if len(username) == 1 || strings.Contains(username, ".") || !validTelegramUsername(username) || telegramPathReserved(username) {
			return TelegramLink{}, false
		}
		return parsePublicPeerLink(username, segments, query)
	}
	if host != "t.me" && host != "www.t.me" && host != "telegram.me" && host != "www.telegram.me" &&
		host != "telegram.dog" && host != "www.telegram.dog" {
		return TelegramLink{}, false
	}
	if len(segments) == 0 {
		return TelegramLink{}, false
	}

	switch strings.ToLower(segments[0]) {
	case "c":
		if len(segments) != 3 {
			return TelegramLink{}, false
		}
		channelID, ok := parsePositiveInt64(segments[1])
		if !ok {
			return TelegramLink{}, false
		}
		msgID, ok := parsePositiveInt(segments[2])
		if !ok {
			return TelegramLink{}, false
		}
		commentID, ok := parseMessageLinkQuery(query)
		return TelegramLink{ChannelID: channelID, MsgID: msgID, CommentID: commentID}, ok
	case "s":
		if len(segments) != 3 || !validTelegramUsername(segments[1]) || telegramPathReserved(segments[1]) {
			return TelegramLink{}, false
		}
		return parsePublicPeerLink(segments[1], segments[2:], query)
	default:
		if telegramPathReserved(segments[0]) {
			return TelegramLink{}, false
		}
		return parsePublicPeerLink(segments[0], segments[1:], query)
	}
}

func parsePublicPeerLink(username string, rest []string, query url.Values) (TelegramLink, bool) {
	if !validTelegramUsername(username) {
		return TelegramLink{}, false
	}
	if len(rest) == 0 {
		return TelegramLink{Username: username}, len(query) == 0
	}
	if len(rest) != 1 {
		return TelegramLink{}, false
	}
	msgID, ok := parsePositiveInt(rest[0])
	if !ok {
		return TelegramLink{}, false
	}
	commentID, ok := parseMessageLinkQuery(query)
	return TelegramLink{Username: username, MsgID: msgID, CommentID: commentID}, ok
}

func parseTGNavigationLink(u *url.URL) (TelegramLink, bool) {
	query := lowerQueryKeys(u.Query())
	switch strings.ToLower(u.Host) {
	case "resolve":
		username := query.Get("domain")
		if !validTelegramUsername(username) || telegramPathReserved(username) {
			return TelegramLink{}, false
		}
		post := query.Get("post")
		if post == "" {
			return TelegramLink{Username: username}, onlyQueryKeys(query, "domain")
		}
		msgID, ok := parsePositiveInt(post)
		if !ok || !onlyQueryKeys(query, "domain", "post", "single", "comment") {
			return TelegramLink{}, false
		}
		commentID, ok := parseOptionalComment(query)
		return TelegramLink{Username: username, MsgID: msgID, CommentID: commentID}, ok
	case "privatepost":
		channelID, ok := parsePositiveInt64(query.Get("channel"))
		if !ok {
			return TelegramLink{}, false
		}
		msgID, ok := parsePositiveInt(query.Get("post"))
		if !ok || !onlyQueryKeys(query, "channel", "post", "single", "comment") {
			return TelegramLink{}, false
		}
		commentID, ok := parseOptionalComment(query)
		return TelegramLink{ChannelID: channelID, MsgID: msgID, CommentID: commentID}, ok
	default:
		return TelegramLink{}, false
	}
}

func lowerQueryKeys(query url.Values) url.Values {
	result := make(url.Values, len(query))
	for key, values := range query {
		result[strings.ToLower(key)] = append(result[strings.ToLower(key)], values...)
	}
	return result
}

func parseMessageLinkQuery(query url.Values) (int, bool) {
	if !onlyQueryKeys(query, "single", "comment") {
		return 0, false
	}
	return parseOptionalComment(query)
}

func parseOptionalComment(query url.Values) (int, bool) {
	value := query.Get("comment")
	if value == "" {
		return 0, true
	}
	return parsePositiveInt(value)
}

func onlyQueryKeys(query url.Values, allowed ...string) bool {
	for key := range query {
		if !slices.Contains(allowed, strings.ToLower(key)) {
			return false
		}
	}
	return true
}

func linkPathSegments(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func telegramPathReserved(path string) bool {
	_, ok := reservedTelegramPaths[strings.ToLower(path)]
	return ok
}

func validTelegramUsername(username string) bool {
	if username == "" {
		return false
	}
	for _, r := range username {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func parsePositiveInt(value string) (int, bool) {
	n, err := strconv.Atoi(value)
	return n, err == nil && n > 0
}

func parsePositiveInt64(value string) (int64, bool) {
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil && n > 0
}
