package ssh_server

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

func (srv *Server) serveOnce(l net.Listener) error {
	srv.ensureHandlers()
	if err := srv.ensureHostSigner(); err != nil {
		return err
	}
	conn, e := l.Accept()
	if e != nil {
		return e
	}
	srv.ChannelHandlers = map[string]ChannelHandler{
		"session":      DefaultSessionHandler,
		"direct-tcpip": DirectTCPIPHandler,
	}
	srv.HandleConn(conn)
	return nil
}

func newLocalListener() net.Listener {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if l, err = net.Listen("tcp6", "[::1]:0"); err != nil {
			panic(fmt.Sprintf("failed to listen on a port: %v", err))
		}
	}
	return l
}

func newClientSession(t *testing.T, addr string, config *gossh.ClientConfig) (*gossh.Session, *gossh.Client, func()) {
	if config == nil {
		config = &gossh.ClientConfig{
			User: "testuser",
			Auth: []gossh.AuthMethod{
				gossh.Password("testpass"),
			},
		}
	}
	if config.HostKeyCallback == nil {
		config.HostKeyCallback = gossh.InsecureIgnoreHostKey()
	}
	client, err := gossh.Dial("tcp", addr, config)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	return session, client, func() {
		session.Close()
		client.Close()
	}
}

func newTestSession(t *testing.T, srv *Server, cfg *gossh.ClientConfig) (*gossh.Session, *gossh.Client, func()) {
	l := newLocalListener()
	go srv.serveOnce(l)
	return newClientSession(t, l.Addr().String(), cfg)
}

func TestStdout(t *testing.T) {
	t.Parallel()
	testBytes := []byte("Hello world\n")
	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			s.Write(testBytes)
		},
	}, nil)
	defer cleanup()
	var stdout bytes.Buffer
	session.Stdout = &stdout
	if err := session.Run(""); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stdout.Bytes(), testBytes) {
		t.Fatalf("stdout = %#v; want %#v", stdout.Bytes(), testBytes)
	}
}

func TestStderr(t *testing.T) {
	t.Parallel()
	testBytes := []byte("Hello world\n")
	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			s.Stderr().Write(testBytes)
		},
	}, nil)
	defer cleanup()
	var stderr bytes.Buffer
	session.Stderr = &stderr
	if err := session.Run(""); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stderr.Bytes(), testBytes) {
		t.Fatalf("stderr = %#v; want %#v", stderr.Bytes(), testBytes)
	}
}

func TestStdin(t *testing.T) {
	t.Parallel()
	testBytes := []byte("Hello world\n")
	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			io.Copy(s, s) // stdin back into stdout
		},
	}, nil)
	defer cleanup()
	var stdout bytes.Buffer
	session.Stdout = &stdout
	session.Stdin = bytes.NewBuffer(testBytes)
	if err := session.Run(""); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stdout.Bytes(), testBytes) {
		t.Fatalf("stdout = %#v; want %#v given stdin = %#v", stdout.Bytes(), testBytes, testBytes)
	}
}

func TestUser(t *testing.T) {
	t.Parallel()
	testUser := []byte("progrium")
	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			io.WriteString(s, s.User())
		},
	}, &gossh.ClientConfig{
		User: string(testUser),
	})
	defer cleanup()
	var stdout bytes.Buffer
	session.Stdout = &stdout
	if err := session.Run(""); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stdout.Bytes(), testUser) {
		t.Fatalf("stdout = %#v; want %#v given user = %#v", stdout.Bytes(), testUser, string(testUser))
	}
}

func TestDefaultExitStatusZero(t *testing.T) {
	t.Parallel()
	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			// noop
		},
	}, nil)
	defer cleanup()
	err := session.Run("")
	if err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
}

func TestExplicitExitStatusZero(t *testing.T) {
	t.Parallel()
	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			s.Exit(0)
		},
	}, nil)
	defer cleanup()
	err := session.Run("")
	if err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
}

func TestExitStatusNonZero(t *testing.T) {
	t.Parallel()
	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			s.Exit(1)
		},
	}, nil)
	defer cleanup()
	err := session.Run("")
	e, ok := err.(*gossh.ExitError)
	if !ok {
		t.Fatalf("expected ExitError but got %T", err)
	}
	if e.ExitStatus() != 1 {
		t.Fatalf("exit-status = %#v; want %#v", e.ExitStatus(), 1)
	}
}

func TestPty(t *testing.T) {
	t.Parallel()
	term := "xterm"
	winWidth := 40
	winHeight := 80
	done := make(chan bool)
	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			ptyReq, _, isPty := s.Pty()
			if !isPty {
				t.Fatalf("expected pty but none requested")
			}
			if ptyReq.Term != term {
				t.Fatalf("expected term %#v but got %#v", term, ptyReq.Term)
			}
			if ptyReq.Window.Width != winWidth {
				t.Fatalf("expected window width %#v but got %#v", winWidth, ptyReq.Window.Width)
			}
			if ptyReq.Window.Height != winHeight {
				t.Fatalf("expected window height %#v but got %#v", winHeight, ptyReq.Window.Height)
			}
			close(done)
		},
	}, nil)
	defer cleanup()
	if err := session.RequestPty(term, winHeight, winWidth, gossh.TerminalModes{}); err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
	if err := session.Shell(); err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
	<-done
}

func TestX11(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	session, _, cleanup := newTestSession(t, &Server{
		X11Callback: func(ctx Context, x11 X11) bool {
			return true
		},
		Handler: func(s Session) {
			x11Req, isX11 := s.X11()
			if !isX11 {
				t.Fatalf("expected x11 but none requested")
			}
			if !x11Req.SingleConnection {
				t.Fatalf("expected single connection but got %#v", x11Req.SingleConnection)
			}
			close(done)
		},
	}, nil)
	defer cleanup()

	reply, err := session.SendRequest("x11-req", true, gossh.Marshal(X11{
		SingleConnection: true,
		AuthProtocol:     "MIT-MAGIC-COOKIE-1",
		AuthCookie:       "deadbeef",
		ScreenNumber:     1,
	}))
	if err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
	if !reply {
		t.Fatalf("expected true but got %v", reply)
	}
	err = session.Shell()
	if err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
	session.Close()
	<-done
}

func TestPtyResize(t *testing.T) {
	t.Skip("it hangs")

	t.Parallel()
	winch0 := Window{40, 80, 0, 0}
	winch1 := Window{80, 160, 0, 0}
	winch2 := Window{20, 40, 0, 0}
	winches := make(chan Window)
	done := make(chan bool)
	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			ptyReq, winCh, isPty := s.Pty()
			if !isPty {
				t.Fatalf("expected pty but none requested")
			}
			if ptyReq.Window != winch0 {
				t.Fatalf("expected window %#v but got %#v", winch0, ptyReq.Window)
			}
			for win := range winCh {
				winches <- win
			}
			close(done)
		},
	}, nil)
	defer cleanup()
	// winch0
	if err := session.RequestPty("xterm", winch0.Height, winch0.Width, gossh.TerminalModes{}); err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
	if err := session.Shell(); err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
	gotWinch := <-winches
	if gotWinch != winch0 {
		t.Fatalf("expected window %#v but got %#v", winch0, gotWinch)
	}
	// winch1
	winchMsg := struct{ w, h uint32 }{uint32(winch1.Width), uint32(winch1.Height)}
	ok, err := session.SendRequest("window-change", true, gossh.Marshal(&winchMsg))
	if err == nil && !ok {
		t.Fatalf("unexpected error or bad reply on send request")
	}
	gotWinch = <-winches
	if gotWinch != winch1 {
		t.Fatalf("expected window %#v but got %#v", winch1, gotWinch)
	}
	// winch2
	winchMsg = struct{ w, h uint32 }{uint32(winch2.Width), uint32(winch2.Height)}
	ok, err = session.SendRequest("window-change", true, gossh.Marshal(&winchMsg))
	if err == nil && !ok {
		t.Fatalf("unexpected error or bad reply on send request")
	}
	gotWinch = <-winches
	if gotWinch != winch2 {
		t.Fatalf("expected window %#v but got %#v", winch2, gotWinch)
	}
	session.Close()
	<-done
}

func TestSignals(t *testing.T) {
	t.Parallel()

	// errChan lets us get errors back from the session
	errChan := make(chan error, 5)

	// doneChan lets us specify that we should exit.
	doneChan := make(chan interface{})

	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			// We need to use a buffered channel here, otherwise it's possible for the
			// second call to Signal to get discarded.
			signals := make(chan Signal, 2)
			s.Signals(signals)

			select {
			case sig := <-signals:
				if sig != SIGINT {
					errChan <- fmt.Errorf("expected signal %v but got %v", SIGINT, sig)
					return
				}
			case <-doneChan:
				errChan <- fmt.Errorf("Unexpected done")
				return
			}

			select {
			case sig := <-signals:
				if sig != SIGKILL {
					errChan <- fmt.Errorf("expected signal %v but got %v", SIGKILL, sig)
					return
				}
			case <-doneChan:
				errChan <- fmt.Errorf("Unexpected done")
				return
			}
		},
	}, nil)
	defer cleanup()

	go func() {
		session.Signal(gossh.SIGINT)
		session.Signal(gossh.SIGKILL)
	}()

	go func() {
		errChan <- session.Run("")
	}()

	err := <-errChan
	close(doneChan)

	if err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
}

func TestSignalsRaceDeregisterAndReregister(t *testing.T) {
	t.Parallel()

	numSignals := 128

	// errChan lets us get errors back from the session
	errChan := make(chan error, 5)

	// doneChan lets us specify that we should exit.
	doneChan := make(chan interface{})

	// Channels to synchronize the handler and the test.
	handlerPreRegister := make(chan struct{})
	handlerPostRegister := make(chan struct{})
	signalInit := make(chan struct{})

	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			// Single buffer slot, this is to make sure we don't miss
			// signals or send on nil a channel.
			signals := make(chan Signal, 1)

			<-handlerPreRegister // Wait for initial signal buffering.

			// Register signals.
			s.Signals(signals)
			close(handlerPostRegister) // Trigger post register signaling.

			// Process signals so that we can don't see a deadlock.
			discarded := 0
			discardDone := make(chan struct{})
			go func() {
				defer close(discardDone)
				for range signals {
					discarded++
				}
			}()
			// Deregister signals.
			s.Signals(nil)
			// Close channel to close goroutine and ensure we don't send
			// on a closed channel.
			close(signals)
			<-discardDone

			signals = make(chan Signal, 1)
			consumeDone := make(chan struct{})
			go func() {
				defer close(consumeDone)

				for i := 0; i < numSignals-discarded; i++ {
					select {
					case sig := <-signals:
						if sig != SIGHUP {
							errChan <- fmt.Errorf("expected signal %v but got %v", SIGHUP, sig)
							return
						}
					case <-doneChan:
						errChan <- fmt.Errorf("Unexpected done")
						return
					}
				}
			}()

			// Re-register signals and make sure we don't miss any.
			s.Signals(signals)
			close(signalInit)

			<-consumeDone
		},
	}, nil)
	defer cleanup()

	go func() {
		// Send 1/4th directly to buffer.
		for i := 0; i < numSignals/4; i++ {
			session.Signal(gossh.SIGHUP)
		}
		close(handlerPreRegister)
		<-handlerPostRegister
		// Send 1/4th to channel or buffer.
		for i := 0; i < numSignals/4; i++ {
			session.Signal(gossh.SIGHUP)
		}
		// Send final 1/2 to channel.
		<-signalInit
		for i := 0; i < numSignals/2; i++ {
			session.Signal(gossh.SIGHUP)
		}
	}()

	go func() {
		errChan <- session.Run("")
	}()

	select {
	case err := <-errChan:
		close(doneChan)
		if err != nil {
			t.Fatalf("expected nil but got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for session to exit")
	}
}

func TestBreakWithChanRegistered(t *testing.T) {
	t.Parallel()

	// errChan lets us get errors back from the session
	errChan := make(chan error, 5)

	// doneChan lets us specify that we should exit.
	doneChan := make(chan interface{})

	breakChan := make(chan bool)

	readyToReceiveBreak := make(chan bool)

	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			s.Break(breakChan) // register a break channel with the session
			readyToReceiveBreak <- true

			select {
			case <-breakChan:
				io.WriteString(s, "break")
			case <-doneChan:
				errChan <- fmt.Errorf("Unexpected done")
				return
			}
		},
	}, nil)
	defer cleanup()
	var stdout bytes.Buffer
	session.Stdout = &stdout
	go func() {
		errChan <- session.Run("")
	}()

	<-readyToReceiveBreak
	ok, err := session.SendRequest("break", true, nil)
	if err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
	if ok != true {
		t.Fatalf("expected true but got %v", ok)
	}

	err = <-errChan
	close(doneChan)

	if err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
	if !bytes.Equal(stdout.Bytes(), []byte("break")) {
		t.Fatalf("stdout = %#v, expected 'break'", stdout.Bytes())
	}
}

func TestBreakWithoutChanRegistered(t *testing.T) {
	t.Parallel()

	// errChan lets us get errors back from the session
	errChan := make(chan error, 5)

	// doneChan lets us specify that we should exit.
	doneChan := make(chan interface{})

	waitUntilAfterBreakSent := make(chan bool)

	session, _, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {
			<-waitUntilAfterBreakSent
		},
	}, nil)
	defer cleanup()
	var stdout bytes.Buffer
	session.Stdout = &stdout
	go func() {
		errChan <- session.Run("")
	}()

	ok, err := session.SendRequest("break", true, nil)
	if err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
	if ok != false {
		t.Fatalf("expected false but got %v", ok)
	}
	waitUntilAfterBreakSent <- true

	err = <-errChan
	close(doneChan)
	if err != nil {
		t.Fatalf("expected nil but got %v", err)
	}
}

func TestSessionKeepAlive(t *testing.T) {
	t.Parallel()

	t.Run("Server replies to keep-alive request", func(t *testing.T) {
		t.Parallel()

		doneCh := make(chan struct{})
		defer close(doneCh)

		var sshSession *session
		srv := &Server{
			ClientAliveInterval: 100 * time.Millisecond,
			ClientAliveCountMax: 2,
			Handler: func(s Session) {
				<-doneCh
			},
			SessionRequestCallback: func(sess Session, requestType string) bool {
				sshSession = sess.(*session)
				return true
			},
		}
		session, client, cleanup := newTestSession(t, srv, nil)
		defer cleanup()

		errChan := make(chan error, 1)
		go func() {
			errChan <- session.Run("")
		}()

		for i := 0; i < 100; i++ {
			ok, reply, err := client.SendRequest(keepAliveRequestType, true, nil)
			require.NoError(t, err)
			require.False(t, ok) // server replied
			require.Empty(t, reply)

			time.Sleep(10 * time.Millisecond)
		}
		doneCh <- struct{}{}

		err := <-errChan
		if err != nil {
			t.Fatalf("expected nil but got %v", err)
		}

		// Verify that...
		require.Equal(t, 100, sshSession.ctx.KeepAlive().Metrics().RequestHandlerCalled)   // client sent keep-alive requests,
		require.Equal(t, 100, sshSession.ctx.KeepAlive().Metrics().KeepAliveReplyReceived) // and server replied to all of them,
		require.Zero(t, sshSession.ctx.KeepAlive().Metrics().ServerRequestedKeepAlive)     // and server didn't send any extra requests.
	})

	t.Run("Server requests keep-alive reply", func(t *testing.T) {
		t.Parallel()

		doneCh := make(chan struct{})
		defer close(doneCh)

		var sshSession *session
		var m sync.Mutex
		srv := &Server{
			ClientAliveInterval: 100 * time.Millisecond,
			ClientAliveCountMax: 2,
			Handler: func(s Session) {
				<-doneCh
			},
			SessionRequestCallback: func(sess Session, requestType string) bool {
				m.Lock()
				defer m.Unlock()

				sshSession = sess.(*session)
				return true
			},
		}
		session, _, cleanup := newTestSession(t, srv, nil)
		defer cleanup()

		errChan := make(chan error, 1)
		go func() {
			errChan <- session.Run("")
		}()

		// Wait for client to reply to at least 10 keep-alive requests.
		assert.Eventually(t, func() bool {
			m.Lock()
			defer m.Unlock()

			return sshSession != nil && sshSession.ctx.KeepAlive().Metrics().KeepAliveReplyReceived >= 10
		}, time.Second*3, time.Millisecond)
		require.GreaterOrEqual(t, 10, sshSession.ctx.KeepAlive().Metrics().KeepAliveReplyReceived)

		doneCh <- struct{}{}
		err := <-errChan
		if err != nil {
			t.Fatalf("expected nil but got %v", err)
		}

		// Verify that...
		require.Zero(t, sshSession.ctx.KeepAlive().Metrics().RequestHandlerCalled)                   // client didn't send any keep-alive requests,
		require.GreaterOrEqual(t, 10, sshSession.ctx.KeepAlive().Metrics().ServerRequestedKeepAlive) //  server requested keep-alive replies
	})

	t.Run("Server terminates connection due to no keep-alive replies", func(t *testing.T) {
		t.Parallel()
		t.Skip("Go SSH client doesn't support disabling replies to keep-alive requests. We can't test it easily without mocking logic.")
	})
}
