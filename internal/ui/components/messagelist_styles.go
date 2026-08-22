package components

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/ui/theme"
)

// senderPaletteColor is the colour this person is drawn in, picked from
// sender_palette by id. It lives on its own so that everything answering "which
// colour is this person" answers the same: a name in a chat and a monogram in
// the profile overlay must not disagree about someone (#223). Reports false
// when the theme sets no palette, which is a legal theme.
func senderPaletteColor(userID int64) (color.Color, bool) {
	pal := theme.T().SenderPalette
	if len(pal) == 0 {
		return nil, false
	}
	idx := userID % int64(len(pal))
	if idx < 0 {
		idx = -idx
	}
	return pal[idx], true
}

func (ml *MessageList) senderNameStyle(senderID int64) lipgloss.Style {
	c, ok := senderPaletteColor(senderID)
	if !ok {
		return theme.S().BodyBold
	}
	return theme.NewStyle().Foreground(c).Bold(true)
}

func buildReactContent(reactions []domain.Reaction) string {
	if len(reactions) == 0 {
		return ""
	}
	parts := make([]string, 0, len(reactions))
	for _, r := range reactions {
		s := r.Emoji + " " + strconv.Itoa(r.Count)
		if r.IsChosen {
			parts = append(parts, theme.S().TickRead.Render(s))
		} else {
			parts = append(parts, theme.S().Timestamp.Render(s))
		}
	}
	sep := theme.S().Timestamp.Render(" · ")
	return strings.Join(parts, sep)
}

func buildMessageMeta(msg domain.Message) string {
	parts := make([]string, 0, 2)
	if msg.HasComments {
		parts = append(parts, theme.S().Timestamp.Render("💬 "+strconv.Itoa(msg.RepliesCount)))
	}
	if reactions := buildReactContent(msg.Reactions); reactions != "" {
		parts = append(parts, reactions)
	}
	if len(parts) == 0 {
		return ""
	}
	return theme.Pad(1) + strings.Join(parts, theme.S().Timestamp.Render(" · ")) + theme.Pad(1)
}
