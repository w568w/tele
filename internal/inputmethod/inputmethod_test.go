package inputmethod

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakeFcitx struct {
	state int
	err   error
}

func (f *fakeFcitx) run(args ...string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if len(args) == 1 {
		return string(rune('0' + f.state)), nil
	}
	switch args[len(args)-1] {
	case "-o":
		f.state = 2
	case "-c":
		f.state = 1
	}
	return "", nil
}

func TestFcitx5NormalModeTemporarilyDisablesInputMethod(t *testing.T) {
	f := &fakeFcitx{state: 2}
	c := &fcitx5Controller{run: f.run}

	c.SetNormal(true)
	assert.Equal(t, 1, f.state, "normal mode starts in English")
	c.SetNormal(false)
	assert.Equal(t, 2, f.state, "leaving normal restores the prior state")

	f.state = 1
	c.SetNormal(true)
	f.state = 2
	c.SetNormal(false)
	assert.Equal(t, 1, f.state, "leaving normal restores an inactive prior state")

	f.state = 2
	c.SetNormal(true)
	assert.Equal(t, 1, f.state, "entering normal always returns to English")
	c.SetNormal(false)
	assert.Equal(t, 2, f.state, "leaving tele restores the state captured by normal mode")
}

func TestFcitx5UnavailableDoesNothing(t *testing.T) {
	for _, f := range []*fakeFcitx{{state: 0}, {state: 2, err: errors.New("dbus unavailable")}} {
		c := &fcitx5Controller{run: f.run}
		want := f.state
		c.SetNormal(true)
		c.SetNormal(false)
		assert.Equal(t, want, f.state)
	}
}
