package ssh

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionKeepAliveSetClientAlive(t *testing.T) {
	t.Parallel()

	const interval = 10 * time.Millisecond
	for _, tc := range []struct {
		name    string
		apply   func(*SessionKeepAlive)
		enabled bool
	}{
		{"Enabled", func(ka *SessionKeepAlive) { ka.SetClientAlive(interval, 1) }, true},
		{"OnlyInterval", func(ka *SessionKeepAlive) { ka.SetClientAlive(interval, 0) }, false},
		{"Disabled", func(ka *SessionKeepAlive) { ka.SetClientAlive(interval, 1); ka.SetClientAlive(0, 0) }, false},
		{"AfterClose", func(ka *SessionKeepAlive) { ka.Close(); ka.SetClientAlive(interval, 1) }, false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			keepAlive := NewSessionKeepAlive(0, 0)
			defer keepAlive.Close()
			tc.apply(keepAlive)

			time.Sleep(3 * interval)
			var ticked bool
			select {
			case <-keepAlive.Ticks():
				ticked = true
			default:
			}
			require.Equal(t, tc.enabled, ticked)
			require.Equal(t, tc.enabled, keepAlive.TimeIsUp())
		})
	}
}
