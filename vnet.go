// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnet

import (
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/platinasystems/elib/cli"
	"github.com/platinasystems/elib/cpu"
	"github.com/platinasystems/elib/elog"
	"github.com/platinasystems/elib/hw"
	"github.com/platinasystems/elib/loop"
	"github.com/platinasystems/elib/parse"
)

// Main structure.
var vnet struct {
	hw.BufferMain
	loop    loop.Loop
	cliMain cliMain
	stopch  chan<- struct{}
}

// each go-routine shoule vnet.WG.Add(1) then defer vnet.WG.Done()
var WG sync.WaitGroup

// each go-routine should also quit when StopCh is closed
var StopCh <-chan struct{}

func Init() {
	stopch := make(chan struct{})
	StopCh = stopch
	vnet.stopch = stopch

	go func() {
		WG.Add(1)
		defer WG.Done()

		// FIXME-XETH how is this used?
		signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)

		sigch := make(chan os.Signal)
		signal.Notify(sigch, syscall.SIGTERM, syscall.SIGINT)
		defer signal.Stop(sigch)
		for {
			select {
			case <-StopCh:
				return
			case <-sigch:
				Quit()
				return
			}
		}
	}()
}

func Quit() {
	close(vnet.stopch)
	vnet.loop.Quit()
}

var RunInitHooks = InitHooks{
	hooks: []func(){
		countersCliInit,
		packageCliInit,
		logInit,
		debugInit,
		bufCliInit,
		errorCliInit,
	},
}

var LoopInitHooks = InitHooks{
	hooks: []func(){
		errorNodeInit,
		cliInit,
		eventNodeInit,
	},
}

// Largest number of outstanding transmit buffers before we suspend.
const MaxOutstandingTxRefs = 16 * MaxVectorLen

var suspendLimits = loop.SuspendLimits{
	Suspend: MaxOutstandingTxRefs,
	Resume:  MaxOutstandingTxRefs / 2,
}

var (
	Something uint64
	Another   uint64
)

func countersCliInit() {
	counters := AtomicCounters{
		// please sort with editor
		{"another", &Another},
		{"something", &Something},
	}
	atomic.StoreUint64(&Something, 123)
	atomic.StoreUint64(&Another, 456)
	for _, cmd := range []*cli.Command{
		&cli.Command{
			Name:      "clear counter",
			ShortHelp: "clear all or matching vnet counter(s)",
			Action:    counters.Clear,
		},
		&cli.Command{
			Name:      "show counter",
			ShortHelp: "show all or matching vnet counter(s)",
			Action:    counters.Show,
		},
	} {
		CliAdd(cmd)
	}
}

func AddBufferPool(p *BufferPool) {
	vnet.BufferMain.AddBufferPool((*hw.BufferPool)(p))
}

func AddNamedNext(n Noder, name string) uint {
	if nextIndex, err := vnet.loop.AddNamedNext(n, name); err == nil {
		return nextIndex
	} else {
		panic(err)
	}
}

func vnetConfigure(in *parse.Input) (err error) {
	if err = ConfigurePackages(in); err != nil {
		return
	}
	if err = InitPackages(); err != nil {
		return
	}
	return
}

func CurrentEvent() *loop.Event {
	return eventNode.CurrentEvent()
}

func GetLoop() *loop.Loop {
	return &vnet.loop
}

/* FIXME-XETH
func ForeachHwIf(unixOnly bool, f func(hi Hi)) {
	for i := range vnet.hwIferPool.elts {
		if vnet.hwIferPool.IsFree(uint(i)) {
			continue
		}
		hwifer := vnet.hwIferPool.elts[i]
		if unixOnly && !hwifer.IsUnix() {
			continue
		}
		h := hwifer.GetHwIf()
		f(h.hi)
	}
}

func ForeachSwIf(f func(si Si)) {
	vnet.swInterfaces.ForeachIndex(func(i uint) {
		f(Si(i))
	})
}

func HwLessThan(a, b *HwIf) (t bool) {
	ha, hb := vnet.HwIfer(a.hi), vnet.HwIfer(b.hi)
	da, db := ha.DriverName(), hb.DriverName()
	if da != db {
		t = da < db
	} else {
		t = ha.LessThan(hb)
	}
	return
}

func SwLessThan(a, b *SwIf) (t bool) {
	hwa, hwb := vnet.SupHwIf(a), vnet.SupHwIf(b)
	if hwa != nil && hwb != nil {
		if hwa != hwb {
			t = vnet.HwLessThan(hwa, hwb)
		} else {
			ha := vnet.HwIfer(hwa.hi)
			t = ha.LessThanId(a.IfId(), b.IfId())
	} else if a.kind != b.kind {
		// Different kind?  Sort by increasing kind.
		t = a.kind < b.kind
	} else {
		// Same kind.
		t = a.Name() < b.Name()
	}
	return
}

func NewSwIf(kind SwIfKind, x Xer) Si {
	return addDelSwInterface(SiNil, kind, x, false)
}

func DelSwIf(si Si) {
	addDelSwInterface(si, 0, nil, true)
}

func NewSwSubInterface(supSi Si, x Xer) Si {
	return addDelSwInterface(SiNil, SwIfKindSoftware, x, false)
}
FIXME-XETH */

func RegisterNode(n Noder, format string, args ...interface{}) {
	vnet.loop.RegisterNode(n, format, args...)
	x := n.GetVnetNode()

	x.errorRefs = make([]ErrorRef, len(x.Errors))
	for i := range x.Errors {
		er := ^ErrorRef(0)
		if len(x.Errors[i]) > 0 {
			er = x.NewError(x.Errors[i])
		}
		x.errorRefs[i] = er
	}
}

func RegisterInOutNode(n InOutNoder, name string, args ...interface{}) {
	RegisterNode(n, name, args...)
	x := n.GetInOutNode()
	x.t = n
}

func RegisterInputNode(n InputNoder, name string, args ...interface{}) {
	RegisterNode(n, name, args...)
	x := n.GetInputNode()
	x.o = n
}

func RegisterOutputNode(n OutputNoder, name string, args ...interface{}) {
	RegisterNode(n, name, args...)
	x := n.GetOutputNode()
	x.o = n
}

/* FIXME-XETH
func RegisterHwInterface(h HwInterfacer, x Xer) error {
	hi := Hi(vnet.hwIferPool.GetIndex())
	vnet.hwIferPool.elts[hi] = h
	hw := h.GetHwIf()
	hw.hi = hi
	hw.X.Xer = x
	x.Vi(Vi(hi))
	hw.Dub(x.Name())
	hw.Identify(x.Xid())
	hw.si = v.NewSwIf(SwIfKindHardware, x)

	isDel := false
	for i := range vnet.interfaceMain.hwIfAddDelHooks.hooks {
		err := vnet.interfaceMain.hwIfAddDelHooks.Get(i)(hi, isDel)
		if err != nil {
			panic(err) // how to recover?
		}
	}
	return nil
}
FIXME-XETH */

func Run(in *parse.Input) error {
	defer WG.Wait()
	RunInitHooks.Run()
	loop.AddInit(func(l *loop.Loop) {
		/* FIXME-XETH
		vnet.interfaceMain.init()
		FIXME-XETH */
		LoopInitHooks.Run()
		if err := vnetConfigure(in); err != nil {
			panic(err)
		}
	})
	vnet.loop.Run()
	return ExitPackages()
}

func SignalEvent(r Eventer) {
	eventNode.SignalEventp(r, elog.PointerToFirstArg(&vnet))
}

func SignalEventAfter(r Eventer, dt float64) {
	eventNode.SignalEventAfterp(r, dt, elog.PointerToFirstArg(&vnet))
}

func TimeDiff(t0, t1 cpu.Time) float64 {
	return vnet.loop.TimeDiff(t1, t0)
}

type IsDel bool

func (x IsDel) String() string {
	if x {
		return "delete"
	}
	return "add"
}

type ActionType int

const (
	PreVnetd       ActionType = iota // before vnetd is started
	ReadyVnetd                       // vnetd has declared it's ready
	PostReadyVnetd                   // vnetd processing something initated from previous state
	Dynamic                          // free-run case
)

/* FIXME-XETH this needs better abstraction as vnet shouldn't need anything
 * bridge (svi) specific...
// Could collapse all vnet Hooks calls into this message
// to avoid direct function calls from vnet to fe
type SviVnetFeMsg struct {
	data []byte
}

const (
	MSG_FROM_VNET = iota
	MSG_SVI_BRIDGE_ADD
	MSG_SVI_BRIDGE_DELETE
	MSG_SVI_BRIDGE_MEMBER_ADD
	MSG_SVI_BRIDGE_MEMBER_DELETE
)

const (
	MSG_FROM_FE = iota + 128
	MSG_SVI_FDB_ADD
	MSG_SVI_FDB_DELETE
)

type FromFeMsg struct {
	MsgId    uint8
	Addr     [6]uint8
	Stag     uint16
	PipePort uint16
}

type BridgeNotifierFn func()

var SviFromFeCh chan FromFeMsg // for l2-mod learning event reporting

// simplified hooks for direct calls to fe1 from vnet
type BridgeAddDelHook_t func(brsi Si, stag uint16, puntIndex uint8, addr net.HardwareAddr, isAdd bool) (err error)

type BridgeMemberAddDelHook_t func(stag uint16, brmSi Si, pipe_port uint16, ctag uint16, isAdd bool, nBrm uint8) (err error)

type BridgeMemberLookup_t func(stag uint16, addr net.HardwareAddr) (pipe_port uint16, err error)

func (v *Vnet) RegisterBridgeAddDelHook(h BridgeAddDelHook_t) {
	v.BridgeAddDelHook = h
}
func (v *Vnet) RegisterBridgeMemberAddDelHook(h BridgeMemberAddDelHook_t) {
	v.BridgeMemberAddDelHook = h
}
func (v *Vnet) RegisterBridgeMemberLookup(h BridgeMemberLookup_t) {
	v.BridgeMemberLookup = h
}
FIXME-XETH */
