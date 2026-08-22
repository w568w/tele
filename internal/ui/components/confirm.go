package components

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sorokin-vladimir/tele/internal/ui/theme"
)

// RenderConfirmBox renders a small modal for an explicit, consequential choice.
func RenderConfirmBox(title, body, confirmLabel string, maxW int) string {
	const padV, padH = 1, 2
	innerW := maxW - 2 - 2*padH
	if innerW < 20 {
		innerW = 20
	}

	body = theme.S().HelpDesc.Width(innerW).Render(body)
	footer := theme.S().HelpKey.Render("y/enter") + theme.Pad(1) +
		theme.S().HelpFaint.Render(confirmLabel+"  |  ") +
		theme.S().HelpKey.Render("n/esc") + theme.Pad(1) +
		theme.S().HelpFaint.Render("cancel")
	content := body + "\n\n" + footer
	lines := strings.Split(content, "\n")
	contentW := 0
	for _, line := range lines {
		if w := lipgloss.Width(line); w > contentW {
			contentW = w
		}
	}
	if contentW > innerW {
		contentW = innerW
	}
	padded := theme.S().HelpBg.Padding(padV, padH).Render(content)
	return RenderBox(padded, theme.S().HelpTitle.Render(title), "", "", "",
		lipgloss.RoundedBorder(), nil, contentW+2*padH+2, len(lines)+2*padV+2)
}
