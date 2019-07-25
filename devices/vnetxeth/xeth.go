// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnetxeth

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/platinasystems/elib/cli"
	"github.com/platinasystems/elib/loop"
	"github.com/platinasystems/elib/parse"
	"github.com/platinasystems/vnet"
	"github.com/platinasystems/xeth"
)

const PackageName = "xeth"

type thisT struct {
	vnet.Package
	loop.Node

	stopch chan<- struct{}

	xeth *xeth.Task

	rxmsg struct {
		sync.Mutex
		hooks []func(msg interface{})
	}
}

var this thisT

type NamedAttr struct {
	Name string
	Attr interface{}
}

var DetailedAttrs = []NamedAttr{
	{"ha", xeth.IfInfoHardwareAddrAttr},
	{"iff", xeth.IfInfoFlagsAttr},
	{"ipnets", xeth.IPNetsAttr},
	{"ethtool-flags", xeth.EthtoolFlagsAttr},
	{"speed", xeth.EthtoolSpeedAttr},
	{"autoneg", xeth.EthtoolAutoNegAttr},
	{"duplex", xeth.EthtoolDuplexAttr},
	{"devport", xeth.EthtoolDevPortAttr},
	{"uppers", xeth.UppersAttr},
	{"lowers", xeth.LowersAttr},
}

var Less = func(i, j xeth.Xid) bool {
	return i.Attrs().IfInfoName() < j.Attrs().IfInfoName()
}

func DetailAttr(name string, key interface{}) {
	DetailedAttrs = append(DetailedAttrs, NamedAttr{name, key})
}

func AddRxMsgHook(hook func(msg interface{})) {
	this.rxmsg.Lock()
	defer this.rxmsg.Unlock()
	this.rxmsg.hooks = append(this.rxmsg.hooks, hook)
}

func RunInit() {
	if _, ok := vnet.PackageByName(PackageName); ok {
		return
	}
	vnet.AddPackage(PackageName, &this)
	counters := vnet.AtomicCounters{
		// please sort with editor
		{"cloned", (*uint64)(&xeth.Cloned)},
		{"dropped", (*uint64)(&xeth.Dropped)},
		{"event-actions", &eventActions},
		{"parsed", (*uint64)(&xeth.Parsed)},
		{"sent", (*uint64)(&xeth.Sent)},
	}
	for _, cmd := range []*cli.Command{
		&cli.Command{
			Name:      "clear xeth counter",
			ShortHelp: "clear all or matching xeth counter(s)",
			Action:    counters.Clear,
		},
		&cli.Command{
			Name:      "show xeth counter",
			ShortHelp: "show all or matching xeth counter(s)",
			Action:    counters.Show,
		},
		&cli.Command{
			Name:      "show xeth interface",
			ShortHelp: "show all or matching xeth inferface(s)",
			Action:    this.show,
		},
	} {
		vnet.CliAdd(cmd)
	}
}

func SetCarrier(xid xeth.Xid, up bool) {
	this.xeth.SetCarrier(xid, up)
}

func (*thisT) Init() (err error) {
	l := vnet.GetLoop()
	l.RegisterNode(&this, "xeth")
	this.xeth, err = xeth.Start(&vnet.WG, vnet.StopCh)
	if err != nil {
		return err
	}
	this.xeth.DumpIfInfo()
	go goevents()
	return nil
}

func (*thisT) show(c cli.Commander, iw cli.Writer, in *cli.Input) error {
	var xids []xeth.Xid
	var detail, stats, sup, adv, lpadv bool
	var reName, reStat parse.Regexp

	for !in.End() {
		switch {
		case in.Parse("m%*atching %v", &reName):
		case in.Parse("d%*etail"):
			detail = true
		case in.Parse("st%*atistics"):
			stats = true
		case in.Parse("pa%*ttern %v", &reStat):
		case in.Parse("su%*pported"):
			sup = true
		case in.Parse("ad%*vertising"):
			adv = true
		case in.Parse("lp%*advertising"):
			lpadv = true
		default:
			in.ParseError()
		}
	}
	if detail {
		sort.SliceStable(DetailedAttrs, func(i, j int) bool {
			return DetailedAttrs[i].Name < DetailedAttrs[j].Name
		})
	}

	xeth.Range(func(xid xeth.Xid) bool {
		if reName.Valid() {
			if !reName.MatchString(xid.Attrs().IfInfoName()) {
				return true
			}
		}
		xids = append(xids, xid)
		return true
	})
	sort.SliceStable(xids, func(i, j int) bool {
		return Less(xids[i], xids[j])
	})
	w := bufio.NewWriter(iw)
	for _, xid := range xids {
		showIfInfo(w, xid, detail)
		if stats {
			showStats(w, &reStat, xid)
		}
		if sup {
			showLinkModes(w, "supported",
				xid.Attrs().LinkModesSupported())
		}
		if adv {
			showLinkModes(w, "advertising",
				xid.Attrs().LinkModesAdvertising())
		}
		if lpadv {
			showLinkModes(w, "lpadvertising",
				xid.Attrs().LinkModesLPAdvertising())
		}
	}
	w.Flush()
	return nil
}

func showIfInfo(w io.Writer, xid xeth.Xid, detail bool) {
	var sum int

	attrs := xid.Attrs()
	n, _ := fmt.Fprint(w, attrs.IfInfoName())
	sum += n
	n, _ = fmt.Fprint(w, ": xid ", uint32(xid))
	sum += n
	n, _ = fmt.Fprint(w, ", ifindex ", attrs.IfInfoIfIndex())
	sum += n
	n, _ = fmt.Fprint(w, ", netns ", attrs.IfInfoNetNs())
	sum += n
	if !detail {
		fmt.Fprintln(w)
		return
	}
	m := attrs.Map()
	for _, na := range DetailedAttrs {
		if v, ok := m.Load(na.Attr); ok {
			s := na.Name
			if _, isbool := v.(bool); !isbool {
				s = fmt.Sprint(na.Name, " ", v)
			}
			if sum == 0 {
				if len(s) > (80 - 8) {
					fmt.Fprint(w, "\t", s, ",\n")
				} else {
					fmt.Fprint(w, "\t", s)
					sum = 8 + len(s)
				}
			} else if sum+len(s) > 80 {
				if len(s) > (80 - 8) {
					fmt.Fprint(w, ",\n\t", s, ",\n")
					sum = 0
				} else {
					fmt.Fprint(w, ",\n\t", s)
					sum = 8 + len(s)
				}
			} else {
				fmt.Fprint(w, ", ", s)
				sum += 2 + len(s)
			}
		}
	}
	if sum != 0 {
		fmt.Fprintln(w)
	}
}

func showStats(w io.Writer, re *parse.Regexp, xid xeth.Xid) {
	attrs := xid.Attrs()
	names := attrs.StatNames()
	stats := attrs.Stats()
	if len(names) == 0 {
		return
	}
	if len(names) != len(stats) {
		panic(fmt.Errorf("%v has mis-matched name/stat tables", xid))
	}
	for i, name := range names {
		if re.Valid() {
			if !re.MatchString(name) {
				continue
			}
		}
		val := atomic.LoadUint64(&stats[i])
		if false && val != 0 {
			continue
		}
		fmt.Fprintf(w, "%20d %s\n", val, name)
	}
}

func showLinkModes(w io.Writer, name string, modes xeth.EthtoolLinkModeBits) {
	fmt.Fprint(w, "\t", name)
	s := fmt.Sprint(modes)
	if 8+len(name)+1+len(s) < 80 {
		fmt.Fprint(w, " ", s, "\n")
		return
	}
	for {
		var i int
		fmt.Fprint(w, "\n\t\t")
		if len(s) < 64 {
			fmt.Fprint(w, s, "\n")
			return
		}
		for i = 64; s[i] != ' '; i-- {
		}
		fmt.Fprint(w, s[:i])
		s = s[i+1:]
	}
}
