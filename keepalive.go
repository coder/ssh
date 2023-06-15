package ssh

import (
	"log"
	"sync"
	"time"
)

type SessionKeepAlive struct {
	clientAliveInterval time.Duration
	clientAliveCountMax int

	ticker       *time.Ticker
	tickerCh     <-chan time.Time
	lastReceived time.Time

	metrics keepAliveMetrics

	m      sync.Mutex
	closed bool
}

type KeepAliveMetrics interface {
	RequestHandlerCalled() int
	KeepAliveReplyReceived() int
	ServerRequestedKeepAlive() int
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
	ska.metrics.requestHandlerCalled++
	ska.m.Unlock()

	log.Println("ska.RequestHandlerCallback()")

	ska.Reset()
}

func (ska *SessionKeepAlive) ServerRequestedKeepAliveCallback() {
	ska.m.Lock()
	defer ska.m.Unlock()

	// log.Println("ska.ServerRequestedKeepAliveCallback()")

	ska.metrics.serverRequestedKeepAlive++
}

func (ska *SessionKeepAlive) Reset() {
	ska.m.Lock()
	defer ska.m.Unlock()

	// log.Println("ska.Reset()")

	ska.metrics.keepAliveReplyReceived++

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

	// log.Println("ska.Close()")

	ska.ticker.Stop()
	ska.closed = true
}

func (ska *SessionKeepAlive) Metrics() KeepAliveMetrics {
	ska.m.Lock()
	defer ska.m.Unlock()

	kam := ska.metrics
	return &kam
}

type keepAliveMetrics struct {
	requestHandlerCalled     int
	keepAliveReplyReceived   int
	serverRequestedKeepAlive int
}

func (kam keepAliveMetrics) RequestHandlerCalled() int {
	return kam.requestHandlerCalled
}

func (kam keepAliveMetrics) KeepAliveReplyReceived() int {
	return kam.keepAliveReplyReceived
}

func (kam keepAliveMetrics) ServerRequestedKeepAlive() int {
	return kam.serverRequestedKeepAlive
}
