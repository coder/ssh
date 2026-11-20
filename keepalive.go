package ssh

import (
	"io"
	"log"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// SessionKeepAlive is the server keep-alive of one connection.
type SessionKeepAlive struct {
	clientAliveInterval time.Duration
	clientAliveCountMax int

	ticker       *time.Ticker
	lastReceived time.Time
	timedOut     bool

	metrics KeepAliveMetrics

	m      sync.Mutex
	closed bool
}

func NewSessionKeepAlive(clientAliveInterval time.Duration, clientAliveCountMax int) *SessionKeepAlive {
	// Stopped until setClientAliveLocked starts it, so Ticks never changes.
	ska := &SessionKeepAlive{ticker: time.NewTicker(time.Hour)}
	ska.ticker.Stop()
	ska.setClientAliveLocked(clientAliveInterval, clientAliveCountMax)
	return ska
}

// SetClientAlive overrides ClientAliveInterval and ClientAliveCountMax for
// this connection, like an sshd_config Match block, and restarts the count.
// A value of zero or less disables the keep-alive.
func (ska *SessionKeepAlive) SetClientAlive(clientAliveInterval time.Duration, clientAliveCountMax int) {
	ska.m.Lock()
	defer ska.m.Unlock()

	if !ska.closed {
		ska.setClientAliveLocked(clientAliveInterval, clientAliveCountMax)
	}
}

func (ska *SessionKeepAlive) setClientAliveLocked(clientAliveInterval time.Duration, clientAliveCountMax int) {
	if clientAliveInterval > 0 && clientAliveCountMax > 0 {
		ska.ticker.Reset(clientAliveInterval)
	} else {
		clientAliveInterval, clientAliveCountMax = 0, 0
		ska.ticker.Stop()
	}
	ska.clientAliveInterval = clientAliveInterval
	ska.clientAliveCountMax = clientAliveCountMax
	ska.lastReceived = time.Now()
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

	if ska.clientAliveInterval > 0 && !ska.closed {
		ska.lastReceived = time.Now()
		ska.ticker.Reset(ska.clientAliveInterval)
	}
}

func (ska *SessionKeepAlive) Ticks() <-chan time.Time {
	return ska.ticker.C
}

func (ska *SessionKeepAlive) TimeIsUp() bool {
	ska.m.Lock()
	defer ska.m.Unlock()

	return ska.timeIsUpLocked()
}

func (ska *SessionKeepAlive) timeIsUpLocked() bool {
	// Like OpenSSH, allow CountMax unanswered probes, one per interval.
	return ska.clientAliveInterval > 0 &&
		time.Since(ska.lastReceived) > time.Duration(ska.clientAliveCountMax+1)*ska.clientAliveInterval
}

// TimedOut reports whether the connection was closed because the client
// stopped answering keep-alives.
func (ska *SessionKeepAlive) TimedOut() bool {
	ska.m.Lock()
	defer ska.m.Unlock()

	return ska.timedOut
}

func (ska *SessionKeepAlive) markTimedOut() bool {
	ska.m.Lock()
	defer ska.m.Unlock()

	ska.timedOut = ska.timeIsUpLocked()
	return ska.timedOut
}

func (ska *SessionKeepAlive) Close() {
	ska.m.Lock()
	defer ska.m.Unlock()

	ska.ticker.Stop()
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

// handleKeepAlive probes the client on every tick for the whole connection
// and closes it once TimeIsUp.
func handleKeepAlive(done <-chan struct{}, keepAlive *SessionKeepAlive, conn gossh.Conn) {
	// One probe at a time; a dead client blocks it until the conn closes.
	var probing sync.Mutex
	for {
		select {
		case <-done:
			return
		case <-keepAlive.Ticks():
		}
		if keepAlive.markTimedOut() {
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
