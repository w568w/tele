package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func TestKittyGraphemeWidthFilter(t *testing.T) {
	tests := []struct {
		capability string
		wantReport bool
	}{
		{"kitty-query-version=0.41.1", false},
		{"kitty-query-version=0.42.0", true},
		{"kitty-query-version=0.48.2", true},
		{"kitty-query-version=1.0.0", true},
		{"RGB", false},
	}

	for _, tt := range tests {
		t.Run(tt.capability, func(t *testing.T) {
			input := tea.CapabilityMsg{Content: tt.capability}
			msg := kittyGraphemeWidthFilter(nil, input)
			report, ok := msg.(tea.ModeReportMsg)
			assert.Equal(t, tt.wantReport, ok)
			if !ok {
				assert.Equal(t, input, msg)
				return
			}
			assert.Equal(t, ansi.ModeUnicodeCore, report.Mode)
			assert.Equal(t, ansi.ModePermanentlySet, report.Value)
		})
	}
}
