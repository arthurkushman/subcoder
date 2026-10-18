package tui

import (
	"context"

	"github.com/arthurkushman/subcoder/internal/permission"
	tea "github.com/charmbracelet/bubbletea"
)

type permissionMsg struct {
	req   permission.Request
	reply chan permission.Reply
}

type Asker struct {
	Program *tea.Program
	Send    func(tea.Msg)
}

func (a *Asker) deliver(msg tea.Msg) {
	if a.Program != nil {
		a.Program.Send(msg)
		return
	}
	if a.Send != nil {
		a.Send(msg)
	}
}

func (a *Asker) Ask(ctx context.Context, req permission.Request) (permission.Reply, error) {
	reply := make(chan permission.Reply, 1)
	a.deliver(permissionMsg{req: req, reply: reply})

	select {
	case r := <-reply:
		return r, nil
	case <-ctx.Done():
		return permission.Reply{}, ctx.Err()
	}
}
