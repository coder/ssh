package ssh

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

func TestAddHostKey(t *testing.T) {
	s := Server{}
	signer, err := generateSigner()
	if err != nil {
		t.Fatal(err)
	}
	s.AddHostKey(signer)
	if len(s.HostSigners) != 1 {
		t.Fatal("Key was not properly added")
	}
	signer, err = generateSigner()
	if err != nil {
		t.Fatal(err)
	}
	s.AddHostKey(signer)
	if len(s.HostSigners) != 1 {
		t.Fatal("Key was not properly replaced")
	}
}

func TestServerShutdown(t *testing.T) {
	l := newLocalListener()
	testBytes := []byte("Hello world\n")
	s := &Server{
		Handler: func(s Session) {
			s.Write(testBytes)
			time.Sleep(50 * time.Millisecond)
		},
	}
	go func() {
		err := s.Serve(l)
		if err != nil && err != ErrServerClosed {
			t.Fatal(err)
		}
	}()
	sessDone := make(chan struct{})
	sess, _, cleanup := newClientSession(t, l.Addr().String(), nil)
	go func() {
		defer cleanup()
		defer close(sessDone)
		var stdout bytes.Buffer
		sess.Stdout = &stdout
		if err := sess.Run(""); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stdout.Bytes(), testBytes) {
			t.Fatalf("expected = %s; got %s", testBytes, stdout.Bytes())
		}
	}()

	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		err := s.Shutdown(context.Background())
		if err != nil {
			t.Fatal(err)
		}
	}()

	timeout := time.After(2 * time.Second)
	select {
	case <-timeout:
		t.Fatal("timeout")
		return
	case <-srvDone:
		// TODO: add timeout for sessDone
		<-sessDone
		return
	}
}

func TestServerClose(t *testing.T) {
	l := newLocalListener()
	s := &Server{
		Handler: func(s Session) {
			time.Sleep(5 * time.Second)
		},
	}
	go func() {
		err := s.Serve(l)
		if err != nil && err != ErrServerClosed {
			t.Fatal(err)
		}
	}()

	clientDoneChan := make(chan struct{})
	closeDoneChan := make(chan struct{})

	sess, _, cleanup := newClientSession(t, l.Addr().String(), nil)
	go func() {
		defer cleanup()
		defer close(clientDoneChan)
		<-closeDoneChan
		if err := sess.Run(""); err != nil && err != io.EOF {
			t.Fatal(err)
		}
	}()

	go func() {
		err := s.Close()
		if err != nil {
			t.Fatal(err)
		}
		close(closeDoneChan)
	}()

	timeout := time.After(100 * time.Millisecond)
	select {
	case <-timeout:
		t.Error("timeout")
		return
	case <-s.getDoneChan():
		<-clientDoneChan
		return
	}
}

func TestServerClose_ConnectionLeak(t *testing.T) {
	l := newLocalListener()
	s := &Server{
		Handler: func(s Session) {
			time.Sleep(5 * time.Second)
		},
	}
	go func() {
		err := s.Serve(l)
		if err != nil && err != ErrServerClosed {
			t.Error(err)
		}
	}()

	clientDoneChan := make(chan struct{})
	closeDoneChan := make(chan struct{})

	num := 3
	ch := make(chan struct{}, num)
	go func() {
		for i := 0; i < num; i++ {
			<-ch
		}
		close(clientDoneChan)
	}()
	prepare := make(chan struct{}, num)
	go func() {
		for i := 0; i < num; i++ {
			go func() {
				defer func() {
					ch <- struct{}{}
				}()
				sess, _, cleanup, err := newClientSession2(t, l.Addr().String(), nil)
				prepare <- struct{}{}
				if err != nil {
					t.Log(err)
					return
				}
				defer cleanup()
				if err := sess.Run(""); err != nil && err != io.EOF {
					t.Log(err)
				}
			}()
		}
	}()

	go func() {
		for i := 0; i < num-1; i++ {
			<-prepare
		}
		err := s.Close()
		if err != nil {
			t.Error(err)
		}
		close(closeDoneChan)
	}()

	timeout := time.After(1000 * time.Millisecond)
	select {
	case <-timeout:
		t.Error("timeout")
		return
	case <-closeDoneChan:
	}
	select {
	case <-timeout:
		t.Error("timeout")
		return
	case <-clientDoneChan:
	}
}

func newClientSession2(t *testing.T, addr string, config *gossh.ClientConfig) (*gossh.Session, *gossh.Client, func(), error) {
	t.Helper()

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
		return nil, nil, nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, nil, nil, err
	}
	return session, client, func() {
		session.Close()
		client.Close()
	}, nil
}
