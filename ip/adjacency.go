// Copyright 2016 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ip

import (
	"github.com/platinasystems/elib"
	"github.com/platinasystems/elib/dep"
	"github.com/platinasystems/elib/parse"
	"github.com/platinasystems/vnet"
	"github.com/platinasystems/vnet/internal/dbgvnet"

	"errors"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"unsafe"
)

// Next node index stored in ip adjacencies.
type LookupNext uint16

const (
	// Packet does not match any route in table.
	LookupNextMiss LookupNext = iota

	// Adjacency says to drop or punt this packet.
	LookupNextDrop
	LookupNextPunt

	// This packet matches an IP address of one of our interfaces.
	LookupNextLocal

	// Glean node.
	// This packet matches an "interface route" and packets need to be passed to ip4 ARP (or ip6 neighbor discovery)
	// to find rewrite string for this destination.
	LookupNextGlean

	// This packet is to be rewritten and forwarded to the next
	// processing node.  This is typically the output interface but
	// might be another node for further output processing.
	LookupNextRewrite

	LookupNNext
)

var lookupNextNames = [...]string{
	LookupNextMiss:    "miss",
	LookupNextDrop:    "drop",
	LookupNextPunt:    "punt",
	LookupNextLocal:   "local",
	LookupNextGlean:   "glean",
	LookupNextRewrite: "rewrite",
}

func (n LookupNext) String() string { return elib.StringerHex(lookupNextNames[:], int(n)) }

func (n *LookupNext) Parse(in *parse.Input) {
	switch text := in.Token(); text {
	case "miss":
		*n = LookupNextMiss
	case "drop":
		*n = LookupNextDrop
	case "punt":
		*n = LookupNextPunt
	case "local":
		*n = LookupNextLocal
	case "glean":
		*n = LookupNextGlean
	case "rewrite":
		*n = LookupNextRewrite
	default:
		in.ParseError()
	}
}

type Adjacency struct {
	// Next hop after ip4-lookup.
	LookupNextIndex LookupNext

	// Number of adjecencies in block.  Greater than 1 means multipath; otherwise equal to 1.
	NAdj uint16

	// not used for Local and Glean
	// Destination index for rewrite.
	Index uint32

	vnet.Rewrite
}

func (ai Adj) getAsSingle(m *Main) (a *Adjacency, ok bool) {
	if ai == AdjNil {
		return
	}
	if m.IsAdjFree(ai) {
		return
	}
	as := m.GetAdj(ai)
	if len(as) != 1 || m.IsMpAdj(ai) {
		return
	}
	a = &as[0]
	ok = true
	return
}

func (ai Adj) IsConnectedRoute(m *Main) (conn bool, si vnet.Si) {
	si = vnet.SiNil
	if a, ok := ai.getAsSingle(m); ok {
		conn = a.LookupNextIndex == LookupNextRewrite
		si = a.Si
	}
	return
}
func (ai Adj) IsViaRoute(m *Main) bool {
	return m.IsMpAdj(ai)
}
func (ai Adj) IsLocal(m *Main) bool {
	if a, ok := ai.getAsSingle(m); ok {
		return a.LookupNextIndex == LookupNextLocal
	}
	return false
}
func (ai Adj) IsGlean(m *Main) bool {
	if a, ok := ai.getAsSingle(m); ok {
		return a.LookupNextIndex == LookupNextGlean
	}
	return false
}

func (a *Adjacency) IsRewrite() bool { return a.LookupNextIndex == LookupNextRewrite }
func (a *Adjacency) IsLocal() bool   { return a.LookupNextIndex == LookupNextLocal }
func (a *Adjacency) IsGlean() bool   { return a.LookupNextIndex == LookupNextGlean }

func (a *Adjacency) AdjLines(m *Main) (lines []string) {
	lines = append(lines, a.LookupNextIndex.String())
	ni := a.LookupNextIndex
	switch ni {
	case LookupNextRewrite:
		l := a.Rewrite.Lines(m.v)
		lines[0] += " " + l[0]
		// If only 2 lines, fit into a single line.
		if len(l) == 2 {
			lines[0] += " " + l[1]
		} else {
			lines = append(lines, l[1:]...)
		}
	case LookupNextGlean, LookupNextLocal:
		lines[0] += " " + vnet.SiName{m.v, a.Si}.String()
	}
	return
}

func (a *Adjacency) ParseWithArgs(in *parse.Input, args *parse.Args) {
	m := args.Get().(*Main)
	if !in.Parse("%v", &a.LookupNextIndex) {
		in.ParseError()
	}
	a.NAdj = 1
	a.Index = ^uint32(0)
	switch a.LookupNextIndex {
	case LookupNextRewrite:
		if !in.Parse("%v", &a.Rewrite, m.v) {
			panic(in.Error())
		}
	case LookupNextLocal, LookupNextGlean:
		var si vnet.Si
		if in.Parse("%v", &si, m.v) {
			a.Index = uint32(si)
		}
	}
}

// Index into adjacency table.
type Adj uint32

// Miss adjacency is always first in adjacency table.
const (
	AdjMiss Adj = 0
	AdjDrop Adj = 1
	AdjPunt Adj = 2
	AdjNil  Adj = (^Adj(0) - 1) // so AdjNil + 1 is non-zero for remaps.
)

func (a Adj) String() string {
	switch a {
	case AdjMiss:
		return "0(AdjMiss)"
	case AdjDrop:
		return "1(AdjDrop)"
	case AdjPunt:
		return "2(AdjPunt)"
	case AdjNil:
		return "AdjNil"
	default:
		return strconv.Itoa(int(a))
	}
}

//go:generate gentemplate -d Package=ip -id adjacencyHeap -d HeapType=adjacencyHeap -d Data=elts -d Type=Adjacency github.com/platinasystems/elib/heap.tmpl

type adjacencyThread struct {
	// Packet/byte counters for each adjacency.
	counters vnet.CombinedCounters
}

type adjacencyMain struct {
	adjacencyHeap

	multipathMain multipathMain

	threads []*adjacencyThread

	adjAddDelHookVec
	adjSyncCounterHookVec
	adjGetCounterHookVec

	specialAdj [3]Adj
}

type adjAddDelHook func(m *Main, adj Adj, isDel bool)
type adjSyncCounterHook func(m *Main)
type AdjGetCounterHandler func(tag string, v vnet.CombinedCounter)
type adjGetCounterHook func(m *Main, adj Adj, f AdjGetCounterHandler, clear bool)

//go:generate gentemplate -id adjAddDelHook -d Package=ip -d DepsType=adjAddDelHookVec -d Type=adjAddDelHook -d Data=adjAddDelHooks github.com/platinasystems/elib/dep/dep.tmpl
//go:generate gentemplate -id adjSyncHook -d Package=ip -d DepsType=adjSyncCounterHookVec -d Type=adjSyncCounterHook -d Data=adjSyncCounterHooks github.com/platinasystems/elib/dep/dep.tmpl
//go:generate gentemplate -id adjGetCounterHook -d Package=ip -d DepsType=adjGetCounterHookVec -d Type=adjGetCounterHook -d Data=adjGetCounterHooks github.com/platinasystems/elib/dep/dep.tmpl

type NextHopWeight uint32

// A next hop in a multipath.
type nextHop struct {
	// Adjacency index for next hop's rewrite.
	Adj Adj

	// Relative weight for this next hop.
	Weight NextHopWeight
}

type NextHop struct {
	Address net.IP
	Si      vnet.Si
	nextHop
}

func (n *NextHop) NextHopWeight() NextHopWeight     { return n.Weight }
func (n *NextHop) NextHopFibIndex(m *Main) FibIndex { return m.FibIndexForSi(n.Si) }
func (n *NextHop) FinalizeAdjacency(a *Adjacency)   {}

type NextHopVec []NextHop

func (nhs NextHopVec) ListNhs(m *Main) string {
	if dbgvnet.Adj == 0 {
		return "noop"
	}
	s := ""
	for _, nh := range nhs {
		s += fmt.Sprintf("  ip:%v intf:%v adj:%v weight:%v\n",
			nh.Address, vnet.SiName{m.v, nh.Si}, nh.Adj, nh.Weight)
	}
	return s
}

func (nhs NextHopVec) Match(nhs2 []NextHop) bool {
	if len(nhs) != len(nhs2) {
		return false
	}
	for i, _ := range nhs {
		if nhs[i].Si != nhs2[i].Si {
			return false
		}
		// works even if addres is nil
		if !nhs[i].Address.Equal(nhs2[i].Address) {
			return false
		}
	}
	return true
}

//go:generate gentemplate -d Package=ip -id nextHopHeap -d HeapType=nextHopHeap -d Data=elts -d Type=nextHop github.com/platinasystems/elib/heap.tmpl
//go:generate gentemplate -d Package=ip -id nextHopVec -d VecType=nextHopVec -d Type=nextHop github.com/platinasystems/elib/vec.tmpl
//go:generate gentemplate -d Package=ip -id multipathAdjacencyPool -d PoolType=multipathAdjacencyPool -d Type=multipathAdjacency -d Data=elts github.com/platinasystems/elib/pool.tmpl

type nextHopHashValue struct {
	heapOffset uint32
	adj        Adj
}

type multipathMain struct {
	// Expressed as max allowable fraction of traffic sent the wrong way assuming perfectly random distribution.
	multipathErrorTolerance float64
	nextHopHash             elib.Hash
	nextHopHashValues       []nextHopHashValue

	cachedNextHopVec [3]nextHopVec

	nextHopHeap

	// Indexed by heap id.  So, one element per heap block.
	mpAdjPool multipathAdjacencyPool
}

func (m *multipathMain) GetNextHops(i uint) []nextHop {
	return m.nextHopHeap.Slice(uint(m.nextHopHashValues[i].heapOffset))
}
func (m *multipathMain) HashIndex(s *elib.HashState, i uint) {
	nextHopVec(m.GetNextHops(i)).HashKey(s)
}
func (m *multipathMain) HashResize(newCap uint, rs []elib.HashResizeCopy) {
	src, dst := m.nextHopHashValues, make([]nextHopHashValue, newCap)
	for i := range rs {
		dst[rs[i].Dst] = src[rs[i].Src]
	}
	m.nextHopHashValues = dst
}

func (a nextHopVec) HashKey(s *elib.HashState) {
	s.HashPointer(unsafe.Pointer(&a[0]), uintptr(a.Len())*unsafe.Sizeof(a[0]))
}
func (a nextHopVec) Equal(b nextHopVec) bool {
	if la, lb := a.Len(), b.Len(); la != lb {
		return false
	} else {
		for i := uint(0); i < la; i++ {
			if a[i].compare(&b[i]) != 0 {
				return false
			}
		}
	}
	return true
}
func (a nextHopVec) HashKeyEqual(h elib.Hasher, i uint) bool {
	b := nextHopVec(h.(*multipathMain).GetNextHops(i))
	return a.Equal(b)
}

func (a *nextHop) compare(b *nextHop) (cmp int) {
	cmp = int(b.Weight) - int(a.Weight)
	if cmp == 0 {
		cmp = int(a.Adj) - int(b.Adj)
	}
	return
}

func (a nextHopVec) sort() {
	sort.Slice(a, func(i, j int) bool {
		return a[i].compare(&a[j]) < 0
	})
}

// Normalize next hops: find a power of 2 sized block of next hops within error tolerance of given raw next hops.
func (given nextHopVec) normalizePow2(m *multipathMain, result *nextHopVec) (nAdj uint, norm nextHopVec) {
	nGiven := given.Len()

	if nGiven == 0 {
		return
	}

	// Allocate enough space for 2 copies; we'll use second copy to save original weights.
	t := *result
	t.Validate(2*nGiven - 1)
	// Save allocation for next caller.
	*result = t

	n := nGiven
	nAdj = n
	switch n {
	case 1:
		t[0] = given[0]
		t[0].Weight = 1
		norm = t[:1]
		return
	case 2:
		cmp := 0
		if given[0].compare(&given[1]) > 0 {
			cmp = 1
		}
		t[0], t[1] = given[cmp], given[cmp^1]
		if t[0].Weight == t[1].Weight {
			t[0].Weight = 1
			t[1].Weight = 1
			norm = t[:2]
			return
		}

	default:
		copy(t, given)
		// Order by decreasing weight and increasing adj index for equal weights.
		sort.Slice(t[0:n], func(i, j int) bool {
			return t[i].compare(&t[j]) < 0
		})
	}

	// Find total weight to normalize weights.
	sumWeight := float64(0)
	for i := uint(0); i < n; i++ {
		sumWeight += float64(t[i].Weight)
	}

	// In the unlikely case that all weights are given as 0, set them all to 1.
	if sumWeight == 0 {
		for i := uint(0); i < n; i++ {
			t[i].Weight = 1
		}
		sumWeight = float64(n)
	}

	// Save copies of all next hop weights to avoid being overwritten in loop below.
	copy(t[n:], t[:n])
	t_save := t[n:]

	if m.multipathErrorTolerance == 0 {
		m.multipathErrorTolerance = .01
	}

	// Try larger and larger power of 2 sized adjacency blocks until we
	// find one where traffic flows to within 1% of specified weights.
	nAdj = uint(elib.Word(n).MaxPow2())
	for {
		w := float64(nAdj) / sumWeight
		nLeft := nAdj

		for i := uint(0); i < n; i++ {
			nf := w * float64(t_save[i].Weight)
			n := uint(nf + .5) // round to nearest integer
			if n > nLeft {
				n = nLeft
			}
			nLeft -= n
			t[i].Weight = NextHopWeight(n)
		}
		// Add left over weight to largest weight next hop.
		t[0].Weight += NextHopWeight(nLeft)
		error := float64(0)
		i_zero := n
		for i := uint(0); i < n; i++ {
			if t[i].Weight == 0 && i_zero == n {
				i_zero = i
			}
			// perfect distribution of weight:
			want := float64(t_save[i].Weight) / sumWeight
			// approximate distribution with nAdj
			have := float64(t[i].Weight) / float64(nAdj)
			error += math.Abs(want - have)
		}
		if error < m.multipathErrorTolerance {
			// Truncate any next hops with zero weight.
			norm = t[:i_zero]
			break
		}

		// Try next power of 2 size.
		nAdj *= 2
	}
	return
}

func (m *multipathMain) allocNextHopBlock(b *nextHopBlock, key nextHopVec) {
	n := uint(len(key))
	o := m.nextHopHeap.Get(n)
	copy(m.nextHopHeap.Slice(o), key)
	b.size = uint32(n)
	b.offset = uint32(o)
}

func (m *multipathMain) freeNextHopBlock(b *nextHopBlock) {
	m.nextHopHeap.Put(uint(b.offset))
	b.offset = ^uint32(0)
	b.size = ^uint32(0)
}

func (m *multipathMain) getNextHopBlock(b *nextHopBlock) nextHopVec {
	return nextHopVec(m.nextHopHeap.Slice(uint(b.offset)))
}

type nextHopBlock struct {
	// Heap offset of first next hop.
	offset uint32

	// Block size.
	size uint32
}

type multipathAdjacency struct {
	// Index of first adjacency in block.
	adj Adj

	// Power of 2 size of block.
	nAdj uint32

	// Number of prefixes that point to this adjacency.
	referenceCount uint32

	// Pool index.
	index uint

	// Given next hops are saved so that control plane has a record of exactly
	// what the RIB told it.
	givenNextHops nextHopBlock

	// Resolved next hops are used as hash keys: they are sorted by weight
	// and weights are chosen so they add up to nAdj (with zero-weighted next hops being deleted).
	resolvedNextHops nextHopBlock
}

func (p *nextHopVec) sum_duplicates() {
	var i, j, l int
	r := *p
	l = len(r)
	lastAdj := AdjNil
	for i = 0; i < l; i++ {
		a := &r[i]
		if i > 0 && a.Adj == lastAdj {
			r[j].Weight += a.Weight
		} else {
			r[j] = *a
			j++
		}
		lastAdj = a.Adj
	}
	*p = r[:j]
}

func (r *nextHopVec) resolve(m *Main, given nextHopVec, level uint) {
	mp := &m.multipathMain
	if level == 0 {
		r.ResetLen()
	}
	for i := range given {
		g := &given[i]
		i0 := r.Len()
		ma := m.mpAdjForAdj(g.Adj, false)
		if ma != nil {
			r.resolve(m, mp.getNextHopBlock(&ma.givenNextHops), level+1)
		} else {
			*r = append(*r, *g)
		}
		i1 := r.Len()
		for i0 < i1 {
			(*r)[i0].Weight *= g.Weight
			i0++
		}
	}
	if level == 0 {
		r.sort()
		r.sum_duplicates()
	}
}

type AdjacencyFinalizer interface {
	FinalizeAdjacency(a *Adjacency)
}

func (m *Main) createMpAdj(given nextHopVec, af AdjacencyFinalizer) (madj *multipathAdjacency) {
	mp := &m.multipathMain

	mp.cachedNextHopVec[1].resolve(m, given, 0)
	resolved := mp.cachedNextHopVec[1]

	nAdj, norm := resolved.normalizePow2(mp, &mp.cachedNextHopVec[2])

	dbgvnet.Adj.Log("given nhs:", given.ListNhs(m))
	dbgvnet.Adj.Log("resolved nhs:", resolved.ListNhs(m))
	dbgvnet.Adj.Log("normalized nhs:", norm.ListNhs(m))

	// Use given next hops to see if we've seen a block equivalent to this one before. (not really, norm is built from resolved)
	i, ok := mp.nextHopHash.Get(norm)
	if ok {
		ai := mp.nextHopHashValues[i].adj
		madj = m.mpAdjForAdj(ai, false)
		if madj != nil {
			dbgvnet.Adj.Log("reuse existing block, adj", madj.adj)
			if dbgvnet.AdjFlag {
				m.checkMpAdj(madj.adj)
			}
		} else {
			// Shouldn't get to this point.
			// If there is a block matching the resolved nhs, but it's not a mpAdj...
			// usually means there is some leftover block that didn't get cleaned up
			// like if a mpAdj was deleted before calling free(m *Main).
			if dbgvnet.AdjFlag {
				panic(fmt.Errorf("DEBUG: adjacency.go createMpAdj: found existing block, but it is not mpAdj, ai %v\n", ai))
			}
			fmt.Printf("DEBUG: adjacency.go createMpAdj: found existing block, but it is not mpAdj, ai %v\n", ai)
		}
		return
	}

	// Copy next hops into power of 2 adjacency block one for each weight.
	ai, as := m.NewAdj(nAdj)
	dbgvnet.Adj.Logf("get new ai %v for %v nhs\n", ai, nAdj)
	for nhi := range norm {
		nh := &norm[nhi]
		nextHopAdjacency := &m.adjacencyHeap.elts[nh.Adj]
		for w := NextHopWeight(0); w < nh.Weight; w++ {
			as[i] = *nextHopAdjacency
			if af != nil {
				af.FinalizeAdjacency(&as[i])
			}
			as[i].NAdj = uint16(nAdj)
			i++
		}
	}

	madj = m.mpAdjForAdj(ai, true)
	if madj == nil {
		// self-protect
		dbgvnet.Adj.Log("self-protect return")
		return
	}
	madj.adj = ai
	madj.nAdj = uint32(nAdj)
	madj.referenceCount = 0 // caller will set to 1

	mp.allocNextHopBlock(&madj.resolvedNextHops, norm)
	mp.allocNextHopBlock(&madj.givenNextHops, given)

	i, _ = mp.nextHopHash.Set(norm)
	mp.nextHopHashValues[i] = nextHopHashValue{
		heapOffset: madj.resolvedNextHops.offset,
		adj:        ai,
	}

	m.CallAdjAddHooks(ai)
	dbgvnet.Adj.Log("create new block, adj", madj.adj)
	if dbgvnet.AdjFlag {
		m.checkMpAdj(madj.adj)
	}
	return
}

func (m *Main) checkMpAdj(mpAdj Adj) {
	//verify all nh.adj of madj are not themselves multipath
	nhs := m.NextHopsForAdj(mpAdj)
	fail := false
	for nhi := range nhs {
		nh := &nhs[nhi]
		if m.IsMpAdj(nh.Adj) {
			fail = true
		}
	}
	if fail {
		panic(fmt.Errorf("checkMpAdj: multipathAdj %v did not resolve properly\n", mpAdj))
	}
}

func (nhs nextHopVec) ListNhs(m *Main) string {
	if dbgvnet.Adj == 0 {
		return "noop"
	}
	s := ""
	for nhi := range nhs {
		nh := &nhs[nhi]
		if m.IsMpAdj(nh.Adj) {
			s += fmt.Sprintf(" %v(mpAdj) weight %v;", nh.Adj, nh.Weight)
		} else {
			s += fmt.Sprintf(" %v weight %v;", nh.Adj, nh.Weight)
		}
	}
	return s
}

func (m *Main) GetAdjacencyUsage() elib.HeapUsage { return m.adjacencyHeap.GetUsage() }

func (m *Main) IsMpAdj(a Adj) bool {
	if a == AdjNil {
		return false
	}
	ma := m.mpAdjForAdj(a, false)
	if ma == nil {
		return false
	}
	if !ma.isValid() {
		return false
	}
	return true
}

func (m *adjacencyMain) mpAdjForAdj(a Adj, create bool) (ma *multipathAdjacency) {
	if a == AdjNil {
		return
	}
	if int(a) >= len(m.adjacencyHeap.elts) {
		fmt.Print("adjacency.go mpAdjForAdj: index out of range adj", a)
		return
	}
	adj := &m.adjacencyHeap.elts[a]
	if !adj.IsRewrite() {
		return
	}
	i := uint(adj.Index)
	mm := &m.multipathMain
	if create {
		i = mm.mpAdjPool.GetIndex()
		adj.Index = uint32(i)
		mm.mpAdjPool.elts[i].index = i
	}
	if i < mm.mpAdjPool.Len() {
		ma = &mm.mpAdjPool.elts[i]
	}
	return
}

func (m *adjacencyMain) NextHopsForAdj(a Adj) (nhs nextHopVec) {
	mm := &m.multipathMain
	ma := m.mpAdjForAdj(a, false)
	if ma != nil && ma.nAdj > 0 {
		nhs = mm.getNextHopBlock(&ma.resolvedNextHops)
	} else {
		nhs = []nextHop{{Adj: a, Weight: 1}}
	}
	return
}

func (m *Main) DelNextHopsAdj(oldAdj Adj) (ok bool) {
	old := m.mpAdjForAdj(oldAdj, false)
	if old == nil {
		dbgvnet.Adj.Logf("adj %v not found\n", oldAdj)
		return
	} else {
		old.referenceCount--
		dbgvnet.Adj.Logf("multipathAdj %v referenceCount-- %v\n", old.adj, old.referenceCount)
		if old.referenceCount == 0 {
			old.free(m)
		}
		ok = true
	}
	return
}

func (m *Main) AddNextHopsAdj(nhs NextHopVec) (newAdj Adj, ok bool) {
	dbgvnet.Adj.Log("add nhs:\n", nhs.ListNhs(m))
	nnh := uint(len(nhs))
	newAdj = AdjNil
	if nnh == 0 {
		dbgvnet.Adj.Log("DEBUG adding an empty set of next hops")
		return
	}
	// make NextHopVec into a nextHopVec
	// skip non rewrites
	//mm := &m.multipathMain
	//mm.cachedNextHopVec[0].Validate(nnh)
	//newNhs := mm.cachedNextHopVec[0]
	newNhs := nextHopVec{}
	for _, nh := range nhs {
		if ok, _ := nh.Adj.IsConnectedRoute(m); ok {
			newNhs = append(newNhs, nh.nextHop)
		}
	}
	if len(newNhs) == 0 {
		// not a failure, just means no adjacency
		ok = true
		return
	}
	new := m.createMpAdj(newNhs, nil)
	if new != nil {
		new.referenceCount++
		dbgvnet.Adj.Logf("multipathAdj %v referenceCount++ %v\n", new.adj, new.referenceCount)
		newAdj = new.adj
		ok = true
		{
			new_nhs := m.NextHopsForAdj(newAdj)
			adjs := m.GetAdj(newAdj)
			ai := Adj(0)
			failed := false
			for _, nnh := range new_nhs {
				si := adjs[ai].Si
				found := false
				for _, nh := range nhs {
					if si == nh.Si {
						found = true
						break
					}
				}
				if !found {
					failed = true
				}
				ai += Adj(nnh.Weight)
			}
			if failed {
				dbgvnet.Adj.Log("DEBUG DEBUG requested and new nexthops don't match")
				dbgvnet.Adj.Log("DEBUG DEBUG requested:", nhs.ListNhs(m))
				dbgvnet.Adj.Log("DEBUG DEBUG new:", new_nhs.ListNhs(m))
			}

		}

	} else {
		dbgvnet.Adj.Log("DEBUG got nil for new adjacency")
	}
	return
}

func (m *Main) AddDelNextHop(oldAdj Adj, nextHopAdj Adj, nextHopWeight NextHopWeight, af AdjacencyFinalizer, isDel bool) (newAdj Adj, ok bool) {
	mm := &m.multipathMain
	var (
		old, new *multipathAdjacency
		nhs      nextHopVec
		nnh, nhi uint
	)

	if oldAdj != AdjNil && oldAdj != AdjMiss {
		if old = m.mpAdjForAdj(oldAdj, false); old != nil {
			if old.isValid() { //when a ma is returned to pool it is invalid instead of nil
				nhs = mm.getNextHopBlock(&old.givenNextHops)
				nnh = nhs.Len()
				nhi, ok = nhs.find(nextHopAdj)

				nhs_string := ""
				if !ok && m.IsMpAdj(nextHopAdj) && isDel { // Really shouldn't be here anymore; if so could be indicator of bug somewhere else
					// If nextHopAdj is itself a mpAdj (rare), than try using the its resolved nh if there is only 1
					// Do this only for delete. For add, need to add the given nextHopAdj if not found; rest would take care of itself later.
					resolved_nhs := m.NextHopsForAdj(nextHopAdj)
					if len(resolved_nhs) == 1 {
						nhi, ok = nhs.find(resolved_nhs[0].Adj)
					}
					dbgvnet.Adj.Logf("DEBUG delete failed to find nhAdj %v from oldAdj %v, but found nhAdj's resolved adj %v at nhi %v\n",
						nextHopAdj, oldAdj, resolved_nhs[0].Adj, nhi)
					nhs_string = fmt.Sprintf("that's a MpAdj with resolved adjs %v", resolved_nhs.ListNhs(m))

				}

				if isDel && !ok { // Really shouldn't be here anymore; if so could be indicator of bug somewhere else
					dbgvnet.Adj.Logf("DEBUG adjacency.go AddDelNextHop delete old is valid, but failed to find in oldAdj %v nhAdj %v %v\n", oldAdj, nextHopAdj, nhs_string)
					if oldAdj == nextHopAdj { // really shouldn't come to this
						// nhAdj not found in oldAdj, but nhAdj == oldAdj; remove itself
						ok = true
						newAdj = AdjNil
						old.referenceCount--
						dbgvnet.Adj.Logf(" ...multipathAdj %v referenceCount-- %v\n", old.adj, old.referenceCount)
						return
					}
				}
			}
		} else {
			if dbgvnet.AdjFlag {
				panic(fmt.Errorf("adjacency.go AddDelNextHop %v nhAdj %v from %v IsMpAdj %v", vnet.IsDel(isDel), nextHopAdj, oldAdj, m.IsMpAdj(oldAdj)))
			}
		}
	}
	// For delete next hop must be found.
	if nhi >= nnh && isDel {
		dbgvnet.Adj.Logf("delete failed to find nhAdj %v from oldAdj %v\n", nextHopAdj, oldAdj)
		return
	}

	// Re-use vector for each call.
	// nnh is 0 or is number of nh in oldAdj
	// nhi is 0 or is index of nh in oldAdj's nhs
	mm.cachedNextHopVec[0].Validate(nnh)
	newNhs := mm.cachedNextHopVec[0]
	newAdj = AdjNil
	if isDel {
		dbgvnet.Adj.Logf("delete adj %v from %v oldNhs:%v ... \n", nextHopAdj, oldAdj, nhs.ListNhs(m))
		// Delete next hop at previously found index.
		if nhi > 0 {
			copy(newNhs[:nhi], nhs[:nhi])
		}
		if nhi+1 < nnh {
			copy(newNhs[nhi:], nhs[nhi+1:])
		}
		newNhs = newNhs[:nnh-1]
	} else {
		// If next hop is already there with the same weight, we have nothing to do.
		if nhi < nnh && nhs[nhi].Weight == nextHopWeight {
			newAdj = oldAdj
			ok = true
			dbgvnet.Adj.Logf("add adj %v to %v, same nh and weight, no change\n", nextHopAdj, oldAdj)
			return
		}

		newNhs = newNhs[:nnh+1]

		// Copy old next hops to lookup key.
		copy(newNhs, nhs)

		var nh *nextHop
		if nhi < nnh {
			// Change weight of existing next hop.
			nh = &newNhs[nhi]
		} else {
			// Add a new next hop.
			nh = &newNhs[nnh]
			nh.Adj = nextHopAdj
		}
		// In either case set next hop weight.
		nh.Weight = nextHopWeight
		dbgvnet.Adj.Logf("add adj %v to %v oldNhs:%v, nnh %v, newNhs before resolve %v ... \n",
			nextHopAdj, oldAdj, nhs.ListNhs(m), nnh, newNhs.ListNhs(m))
	}

	new = m.addDelHelper(newNhs, old, af)
	if new != nil {
		ok = true
		newAdj = new.adj
	}

	nhAdjs_string := ""
	if newAdj != AdjNil {
		resolved_nhs := m.NextHopsForAdj(newAdj)
		nhAdjs_string += fmt.Sprintf("resolved adj: %v", resolved_nhs.ListNhs(m))
	}
	dbgvnet.Adj.Logf("%v adj %v to/from %v, newAdj %v %v\n", vnet.IsDel(isDel), nextHopAdj, oldAdj, newAdj, nhAdjs_string)
	if isDel && !ok {
		dbgvnet.Adj.Logf("delete failed new_is_nil=%v", new == nil)
	}

	return
}

func (m *Main) addDelHelper(newNhs nextHopVec, old *multipathAdjacency, af AdjacencyFinalizer) (new *multipathAdjacency) {
	mm := &m.multipathMain

	if len(newNhs) > 0 {
		new = m.createMpAdj(newNhs, af)
		// Fetch again since create may have moved multipath adjacency vector.
		if old != nil {
			old = &mm.mpAdjPool.elts[old.index]
		}
	}

	if new != old {
		if old != nil {
			old.referenceCount--
			dbgvnet.Adj.Logf(" multipathAdj %v referenceCount-- %v\n", old.adj, old.referenceCount)
		}
		if new != nil {
			new.referenceCount++
			dbgvnet.Adj.Logf(" multipathAdj %v referenceCount++ %v\n", new.adj, new.referenceCount)
		}
	}
	if old != nil && old.referenceCount == 0 {
		old.free(m)
	}
	return
}

func (m *adjacencyMain) PoisonAdj(a Adj) {
	as := m.adjacencyHeap.Slice(uint(a))
	elib.PointerPoison(unsafe.Pointer(&as[0]), uintptr(len(as))*unsafe.Sizeof(as[0]))
}

func (m *Main) IsAdjFree(a Adj) bool {
	return m.adjacencyHeap.IsFree(uint(a))
}

func (m *Main) FreeAdj(a Adj) bool {
	// FreeAdj just puts index back into adjacencyHeap
	// If a is a mpAdj, should call free(m *Main) instead which includes cleaning up all the other stuff associated with a
	// by the time FreeAdj is called, a should have been poisoned, and IsMpAdj should be false
	dbgvnet.Adj.Logf("%v IsMpAdj %v\n", a, m.IsMpAdj(a))

	if !m.adjacencyHeap.IsFree(uint(a)) {
		m.adjacencyHeap.Put(uint(a))
	}
	return true
}

func (m *Main) DelAdj(a Adj) {
	if ma := m.mpAdjForAdj(a, false); ma != nil {
		// Shouldn't be here; if mpAdj, then caller should have used free which deletes the adj and mpAdj
		// However, handle it
		dbgvnet.Adj.Logf("%v IsMpAdj %v\n", a, m.IsMpAdj(a))
		if ma.isValid() {
			ma.free(m)
		}
	} else {
		m.PoisonAdj(a)
		if ok := m.FreeAdj(a); ok {
			dbgvnet.Adj.Logf("CallAdjDelHooks(%v)\n", a)
			m.CallAdjDelHooks(a)
		}
	}
}

func (nhs nextHopVec) find(target Adj) (i uint, ok bool) {
	for i = 0; i < uint(len(nhs)); i++ {
		if ok = nhs[i].Adj == target; ok {
			break
		}
	}
	return
}

var (
	ErrNotRewrite = errors.New("replacment adjacenty is not rewrite")
	ErrNotFound   = errors.New("adjacency not found")
)

func (m *Main) ReplaceNextHop(ai, fromNextHopAdj, toNextHopAdj Adj, af AdjacencyFinalizer) (newAdj Adj, err error) {
	var new *multipathAdjacency
	newAdj = ai // if error at any point would return ai as newAdj (i.e. unmodified)

	if fromNextHopAdj == toNextHopAdj {
		return
	}

	// Adjacencies must be rewrites.
	as := m.GetAdj(toNextHopAdj)
	for i := range as {
		if !as[i].IsRewrite() {
			err = ErrNotRewrite
			return
		}
	}

	mm := &m.multipathMain
	ma := m.mpAdjForAdj(ai, false)
	if ma == nil {
		// self-protection
		// Do something better than returning nil
		err = nil
		return
	}

	given := mm.getNextHopBlock(&ma.givenNextHops)
	found := false
	for i := range given {
		nh := &given[i]
		if found = nh.Adj == fromNextHopAdj; found {
			nh.Adj = toNextHopAdj
			break
		}
	}
	if !found {
		// err = ErrNotFound
		// self-protection
		// Do something better than returning nil
		err = nil
		return
	}

	// After addDelHelper, new adjacency may get a different index; update
	new = m.addDelHelper(given, ma, af)
	if new != nil { // since this is a replace should never be nil, but just in case
		newAdj = new.adj
	} else {
		newAdj = AdjNil
	}
	dbgvnet.Adj.Logf("oldAdj %v replace nhAdj %v with nhAdj %v and got newAdj %v\n", ai, fromNextHopAdj, toNextHopAdj, newAdj)

	return
}

func (a *multipathAdjacency) invalidate()   { a.nAdj = 0 }
func (a *multipathAdjacency) isValid() bool { return a.nAdj != 0 }

func (m *Main) callAdjAddDelHooks(a Adj, isDel bool) {
	for i := range m.adjAddDelHooks {
		m.adjAddDelHookVec.Get(i)(m, a, isDel)
	}
}
func (m *Main) CallAdjAddHooks(a Adj) { m.callAdjAddDelHooks(a, false) }
func (m *Main) CallAdjDelHooks(a Adj) { m.callAdjAddDelHooks(a, true) }

func (m *Main) RegisterAdjAddDelHook(f adjAddDelHook, dep ...*dep.Dep) {
	m.adjAddDelHookVec.Add(f, dep...)
}

func (m *Main) CallAdjSyncCounterHooks() {
	for i := range m.adjSyncCounterHooks {
		m.adjSyncCounterHookVec.Get(i)(m)
	}
}

func (m *Main) RegisterAdjSyncCounterHook(f adjSyncCounterHook, dep ...*dep.Dep) {
	m.adjSyncCounterHookVec.Add(f, dep...)
}

func (m *Main) CallAdjGetCounterHooks(adj Adj, f AdjGetCounterHandler, clear bool) {
	for i := range m.adjGetCounterHooks {
		m.adjGetCounterHookVec.Get(i)(m, adj, f, clear)
	}
}

func (m *Main) RegisterAdjGetCounterHook(f adjGetCounterHook, dep ...*dep.Dep) {
	m.adjGetCounterHookVec.Add(f, dep...)
}

func (ma *multipathAdjacency) free(m *Main) {
	dbgvnet.Adj.Logf("free: %v CallAdjDelHooks(%v)\n", ma.adj, ma.adj)
	m.CallAdjDelHooks(ma.adj)

	mm := &m.multipathMain
	nhs := mm.getNextHopBlock(&ma.resolvedNextHops)
	i, ok := mm.nextHopHash.Unset(nhs)
	if !ok {
		panic("unknown multipath adjacency")
	}
	mm.nextHopHashValues[i] = nextHopHashValue{
		heapOffset: ^uint32(0),
		adj:        AdjNil,
	}

	m.PoisonAdj(ma.adj)
	m.FreeAdj(ma.adj)

	poolIndex := ma.index
	mm.freeNextHopBlock(&ma.givenNextHops)
	mm.freeNextHopBlock(&ma.resolvedNextHops)
	ma.invalidate()
	mm.mpAdjPool.PutIndex(poolIndex)
}

func (m *adjacencyMain) validateCounter(a Adj) {
	for _, t := range m.threads {
		t.counters.Validate(uint(a))
	}
}
func (m *adjacencyMain) clearCounter(a Adj) {
	for _, t := range m.threads {
		t.counters.Clear(uint(a))
	}
}

func (m *Main) ClearAdjCounters() {
	for _, t := range m.threads {
		t.counters.ClearAll()
	}
	f := func(tag string, v vnet.CombinedCounter) {}
	m.adjacencyHeap.Foreach(func(o, l uint) {
		const clear = true
		m.CallAdjGetCounterHooks(Adj(o), f, clear)
	})
}

func (m *Main) ForeachAdjCounter(a Adj, f AdjGetCounterHandler) {
	var v vnet.CombinedCounter
	for _, t := range m.threads {
		var u vnet.CombinedCounter
		t.counters.Get(uint(a), &u)
		v.Add(&u)
	}
	f("", v)
	const clear = false
	m.CallAdjGetCounterHooks(a, f, clear)
	return
}

func (m *adjacencyMain) EqualAdj(ai0, ai1 Adj) (same bool) {
	a0, a1 := &m.adjacencyHeap.elts[ai0], &m.adjacencyHeap.elts[ai1]
	ni0, ni1 := a0.LookupNextIndex, a1.LookupNextIndex
	if ni0 != ni1 {
		return
	}
	switch ni0 {
	case LookupNextGlean:
		if a0.Index != a1.Index {
			return
		}
	case LookupNextRewrite:
		if string(a0.Rewrite.Slice()) != string(a1.Rewrite.Slice()) {
			return
		}
	}
	same = true
	return
}

func (m *adjacencyMain) GetAdj(a Adj) (as []Adjacency) { return m.adjacencyHeap.Slice(uint(a)) }
func (m *adjacencyMain) GetAdjRewriteSi(a Adj) (si vnet.Si, ok bool) {
	si = vnet.SiNil
	as := m.GetAdj(a)
	if as[0].LookupNextIndex == LookupNextRewrite {
		si = as[0].Rewrite.Si
		ok = true
	}
	return
}

func (m *adjacencyMain) NewAdjWithTemplate(n uint, template *Adjacency) (ai Adj, as []Adjacency) {
	ai = Adj(m.adjacencyHeap.Get(n))
	m.validateCounter(ai)
	as = m.GetAdj(ai)
	for i := range as {
		if template != nil {
			as[i] = *template
		}
		as[i].Si = vnet.SiNil
		as[i].NAdj = uint16(n)
		as[i].Index = ^uint32(0)
		m.clearCounter(ai + Adj(i))
	}
	return
}
func (m *adjacencyMain) NewAdj(n uint) (Adj, []Adjacency) { return m.NewAdjWithTemplate(n, nil) }

func (m *multipathMain) init() {
	m.nextHopHash.Init(m, 32)
}

func (m *Main) adjacencyInit() {
	m.multipathMain.init()

	// Build special adjacencies.
	for i, v := range []struct {
		Adj
		LookupNext
	}{
		{Adj: AdjMiss, LookupNext: LookupNextMiss}, // must be in order 0 1 2 else panic below triggers.
		{Adj: AdjDrop, LookupNext: LookupNextDrop},
		{Adj: AdjPunt, LookupNext: LookupNextPunt},
	} {
		var as []Adjacency
		a, as := m.NewAdj(1)
		m.specialAdj[i] = a
		as[0].LookupNextIndex = v.LookupNext
		if got, want := a, v.Adj; got != want {
			panic(fmt.Errorf("special adjacency index mismatch got %d != want %d", got, want))
		}
		m.CallAdjAddHooks(a)
	}
}
