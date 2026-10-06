package tui

import (
	"context"
	"errors"
	"sync"
)

// initGuard serialises `terraform init` and makes concurrent callers wait for
// the run in flight rather than starting a second one.
//
// Terraform's module installer writes into one shared .terraform/modules
// tree, so two overlapping inits clobber each other's git packfiles and both
// fail ("invalid index-pack output", "Module installation was canceled by an
// interrupt signal"). The TUI has three callers that can overlap — the P key,
// the debounced `terraform validate`, and `Apply` — and the window is widest
// on a cold cache, where one init fetches every module.
type initGuard struct {
	mu   sync.Mutex
	done bool

	// cur is the run in flight, or the one that just finished, so a waiter
	// released by it can still read its result. It is replaced only when a
	// new run starts.
	cur *initRun

	// needsUpgrade is set by reset and consumed by the next leader: a ref
	// switch changed only the ?ref= query, so the module must be re-fetched
	// with init -upgrade even though the source URL is unchanged.
	needsUpgrade bool

	// resetGen invalidates an in-flight run's result: reset bumped the
	// generation, so a leader that started before the reset must not mark the
	// wrapper initialised.
	resetGen uint64
}

// initRun is one `terraform init` execution and the result waiters read.
type initRun struct {
	ch       chan struct{}
	err      error
	finished bool
	gen      uint64 // resetGen the run started under
}

// initTicket is what begin hands back: whether to run init, whether init is
// already known to have succeeded, where to wait if not, and whether this run
// must be an -upgrade.
type initTicket struct {
	leader       bool
	done         bool
	wait         <-chan struct{}
	needsUpgrade bool
}

// begin reports whether the caller should run init. When it returns false,
// done reports that init already succeeded and the caller can proceed
// immediately; otherwise the caller must wait on the returned channel and
// then pass it to result.
func (g *initGuard) begin() initTicket {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done {
		return initTicket{done: true}
	}
	if g.cur != nil && !g.cur.finished {
		return initTicket{wait: g.cur.ch}
	}
	g.cur = &initRun{ch: make(chan struct{}), gen: g.resetGen}
	return initTicket{leader: true, wait: g.cur.ch, needsUpgrade: g.needsUpgrade}
}

// finish publishes the leader's result and releases every waiter. upgrade is
// the ticket's needsUpgrade: it is consumed only by a run that actually
// reached the wrapper, so a failed -upgrade still re-runs next time.
func (g *initGuard) finish(t initTicket, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cur == nil {
		return
	}
	if t.needsUpgrade && err == nil {
		g.needsUpgrade = false
	}
	g.cur.err = err
	g.cur.finished = true
	if err == nil && g.cur.gen == g.resetGen {
		g.done = true
	}
	close(g.cur.ch)
}

// result returns the leader's error to a waiter. errInitRestarted means this
// waiter's run was superseded and it should start a fresh one.
func (g *initGuard) result(wait <-chan struct{}) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cur == nil || g.cur.ch != wait {
		return errInitRestarted
	}
	return g.cur.err
}

// reset marks the wrapper uninitialised and invalidates the run in flight, so
// the next caller runs `terraform init -upgrade` against the new module source
// rather than trusting a run that started against the old one.
func (g *initGuard) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.done = false
	g.needsUpgrade = true
	g.resetGen++
}

// errInitRestarted tells a waiter to run init itself: a ref switch reset the
// guard mid-init, so the run it waited on no longer describes the wrapper.
var errInitRestarted = errors.New("init restarted; retry")

// maxInitAttempts bounds the retry loop. Each retry means a reset landed while
// this caller waited, so more than a couple means something is resetting in a
// loop and the caller should surface that rather than spin.
const maxInitAttempts = 3

// ensureInitOnce runs runInit at most once across concurrent callers: the first
// runs it and the rest wait for that result. A waiter whose run a reset
// invalidated retries as the new leader.
func ensureInitOnce(ctx context.Context, g *initGuard, runInit func(context.Context, bool) error) error {
	for range maxInitAttempts {
		t := g.begin()
		if t.done {
			return nil
		}
		if t.leader {
			err := runInit(ctx, t.needsUpgrade)
			g.finish(t, err)
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.wait:
			if err := g.result(t.wait); !errors.Is(err, errInitRestarted) {
				return err
			}
		}
	}
	return errors.New("terraform init: kept restarting, giving up")
}
