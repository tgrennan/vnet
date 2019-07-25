// Copyright 2016 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnet

import (
	"github.com/platinasystems/elib"
	"github.com/platinasystems/elib/dep"
	"github.com/platinasystems/elib/loop"

	"fmt"
)

type Node struct {
	loop.Node
	Dep       dep.Dep
	Errors    []string
	errorRefs []ErrorRef
}

func (n *Node) GetVnetNode() *Node { return n }

func (n *Node) AddSuspendActivity(i *RefIn, a int) {
	vnet.loop.AddSuspendActivity(&i.In, a, &suspendLimits)
}

func (n *Node) Suspend(i *RefIn) {
	vnet.loop.Suspend(&i.In, &suspendLimits)
}

func (n *Node) Resume(i *RefIn) {
	vnet.loop.Resume(&i.In, &suspendLimits)
}

const MaxVectorLen = loop.MaxVectorLen

type Noder interface {
	loop.Noder
	GetVnetNode() *Node
}

type InputNode struct {
	Node
	o InputNoder
}

func (n *InputNode) GetInputNode() *InputNode                 { return n }
func (n *InputNode) MakeLoopOut() loop.LooperOut              { return &RefOut{} }
func (n *InputNode) LoopInput(l *loop.Loop, o loop.LooperOut) { n.o.NodeInput(o.(*RefOut)) }

type InputNoder interface {
	Noder
	GetInputNode() *InputNode
	NodeInput(o *RefOut)
}

type OutputNode struct {
	Node
	o OutputNoder
}

func (n *OutputNode) GetOutputNode() *OutputNode               { return n }
func (n *OutputNode) MakeLoopIn() loop.LooperIn                { return &RefIn{} }
func (n *OutputNode) LoopOutput(l *loop.Loop, i loop.LooperIn) { n.o.NodeOutput(i.(*RefIn)) }

type OutputNoder interface {
	Noder
	GetOutputNode() *OutputNode
	NodeOutput(i *RefIn)
}

type enqueue struct {
	x, n uint32
	i    *RefIn
	o    *RefOut
}

func (q *enqueue) sync() {
	l := q.o.Outs[q.x].GetLen()
	if n := uint(q.n); n > l {
		q.o.Outs[q.x].Dup(q.i)
		q.o.Outs[q.x].SetLen(n)
	}
}
func (q *enqueue) validate() {
	if !elib.Debug {
		return
	}
	out_len, in_len := uint(0), q.i.InLen()
	for i := range q.o.Outs {
		o := &q.o.Outs[i]
		out_len += o.GetLen()
	}
	if out_len > in_len {
		panic(fmt.Errorf("out len %d > in len %d", out_len, in_len))
	}
}

func (q *enqueue) put(r0 *Ref, x0 uint) {
	q.o.Outs[x0].Dup(q.i)
	i0 := q.o.Outs[x0].AddLen()
	q.o.Outs[x0].Refs[i0] = *r0
}
func (q *enqueue) Put1(r0 *Ref, x0 uint) {
	//avoid corner case panic when interface is down and removed but a transaction is in progress
	if r0 == nil {
		fmt.Printf("node.go: Put1, Ref pointer is nil; probably deleted\n")
		return
	}
	defer func() {
		if x := recover(); x != nil {
			fmt.Printf("node.go: Put1 recover(), %v\n", x)
		}
	}()
	//
	q.o.Outs[q.x].Refs[q.n] = *r0
	q.n++
	if uint32(x0) != q.x {
		q.n--
		q.put(r0, x0)
	}
}

func (q *enqueue) setCachedNext(x0 uint) {
	q.sync()
	// New cached next and count.
	q.x = uint32(x0)
	q.n = uint32(q.o.Outs[x0].GetLen())
}

func (q *enqueue) Put2(r0, r1 *Ref, x0, x1 uint) {
	//avoid corner case panic when interface is down and removed but a transaction is in progress
	defer func() {
		if x := recover(); x != nil {
			fmt.Printf("node.go: Put2 recover(), %v\n", x)
		}
	}()
	//
	// Speculatively enqueue both refs to cached next.
	n0 := q.n
	q.o.Outs[q.x].Refs[n0+0] = *r0 // (*) see below.
	q.o.Outs[q.x].Refs[n0+1] = *r1
	q.n = n0 + 2

	// Confirm speculation.
	same, match_cache0 := x0 == x1, uint32(x0) == q.x
	if same && match_cache0 {
		return
	}

	// Restore cached length.
	q.n = n0

	// Put refs in correct next slots.
	q.Put1(r0, x0)
	q.Put1(r1, x1)

	// If neither next matches cached next and both are the same, then changed cached next.
	if same {
		q.setCachedNext(x0)
	}
}

func (q *enqueue) Put4(r0, r1, r2, r3 *Ref, x0, x1, x2, x3 uint) {
	// Speculatively enqueue both refs to cached next.
	n0, x := q.n, uint(q.x)
	q.o.Outs[x].Refs[n0+0] = *r0
	q.o.Outs[x].Refs[n0+1] = *r1
	q.o.Outs[x].Refs[n0+2] = *r2
	q.o.Outs[x].Refs[n0+3] = *r3
	q.n = n0 + 4

	// Confirm speculation.
	if x0 == x && x0 == x1 && x2 == x3 && x0 == x2 {
		return
	}

	// Restore cached length.
	q.n = n0

	// Put refs in correct next slots.
	q.Put1(r0, x0)
	q.Put1(r1, x1)
	q.Put1(r2, x2)
	q.Put1(r3, x3)

	// If last 2 misses in cache and both are the same, then changed cached next.
	if x2 != x && x2 == x3 {
		q.setCachedNext(x2)
	}
}

//go:generate gentemplate -d Package=vnet -id enqueue -d VecType=enqueue_vec -d Type=*enqueue github.com/platinasystems/elib/vec.tmpl

type InOutNode struct {
	Node
	qs enqueue_vec
	t  InOutNoder
}

func (n *InOutNode) GetEnqueue(in *RefIn) (q *enqueue) {
	i := in.ThreadId()
	{ //don't do anything if poller has been freed
		if i == ^uint(0) {
			fmt.Printf("node.go: GetEnqueue, ThreadId() not valid, probably deleted\n")
			return
		}
	}
	//avoid corner case panic when interface is down and removed but a transaction is in progress
	defer func() {
		if x := recover(); x != nil {
			fmt.Printf("node.go: GetEnque recover(), in.ThreadId()=%d, %v\n", in.ThreadId(), x)
		}
	}()
	//
	n.qs.Validate(i)
	q = n.qs[i]
	if n.qs[i] == nil {
		q = &enqueue{}
		n.qs[i] = q
	}
	return
}

func (n *InOutNode) GetInOutNode() *InOutNode    { return n }
func (n *InOutNode) MakeLoopIn() loop.LooperIn   { return &RefIn{} }
func (n *InOutNode) MakeLoopOut() loop.LooperOut { return &RefOut{} }
func (n *InOutNode) LoopInputOutput(l *loop.Loop, i loop.LooperIn, o loop.LooperOut) {
	//avoid corner case panic when interface is down and removed but a transaction is in progress
	defer func() {
		if x := recover(); x != nil {
			fmt.Printf("node.go: LoopInputOutput, recover(), %v\n", x)
		}
	}()
	//
	in, out := i.(*RefIn), o.(*RefOut)
	q := n.GetEnqueue(in)
	q.n, q.i, q.o = 0, in, out
	n.t.NodeInput(in, out)
	q.sync()
	q.validate()
}

type InOutNoder interface {
	Noder
	GetInOutNode() *InOutNode
	NodeInput(i *RefIn, o *RefOut)
}

func (node *Node) Redirect(in *RefIn, out *RefOut, next uint) {
	o := &out.Outs[next]
	n := in.InLen()
	copy(o.Refs[:n], in.Refs[:n])
	node.SetOutLen(o, in, n)
}

func (node *Node) ErrorRedirect(in *RefIn, out *RefOut, next uint, err ErrorRef) {
	o := &out.Outs[next]
	n := in.InLen()
	for i := uint(0); i < n; i++ {
		r := &o.Refs[i]
		*r = in.Refs[i]
		r.Aux = uint32(err)
	}
	node.SetOutLen(o, in, n)
}
