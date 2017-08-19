// autogenerated: do not edit!
// generated from gentemplate [gentemplate -d Package=vnet -id errorEvent -d Type=errorEvent github.com/platinasystems/go/elib/elog/event.tmpl]

// Copyright 2016 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnet

import (
	"github.com/platinasystems/go/elib/elog"
)

var errorEventType = &elog.EventType{
	Name: "vnet.errorEvent",
}

func init() {
	t := errorEventType
	t.Strings = stringer_errorEvent
	t.Encode = encode_errorEvent
	t.Decode = decode_errorEvent
	elog.RegisterType(errorEventType)
}

func stringer_errorEvent(c *elog.Context, e *elog.Event) []string {
	var x errorEvent
	x.Decode(c, e.Data[:])
	return x.Strings(c)
}

func encode_errorEvent(c *elog.Context, e *elog.Event, b []byte) int {
	var x errorEvent
	x.Decode(c, e.Data[:])
	return x.Encode(c, b)
}

func decode_errorEvent(c *elog.Context, e *elog.Event, b []byte) int {
	var x errorEvent
	x.Decode(c, b)
	return x.Encode(c, e.Data[:])
}

func (x errorEvent) log_errorEvent(b *elog.Buffer, r elog.Caller) {
	e := b.Add(errorEventType, r)
	x.Encode(b.GetContext(), e.Data[:])
}

func (x errorEvent) Log() {
	r := elog.GetCaller(elog.PointerToFirstArg(&x))
	x.log_errorEvent(elog.DefaultBuffer, r)
}

func (x errorEvent) Logc(r elog.Caller) {
	x.log_errorEvent(elog.DefaultBuffer, r)
}

func (x errorEvent) Logb(b *elog.Buffer) {
	r := elog.GetCaller(elog.PointerToFirstArg(&x))
	x.log_errorEvent(b, r)
}

func (x errorEvent) Logbc(b *elog.Buffer, r elog.Caller) {
	x.log_errorEvent(b, r)
}
