package behaviortree

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type (
	// Ticker models a node runner
	Ticker interface {
		// Done will close when the ticker is fully stopped.
		Done() <-chan struct{}

		// Err will return any error that occurs.
		Err() error

		// Stop shutdown the ticker asynchronously.
		Stop()
	}

	// tickerCore is the base ticker implementation
	tickerCore struct {
		ctx    context.Context
		cancel context.CancelFunc
		node   Node
		ticker *time.Ticker
		done   chan struct{}
		stop   chan struct{}
		once   sync.Once
		mutex  sync.Mutex
		err    error
	}

	// tickerStopOnFailure is an implementation of a ticker that will run until the first error
	tickerStopOnFailure struct {
		Ticker
	}
)

var (
	// errExitOnFailure is a specific error used internally to exit tickers constructed with NewTickerStopOnFailure,
	// and won't be returned by the tickerStopOnFailure implementation
	errExitOnFailure = errors.New("errExitOnFailure")
)

// NewTicker constructs a new Ticker, which simply uses time.Ticker to tick the provided node periodically, note
// that a panic will occur if ctx is nil, duration is <= 0, or node is nil.
//
// The node will tick until the first error or Ticker.Stop is called, or context is canceled, after which any error
// will be made available via Ticker.Err, before closure of the done channel, indicating that all resources have been
// freed, and any error is available.
//
// Note that the ticker goroutine recovers from panics, which will be treated the same as an error case.
func NewTicker(ctx context.Context, duration time.Duration, node Node) Ticker {
	if ctx == nil {
		panic(errors.New("behaviortree.NewTicker nil context"))
	}

	if duration <= 0 {
		panic(errors.New("behaviortree.NewTicker duration <= 0"))
	}

	if node == nil {
		panic(errors.New("behaviortree.NewTicker nil node"))
	}

	result := &tickerCore{
		node:   node,
		ticker: time.NewTicker(duration),
		done:   make(chan struct{}),
		stop:   make(chan struct{}),
	}

	result.ctx, result.cancel = context.WithCancel(ctx)

	go result.run()

	return result
}

// NewTickerStopOnFailure returns a new Ticker that will exit on the first Failure, but won't return a non-nil Err
// UNLESS there was an actual error returned, it's built on top of the same core implementation provided by NewTicker,
// and uses that function directly, note that it will panic if the node is nil, the panic cases for NewTicker also
// apply.
func NewTickerStopOnFailure(ctx context.Context, duration time.Duration, node Node) Ticker {
	if node == nil {
		panic(errors.New("behaviortree.NewTickerStopOnFailure nil node"))
	}

	return tickerStopOnFailure{
		Ticker: NewTicker(
			ctx,
			duration,
			func() (Tick, []Node) {
				tick, children := node()
				if tick == nil {
					return nil, children
				}
				return func(children []Node) (Status, error) {
					status, err := tick(children)
					if err == nil && status == Failure {
						err = errExitOnFailure
					}
					return status, err
				}, children
			},
		),
	}
}

func (t *tickerCore) run() {
	defer close(t.done)
	defer t.cancel()
	defer t.Stop()
	var err error
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered from panic (%T): %+v", r, r)
		}
		t.mutex.Lock()
		defer t.mutex.Unlock()
		t.err = err
	}()
	for err == nil {
		select {
		case <-t.ctx.Done():
			err = t.ctx.Err()
			return
		case <-t.stop:
			return
		case <-t.ticker.C:
			_, err = t.node.Tick()
		}
	}
}

func (t *tickerCore) Done() <-chan struct{} {
	return t.done
}

func (t *tickerCore) Err() error {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.err
}

func (t *tickerCore) Stop() {
	t.once.Do(func() {
		t.ticker.Stop()
		close(t.stop)
	})
}

func (t tickerStopOnFailure) Err() error {
	err := t.Ticker.Err()
	if err == errExitOnFailure {
		return nil
	}
	return err
}