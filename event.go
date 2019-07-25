// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnet

import (
	"github.com/platinasystems/elib/elog"
	"github.com/platinasystems/elib/event"
	"github.com/platinasystems/elib/loop"
)

var eventNode struct{ Node }

func eventNodeInit() {
	vnet.loop.RegisterNode(&eventNode, "vnet")
}

type Event struct {
	loop.Event
	n *Node
}

func (e *Event) Node() *Node      { return e.n }
func (e *Event) GetEvent() *Event { return e }

type Eventer interface {
	GetEvent() *Event
	event.Actor
}

func (n *Node) SignalEventp(r Eventer, p elog.PointerToFirstArg) {
	e := r.GetEvent()
	e.n = n
	n.Node.SignalEventp(r, &eventNode, p)
}

func (n *Node) SignalEvent(r Eventer) {
	n.SignalEventp(r, elog.PointerToFirstArg(&n))
}

func (n *Node) SignalEventAfterp(r Eventer, dt float64, p elog.PointerToFirstArg) {
	e := r.GetEvent()
	e.n = n
	n.Node.SignalEventAfterp(r, &eventNode, dt, p)
}

func (n *Node) SignalEventAfter(r Eventer, dt float64) {
	n.SignalEventAfterp(r, dt, elog.PointerToFirstArg(&n))
}

func (e *Event) SignalEvent(r Eventer) {
	e.n.SignalEventp(r, elog.PointerToFirstArg(&e))
}

func (e *Event) SignalEventAfter(r Eventer, dt float64) {
	e.n.SignalEventAfterp(r, dt, elog.PointerToFirstArg(&e))
}
