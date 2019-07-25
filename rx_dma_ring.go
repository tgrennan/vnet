// Copyright 2016 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnet

import (
	"sync/atomic"
)

type RxDmaRing struct {
	r        RxDmaRinger
	ring_len uint
	sequence uint
	refs     RefVec
	rxDmaRingState
	RxDmaRefState
}

type RxDmaRinger interface {
	getRing() *RxDmaRing
	GetRefState(flags RxDmaDescriptorFlags) RxDmaRefState
}

func (r *RxDmaRing) getRing() *RxDmaRing { return r }

func (g *RxDmaRing) RxDmaRingInit(r RxDmaRinger, flags RxDmaDescriptorFlags, desc_flag_end_of_packet_shift uint, pool *BufferPool, ring_len uint) {
	g.r = r
	g.is_sop = 1 // initialize at start of packet.
	g.pool = pool
	g.ring_len = ring_len
	g.desc_flags = flags
	g.desc_flag_end_of_packet_shift = desc_flag_end_of_packet_shift
	g.RxDmaRefState = r.GetRefState(flags)
	g.refs.Validate(2*ring_len - 1)
	g.pool.AllocRefs(g.refs)
}

type RxDmaDescriptorFlags uint64

// Allocate new re-fill buffers when ring wraps.
func (r *RxDmaRing) WrapRefill() {
	ri0 := r.sequence & 1
	r.sequence++
	r.pool.AllocRefsStride(&r.refs[ri0], r.ring_len, 2)
}

type rxDmaRingIndex uint

// For even ring sequence, rx refs are even; refills are odd; vice versa for odd sequences.
func (r *RxDmaRing) RingIndex(i uint) rxDmaRingIndex                   { return rxDmaRingIndex(2*i + (r.sequence & 1)) }
func (i rxDmaRingIndex) NextRingIndex(n rxDmaRingIndex) rxDmaRingIndex { return i + 2*n }
func (i rxDmaRingIndex) Index() uint                                   { return uint(i) / 2 }

// Even buffer is for packet receive; odd buffer is to refill ring.
func (i rxDmaRingIndex) RxRef(g *RxDmaRing) *Ref     { return &g.refs[i^0] }
func (i rxDmaRingIndex) RefillRef(g *RxDmaRing) *Ref { return &g.refs[i^1] }

func (i rxDmaRingIndex) NextRxRef(g *RxDmaRing, d rxDmaRingIndex) *Ref {
	return i.NextRingIndex(d).RxRef(g)
}

// Aliases.
func (g *RxDmaRing) RxRef(i rxDmaRingIndex) *Ref     { return i.RxRef(g) }
func (g *RxDmaRing) RefillRef(i rxDmaRingIndex) *Ref { return i.RefillRef(g) }

type rxDmaRingState struct {
	chain RefChain

	is_sop uint8

	last_miss_next   uint
	n_last_miss_next uint

	desc_flags RxDmaDescriptorFlags

	// Pool for allocating buffers/refs.
	pool *BufferPool

	desc_flag_end_of_packet_shift uint

	Out *RefOut

	n_next     uint
	max_n_next uint
	n_packets  uint64
	n_bytes    uint64
}

type RxDmaRefState struct {
	// Next index.
	Next uint

	// Number of byte to advance ref data.
	Advance int

	// Interface and error.
	RefOpaque
}

func (g *RxDmaRing) is_end_of_packet(f RxDmaDescriptorFlags) uint8 {
	return uint8(1 & (f >> g.desc_flag_end_of_packet_shift))
}

func (g *RxDmaRing) Rx1Descriptor(ri rxDmaRingIndex, b0 uint, f0 RxDmaDescriptorFlags) {
	r0 := ri.NextRxRef(g, 0)

	was_sop := g.is_sop != 0
	g.n_packets += uint64(g.is_sop)
	g.n_bytes += uint64(b0)

	r0.SetDataLen(b0)
	r0.Advance(g.Advance)
	r0.RefOpaque = g.RefOpaque
	g.Out.Outs[g.Next].Refs[g.n_next] = *r0
	g.n_next += 1

	g.is_sop = g.is_end_of_packet(f0)

	// Speculative enqueue fails; use slow path to fix it up.
	if was_sop && f0 == g.desc_flags {
		return
	}

	g.n_next -= 1
	g.slow_path(r0, f0)

	return
}

func (g *RxDmaRing) Rx4Descriptors(ri rxDmaRingIndex, b0, b1, b2, b3 uint, f0, f1, f2, f3 RxDmaDescriptorFlags) {
	r0, r1, r2, r3 := ri.NextRxRef(g, 0), ri.NextRxRef(g, 1), ri.NextRxRef(g, 2), ri.NextRxRef(g, 3)

	r0.SetDataLen(b0)
	r1.SetDataLen(b1)
	r2.SetDataLen(b2)
	r3.SetDataLen(b3)

	r0.Advance(g.Advance)
	r1.Advance(g.Advance)
	r2.Advance(g.Advance)
	r3.Advance(g.Advance)

	r0.RefOpaque = g.RefOpaque
	r1.RefOpaque = g.RefOpaque
	r2.RefOpaque = g.RefOpaque
	r3.RefOpaque = g.RefOpaque

	g.Out.Outs[g.Next].Refs[g.n_next+0] = *r0
	g.Out.Outs[g.Next].Refs[g.n_next+1] = *r1
	g.Out.Outs[g.Next].Refs[g.n_next+2] = *r2
	g.Out.Outs[g.Next].Refs[g.n_next+3] = *r3
	g.n_next += 4

	is_sop1, is_sop2, is_sop3 := g.is_end_of_packet(f0), g.is_end_of_packet(f1), g.is_end_of_packet(f2)

	g.n_bytes += uint64(b0 + b1 + b2 + b3)
	g.n_packets += uint64(g.is_sop + is_sop1 + is_sop2 + is_sop3)
	was_sop := g.is_sop != 0
	g.is_sop = g.is_end_of_packet(f3)

	// Speculative enqueue fails; use slow path to fix it up.
	if was_sop && f0 == g.desc_flags && f1 == g.desc_flags && f2 == g.desc_flags && f3 == g.desc_flags {
		return
	}

	// Slow path
	g.n_next -= 4
	g.slow_path(r0, f0)
	g.slow_path(r1, f1)
	g.slow_path(r2, f2)
	g.slow_path(r3, f3)
	return
}

// Shared code for rx slow path.
func (g *RxDmaRing) slow_path(r0 *Ref, f0 RxDmaDescriptorFlags) {
	next, n_next := g.Next, g.n_next

	s := &g.rxDmaRingState

	is_sop0 := s.chain.Len() == 0

	// Correct advance if not at start of packet.
	if !is_sop0 {
		r0.Advance(-g.Advance)
	}

	// Append buffer to current chain.
	s.chain.Append(r0)

	// If at end of packet, enqueue packet to next graph node.
	// Otherwise, we're done.
	if g.is_end_of_packet(f0) == 0 {
		return
	}

	rs0 := g.r.GetRefState(f0)
	g.desc_flags = f0
	next0 := rs0.Next

	// Correct data advance.
	if rs0.Advance != g.Advance {
		r0.Advance(rs0.Advance - g.Advance)
		g.Advance = rs0.Advance
	}

	// Interface change?  Flush counters.
	/* FIXME-XETH
	if rs0.Si != g.Si {
		g.flush_interface_counters()
	}
	FIXME-XETH */

	// Set interface and error at the same time.
	r0.RefOpaque = rs0.RefOpaque
	g.RefOpaque = rs0.RefOpaque

	// Enqueue packet.
	ref := s.chain.Done()
	in := &g.Out.Outs[next0]

	// Cache empty?
	if n_next == 0 {
		next = next0
	}

	// Cache hit?
	if next0 == next {
		s.n_last_miss_next = 0
		in.Refs[n_next] = ref
		n_next++
	} else {
		n_next0 := in.InLen()
		l0 := n_next0 + 1
		in.SetPoolAndLen(g.pool, l0)
		in.Refs[n_next0] = ref
		n_next0 = l0
		if l0 > g.max_n_next {
			g.max_n_next = n_next0
		}

		// Switch cached next after enough repeats of cache miss with same next.
		if next0 == s.last_miss_next {
			s.n_last_miss_next++
			if s.n_last_miss_next >= 4 {
				if n_next > 0 {
					g.Out.Outs[next].SetPoolAndLen(g.pool, n_next)
					if n_next > g.max_n_next {
						g.max_n_next = n_next
					}
				}
				next = next0
				n_next = n_next0
			}
		} else {
			s.last_miss_next = next0
			s.n_last_miss_next = 1
		}
	}

	g.Next = next
	g.n_next = n_next
	return
}

func (g *RxDmaRing) flush_interface_counters() {
	if g.n_packets == 0 {
		return
	}
	/* FIXME-XETH
	t := vnet.GetIfThread(0)
	IfRxCounter.Add64(t, g.Si, g.n_packets, g.n_bytes)
	FIXME-XETH */
	g.n_packets = 0
	g.n_bytes = 0
}

func (g *RxDmaRing) Flush() {
	if g.n_next > 0 {
		g.Out.Outs[g.Next].SetPoolAndLen(g.pool, g.n_next)
		g.n_next = 0
	}
	g.flush_interface_counters()
	// Reset for future calls.
	g.max_n_next = 0
}

func (g *RxDmaRing) MaxNext() (n uint) {
	n = g.n_next
	if g.max_n_next > n {
		n = g.max_n_next
	}
	return
}

// Atomic register shadow helpers.

type Reg32 uint32
type Reg64 uint64

// Atomically set 32-bit register shadow.  Typically done in interrupt routine.
func (s *Reg32) Or(v uint32) (new uint32) {
	for {
		old := atomic.LoadUint32((*uint32)(s))
		new = old | v
		if atomic.CompareAndSwapUint32((*uint32)(s), old, new) {
			break
		}
	}
	return
}

// Atomically read and clear 32-bit status.
func (s *Reg32) ReadClear() (v uint32) {
	for {
		v = atomic.LoadUint32((*uint32)(s))
		if atomic.CompareAndSwapUint32((*uint32)(s), v, 0) {
			break
		}
	}
	return
}

// Atomically set 64-bit status.  Typically done in interrupt routine.
func (s *Reg64) Or(v uint64) {
	for {
		old := atomic.LoadUint64((*uint64)(s))
		new := old | v
		if atomic.CompareAndSwapUint64((*uint64)(s), old, new) {
			break
		}
	}
}

// Atomically read and clear 64-bit status.
func (s *Reg64) ReadClear() (v uint64) {
	for {
		v = atomic.LoadUint64((*uint64)(s))
		if atomic.CompareAndSwapUint64((*uint64)(s), v, 0) {
			break
		}
	}
	return
}
