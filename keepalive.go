package ssh

import (
	"io"
	"log"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

type SessionKeepAlive struct {
	clientAliveInterval time.Duration
	clientAliveCountMax int

	ticker       *time.Ticker
	tickerCh     <-chan time.Time
	lastReceived time.Time

	metrics KeepAliveMetrics

	m      sync.Mutex
	closed bool
}

func NewSessionKeepAlive(clientAliveInterval time.Duration, clientAliveCountMax int) *SessionKeepAlive {
	var t *time.Ticker
	var tickerCh <-chan time.Time
	if clientAliveInterval > 0 {
		t = time.NewTicker(clientAliveInterval)
		tickerCh = t.C
	}

	return &SessionKeepAlive{
		clientAliveInterval: clientAliveInterval,
		clientAliveCountMax: clientAliveCountMax,
		ticker:              t,
		tickerCh:            tickerCh,
		lastReceived:        time.Now(),
	}
}

func (ska *SessionKeepAlive) RequestHandlerCallback() {
	ska.m.Lock()
	ska.metrics.RequestHandlerCalled++
	ska.m.Unlock()

	ska.Reset()
}

func (ska *SessionKeepAlive) ServerRequestedKeepAliveCallback() {
	ska.m.Lock()
	defer ska.m.Unlock()

	ska.metrics.ServerRequestedKeepAlive++
}

func (ska *SessionKeepAlive) Reset() {
	ska.m.Lock()
	defer ska.m.Unlock()

	ska.metrics.KeepAliveReplyReceived++

	if ska.ticker != nil && !ska.closed {
		ska.lastReceived = time.Now()
		ska.ticker.Reset(ska.clientAliveInterval)
	}
}

func (ska *SessionKeepAlive) Ticks() <-chan time.Time {
	return ska.tickerCh
}

func (ska *SessionKeepAlive) TimeIsUp() bool {
	ska.m.Lock()
	defer ska.m.Unlock()

	// true: Keep-alive reply not received
	return ska.lastReceived.Add(time.Duration(ska.clientAliveCountMax) * ska.clientAliveInterval).Before(time.Now())
}

func (ska *SessionKeepAlive) Close() {
	ska.m.Lock()
	defer ska.m.Unlock()

	if ska.ticker != nil {
		ska.ticker.Stop()
	}
	ska.closed = true
}

func (ska *SessionKeepAlive) Metrics() KeepAliveMetrics {
	ska.m.Lock()
	defer ska.m.Unlock()

	return ska.metrics
}

type KeepAliveMetrics struct {
	RequestHandlerCalled     int
	KeepAliveReplyReceived   int
	ServerRequestedKeepAlive int
}

// handleKeepAlive probes the client every ClientAliveInterval for the whole
// connection and closes it after ClientAliveCountMax intervals without a
// reply, like OpenSSH.
func handleKeepAlive(ctx Context, conn gossh.Conn) {
	keepAlive := ctx.KeepAlive()
	// One probe at a time; a dead client blocks it until the conn closes.
	var probing sync.Mutex
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepAlive.Ticks():
		}
		if keepAlive.TimeIsUp() {
			log.Println("Keep-alive reply not received. Close down the connection.")
			_ = conn.Close()
			return
		}
		if !probing.TryLock() {
			continue
		}
		go func() {
			defer probing.Unlock()
			// A success or failure reply both mean the client is alive.
			_, _, err := conn.SendRequest(keepAliveRequestType, true, nil)
			keepAlive.ServerRequestedKeepAliveCallback()
			if err == nil {
				keepAlive.Reset()
			} else if err != io.EOF {
				log.Printf("Sending keep-alive request failed: %v", err)
			}
		}()
	}
}
