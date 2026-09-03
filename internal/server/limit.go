package server

import (
	"net"
	"sync"
)

// LimitListener wraps ln so that at most n connections accepted from it are
// open at once; Accept blocks while the cap is reached, so further peers
// queue in the kernel's listen backlog instead of each costing the server
// a goroutine, an fd, and a scanner buffer. Meant for the TCP listener,
// where the peers are not necessarily trusted; the Unix socket does not
// need it.
func LimitListener(ln net.Listener, n int) net.Listener {
	return &limitListener{
		Listener: ln,
		sem:      make(chan struct{}, n),
		done:     make(chan struct{}),
	}
}

type limitListener struct {
	net.Listener
	sem       chan struct{}
	closeOnce sync.Once
	done      chan struct{}
}

func (l *limitListener) Accept() (net.Conn, error) {
	select {
	case l.sem <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	nc, err := l.Listener.Accept()
	if err != nil {
		<-l.sem
		return nil, err
	}
	return &limitConn{Conn: nc, release: func() { <-l.sem }}, nil
}

func (l *limitListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// limitConn returns its slot to the listener when closed, once.
type limitConn struct {
	net.Conn
	releaseOnce sync.Once
	release     func()
}

func (c *limitConn) Close() error {
	err := c.Conn.Close()
	c.releaseOnce.Do(c.release)
	return err
}
