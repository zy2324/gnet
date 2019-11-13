// Copyright 2019 Andy Pan. All rights reserved.
// Copyright 2018 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build windows

package gnet

import (
	"io"
	"net"
	"sync/atomic"
	"time"
)

type loop struct {
	ch    chan interface{} // command channel
	svr   *server
	idx   int               // loop index
	conns map[*stdConn]bool // track all the conns bound to this loop
}

func (lp *loop) loopRun() {
	var err error
	tick := make(chan bool)
	tock := make(chan time.Duration)
	defer func() {
		if lp.idx == 0 && lp.svr.opts.Ticker {
			close(tock)
			go func() {
				for range tick {
				}
			}()
		}
		lp.svr.signalShutdown(err)
		lp.svr.wg.Done()
		lp.loopEgress()
		lp.svr.wg.Done()
	}()
	if lp.idx == 0 && lp.svr.opts.Ticker {
		go func() {
			for {
				tick <- true
				delay, ok := <-tock
				if !ok {
					break
				}
				time.Sleep(delay)
			}
		}()
	}
	for {
		select {
		case <-tick:
			delay, action := lp.svr.eventHandler.Tick()
			switch action {
			case Shutdown:
				err = errClosing
			}
			tock <- delay
		case v := <-lp.ch:
			switch v := v.(type) {
			case error:
				err = v
			case *stdConn:
				switch v.proto {
				case TCP:
					if len(v.cache) == 0 {
						err = lp.loopAccept(v)
					} else {
						err = lp.loopRead(v)
					}
				case UDP:
					err = lp.loopReadUDP(v)
				}
			case *stderr:
				err = lp.loopError(v.c, v.err)
			case wakeReq:
				err = lp.loopRead(v.c)
			case func():
				v()
			}
		}
		if err != nil {
			return
		}
	}
}

func (lp *loop) loopAccept(c *stdConn) error {
	lp.conns[c] = true
	c.localAddr = lp.svr.ln.lnaddr
	c.remoteAddr = c.conn.RemoteAddr()

	out, action := lp.svr.eventHandler.OnOpened(c)
	if out != nil {
		lp.svr.eventHandler.PreWrite()
		_, _ = c.conn.Write(out)
	}
	if lp.svr.opts.TCPKeepAlive > 0 {
		if c, ok := c.conn.(*net.TCPConn); ok {
			_ = c.SetKeepAlive(true)
			_ = c.SetKeepAlivePeriod(lp.svr.opts.TCPKeepAlive)
		}
	}
	switch action {
	case Shutdown:
		return errClosing
	case Close:
		return lp.loopClose(c)
	}
	return nil
}

func (lp *loop) loopRead(c *stdConn) error {
loopReact:
	out, action := lp.svr.eventHandler.React(c)
	if len(out) != 0 {
		lp.svr.eventHandler.PreWrite()
		_, _ = c.conn.Write(out)
		goto loopReact
	}
	_, _ = c.inboundBuffer.Write(c.cache)
	switch action {
	case Shutdown:
		return errClosing
	case Close:
		return lp.loopClose(c)
	}
	return nil
}

func (lp *loop) loopClose(c *stdConn) error {
	atomic.StoreInt32(&c.done, 1)
	_ = c.conn.SetReadDeadline(time.Now())
	return nil
}

func (lp *loop) loopEgress() {
	var closed bool
loop:
	for v := range lp.ch {
		switch v := v.(type) {
		case error:
			if v == errCloseConns {
				closed = true
				for c := range lp.conns {
					_ = lp.loopClose(c)
				}
			}
		case *stderr:
			_ = lp.loopError(v.c, v.err)
		}
		if len(lp.conns) == 0 && closed {
			break loop
		}
	}
}

func (lp *loop) loopError(c *stdConn, err error) error {
	delete(lp.conns, c)
	switch atomic.LoadInt32(&c.done) {
	case 0: // read error
		_ = c.conn.Close()
		if err == io.EOF {
			err = nil
		}
	case 1: // closed
		_ = c.conn.Close()
		err = nil
	}
	switch lp.svr.eventHandler.OnClosed(c, err) {
	case Shutdown:
		return errClosing
	}
	return nil
}

func (lp *loop) loopReadUDP(c *stdConn) error {
	out, action := lp.svr.eventHandler.React(c)
	if out != nil {
		lp.svr.eventHandler.PreWrite()
		_, _ = lp.svr.ln.pconn.WriteTo(out, c.remoteAddr)
	}
	switch action {
	case Shutdown:
		return errClosing
	}
	return nil
}
