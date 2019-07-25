// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnetxeth

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/platinasystems/vnet"
	"github.com/platinasystems/xeth"
)

type event struct {
	vnet.Event
	bufs []xeth.Buffer
}

var eventPool = sync.Pool{
	New: func() interface{} {
		return &event{
			bufs: make([]xeth.Buffer, 0, 1024),
		}
	},
}

var (
	eventActions uint64
)

func newEvent() *event {
	event := eventPool.Get().(*event)
	event.reset()
	return event
}

func goevents() {
	const (
		period = 100 * time.Millisecond
		wakeup = 20 * time.Second
	)
	vnet.WG.Add(1)
	defer vnet.WG.Done()
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	var idle time.Duration
	event := newEvent()
	for {
		select {
		case <-vnet.StopCh:
			return
		case buf, ok := <-this.xeth.RxCh:
			if !ok {
				return
			}
			event.bufs = append(event.bufs, buf)
			if len(event.bufs) == cap(event.bufs) {
				event = event.signal()
				idle = 0
			}
		case <-ticker.C:
			idle += period
			if len(event.bufs) > 0 || idle > wakeup {
				event = event.signal()
				idle = 0
			}
		}
	}
}

func (event *event) EventAction() {
	atomic.AddUint64(&eventActions, 1)
	this.rxmsg.Lock()
	defer this.rxmsg.Unlock()
	for _, buf := range event.bufs {
		msg := xeth.Parse(buf)
		for _, hook := range this.rxmsg.hooks {
			hook(msg)
		}
		xeth.Pool(msg)
	}
	event.pool()
}

func (event *event) String() string {
	return "xeth msg processor"
}

func (event *event) pool() {
	event.reset()
	eventPool.Put(event)
}

func (event *event) reset() {
	event.bufs = event.bufs[:0]
}

func (event *event) signal() *event {
	vnet.SignalEvent(event)
	return newEvent()
}
