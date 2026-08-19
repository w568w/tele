package ui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sorokin-vladimir/tele/internal/ui"
	"github.com/stretchr/testify/assert"
)

type recordingInputMethod struct {
	states []bool
}

func (r *recordingInputMethod) SetNormal(normal bool) { r.states = append(r.states, normal) }

func TestRootNormalModeDrivesInputMethod(t *testing.T) {
	m, _ := newRootOnChat(t)
	recorder := &recordingInputMethod{}
	m = m.WithInputMethod(recorder)

	next, _ := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	m = next.(ui.RootModel)
	assert.Equal(t, []bool{false}, recorder.states)

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(ui.RootModel)
	assert.Equal(t, []bool{false, true}, recorder.states)
	next, _ = m.Update(tea.BlurMsg{})
	m = next.(ui.RootModel)
	assert.Equal(t, []bool{false, true, false}, recorder.states)
	next, _ = m.Update(tea.FocusMsg{})
	_ = next.(ui.RootModel)
	assert.Equal(t, []bool{false, true, false, true}, recorder.states)
}
