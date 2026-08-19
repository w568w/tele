package inputmethod

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Controller temporarily disables Fcitx while tele is focused in normal mode.
type Controller interface {
	SetNormal(bool)
}

type nopController struct{}

func (nopController) SetNormal(bool) {}

type commandRunner func(args ...string) (string, error)

type fcitx5Controller struct {
	run    commandRunner
	normal bool
	last   bool
}

// New detects Fcitx5 and otherwise returns a no-op controller.
func New() Controller {
	path, err := exec.LookPath("fcitx5-remote")
	if err != nil {
		return nopController{}
	}
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		out, err := exec.CommandContext(ctx, path, args...).Output()
		return string(out), err
	}
	return &fcitx5Controller{run: run}
}

func (c *fcitx5Controller) SetNormal(normal bool) {
	if c.normal == normal {
		return
	}
	if !normal {
		c.setActive(c.last)
		c.normal = false
		return
	}
	active, ok := c.readActive()
	if !ok {
		return
	}
	c.normal = true
	c.last = active
	c.setActive(false)
}

func (c *fcitx5Controller) readActive() (bool, bool) {
	out, err := c.run("--check")
	if err != nil {
		return false, false
	}
	state := strings.TrimSpace(out)
	return state == "2", state == "1" || state == "2"
}

func (c *fcitx5Controller) setActive(active bool) {
	arg := "-c"
	if active {
		arg = "-o"
	}
	_, _ = c.run("--check", arg)
}
