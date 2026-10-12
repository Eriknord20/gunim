package filemanager

import (
	"context"
	"errors"

	"github.com/marrasen/gunim"
)

// PasswordAsk is what a password is asked for: the zip at Path on the
// file system of ID FS, which Where names as the window writes it.
type PasswordAsk struct {
	FS, Path, Where string
	// Make says the password is to protect a zip being made, and is
	// typed twice; without it, the zip is protected already, and the
	// password opens it.
	Make bool
	// Wrong says the last password given did not open the zip.
	Wrong bool
}

// Password answers a PasswordAsk.
type Password struct {
	Text string
	// Worked, when set, is called once the password proved right: what
	// it protects opened with it, or the zip it protects was made. A
	// program that offered to keep a password typed keeps it then.
	Worked func()
}

// errNoPassword is why an operation stops when the user gives no
// password.
var errNoPassword = errors.New("no password was given")

// PasswordPrompt is the state of the window's own dialog that asks for
// a password, for an application with no Options.Password.
type PasswordPrompt struct {
	Token       int
	Title, Text string
	OK          string
	// Make asks for the password twice, to protect a zip being made.
	Make bool
	// Problem says, in the colour of a failure, why it is asked again.
	Problem string
}

// PasswordGiven answers a PasswordPrompt: OK with the password typed,
// or not where the user cancelled.
type PasswordGiven struct {
	Token int
	Text  string
	OK    bool
}

func init() {
	gunim.RegisterType[PasswordPrompt]("files.password")
	gunim.RegisterType[PasswordGiven]("files.password-given")
}

// askPassword asks for the password ask is about: through the program,
// where it asks for passwords, and else in a dialog of the window's, of
// operation op, which goes when the operation ends, or of none for 0. It
// may run on any goroutine, and waits for the answer or the end of ctx.
func (a *app) askPassword(ctx context.Context, op int, ask PasswordAsk) (Password, error) {
	if a.opts.Password != nil {
		return a.opts.Password(ctx, a.win, ask)
	}
	reply := make(chan PasswordGiven, 1)
	a.post(func() {
		name := a.ps.Base(ask.Path)
		p := PasswordPrompt{Title: "Password for “" + name + "”", OK: "Open",
			Text: name + " is protected with a password. Type it to open what it holds."}
		if ask.Make {
			p = PasswordPrompt{Title: "Protect “" + name + "”", OK: "Protect", Make: true,
				Text: "Type a password twice. What the zip holds opens only with it; the names of the files in it stay readable."}
		}
		if ask.Wrong {
			p.Problem = "That password is wrong."
		}
		a.ops.tokens++
		p.Token = a.ops.tokens
		a.showDialog(&dialog{view: "password", state: p, op: op, answer: func(in gunim.Intent) {
			v, _ := in.(PasswordGiven)
			reply <- v
		}})
	})
	select {
	case v := <-reply:
		if !v.OK {
			return Password{}, errNoPassword
		}
		return Password{Text: v.Text}, nil
	case <-ctx.Done():
		return Password{}, ctx.Err()
	}
}

// untilStopped returns a context that ends with the app's, or once the
// app stops, as when its window or pane closes, which the app's own does
// not; cancel lets it go sooner.
func (a *app) untilStopped() (ctx context.Context, cancel context.CancelFunc) {
	ctx, cancel = context.WithCancel(a.ctx)
	go func() {
		select {
		case <-a.stopped:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// passwords hands an extraction the password of the archive it opens:
// the one given last, or a new one asked for when there is none yet or
// it was wrong. worked says the last one opened what it protects.
type passwords struct {
	ask   func(ctx context.Context, wrong bool) (Password, error)
	got   *Password
	wrong bool
	// proven says an item read through with the password checked.
	proven bool
}

// asks reports whether get asks for a password.
func (p *passwords) asks() bool { return p.got == nil || p.wrong }

// get returns the password to try.
func (p *passwords) get(ctx context.Context) (string, error) {
	if !p.asks() {
		return p.got.Text, nil
	}
	if p.ask == nil {
		return "", errNoPassword
	}
	got, err := p.ask(ctx, p.wrong)
	if err != nil {
		return "", err
	}
	p.got, p.wrong = &got, false
	return got.Text, nil
}

// failed says the password given last was wrong.
func (p *passwords) failed() { p.wrong, p.proven = true, false }

// worked says the password given last opened what it protects.
func (p *passwords) worked() {
	if p.got != nil && p.got.Worked != nil {
		p.got.Worked()
		p.got.Worked = nil
	}
}
