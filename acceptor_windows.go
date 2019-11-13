// Copyright 2019 Andy Pan. All rights reserved.
// Copyright 2018 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build windows

package gnet

import (
	"time"

	"github.com/panjf2000/gnet/ringbuffer"
)

func (svr *server) listenerRun() {
	var err error
	defer svr.signalShutdown(err)
	var packet [0xFFFF]byte
	inBuf := ringbuffer.New(socketRingBufferSize)
	for {
		if svr.ln.pconn != nil {
			// udp
			n, addr, e := svr.ln.pconn.ReadFrom(packet[:])
			if err != nil {
				err = e
				return
			}
			lp := svr.subLoopGroup.next()
			c := &stdConn{
				localAddr:     svr.ln.lnaddr,
				remoteAddr:    addr,
				inboundBuffer: inBuf,
				cache:         append([]byte{}, packet[:n]...),
			}
			lp.ch <- &udpIn{c}
		} else {
			// tcp
			conn, e := svr.ln.ln.Accept()
			if err != nil {
				err = e
				return
			}
			lp := svr.subLoopGroup.next()
			sc := &stdConn{conn: conn, loop: lp, inboundBuffer: ringbuffer.New(socketRingBufferSize)}
			lp.ch <- sc
			go func(c *stdConn) {
				var packet [0xFFFF]byte
				for {
					n, err := c.conn.Read(packet[:])
					if err != nil {
						_ = c.conn.SetReadDeadline(time.Time{})
						lp.ch <- &stderr{c, err}
						return
					}
					lp.ch <- &tcpIn{c, append([]byte{}, packet[:n]...)}
				}
			}(sc)
		}
	}
}
