// Copyright 2019 Andy Pan. All rights reserved.
// Copyright 2018 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// +build windows

package gnet

import (
	"errors"
	"runtime"
	"sync"
	"time"
)

var errClosing = errors.New("closing")
var errCloseConns = errors.New("close conns")

type server struct {
	ln               *listener          // all the listeners
	wg               sync.WaitGroup     // loop close waitgroup
	cond             *sync.Cond         // shutdown signaler
	opts             *Options           // options with server
	serr             error              // signal error
	once             sync.Once          // make sure only signalShutdown once
	codec            ICodec             // codec for TCP stream
	loops            []*loop            // all the loops
	ticktock         chan time.Duration // ticker channel
	accepted         uintptr            // accept counter
	eventHandler     EventHandler       // user eventHandler
	subLoopGroup     IEventLoopGroup    // loops for handling events
	subLoopGroupSize int                // number of loops
}

type stderr struct {
	c   *stdConn
	err error
}

// waitForShutdown waits for a signal to shutdown
func (svr *server) waitForShutdown() error {
	svr.cond.L.Lock()
	svr.cond.Wait()
	err := svr.serr
	svr.cond.L.Unlock()
	return err
}

// signalShutdown signals a shutdown an begins server closing
func (svr *server) signalShutdown(err error) {
	svr.once.Do(func() {
		svr.cond.L.Lock()
		svr.serr = err
		svr.cond.Signal()
		svr.cond.L.Unlock()
	})
}

func serve(eventHandler EventHandler, listener *listener, options *Options) error {
	// Figure out the correct number of loops/goroutines to use.
	var numCPU int
	if options.Multicore {
		numCPU = runtime.NumCPU()
	} else {
		numCPU = 1
	}

	svr := new(server)
	svr.eventHandler = eventHandler
	svr.ln = listener
	svr.cond = sync.NewCond(&sync.Mutex{})
	svr.ticktock = make(chan time.Duration, 1)
	svr.opts = options
	svr.subLoopGroup = new(eventLoopGroup)
	svr.codec = func() ICodec {
		if options.Codec == nil {
			return new(BuiltInFrameCodec)
		}
		return options.Codec
	}()

	server := Server{
		Multicore:    options.Multicore,
		Addr:         listener.lnaddr,
		NumLoops:     numCPU,
		ReUsePort:    options.ReusePort,
		TCPKeepAlive: options.TCPKeepAlive,
	}
	switch svr.eventHandler.OnInitComplete(server) {
	case None:
	case Shutdown:
		return nil
	}

	for i := 0; i < numCPU; i++ {
		lp := &loop{
			idx:   i,
			ch:    make(chan interface{}, 64),
			conns: make(map[*stdConn]bool),
			svr:   svr,
		}
		svr.subLoopGroup.register(lp)
	}
	svr.subLoopGroupSize = svr.subLoopGroup.len()

	var err error
	defer func() {
		// wait on a signal for shutdown
		err = svr.waitForShutdown()

		// notify all loops to close by closing all listeners
		svr.subLoopGroup.iterate(func(i int, lp *loop) bool {
			lp.ch <- errClosing
			return true
		})

		// wait on all loops to main loop channel eventHandler
		svr.wg.Wait()

		// close all connections
		svr.wg.Add(svr.subLoopGroupSize)
		svr.subLoopGroup.iterate(func(i int, lp *loop) bool {
			lp.ch <- errCloseConns
			return true
		})
		svr.wg.Wait()

	}()
	svr.wg.Add(svr.subLoopGroupSize)
	svr.subLoopGroup.iterate(func(i int, lp *loop) bool {
		go lp.loopRun()
		return true
	})
	go svr.listenerRun()
	return err
}
