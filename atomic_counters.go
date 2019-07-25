// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnet

import (
	"bufio"
	"fmt"
	"sync/atomic"

	"github.com/platinasystems/elib/cli"
	"github.com/platinasystems/elib/parse"
)

type AtomicCounters []struct {
	Name string
	Ptr  *uint64
}

func (tbl AtomicCounters) Clear(c cli.Commander, iw cli.Writer,
	in *cli.Input) error {

	var re parse.Regexp
	for !in.End() {
		switch {
		case in.Parse("m%*atching %v", &re):
		default:
			in.ParseError()
		}
	}
	if re.Valid() {
		for _, entry := range tbl {
			if re.MatchString(entry.Name) {
				atomic.StoreUint64(entry.Ptr, 0)
			}
		}
	} else {
		for _, entry := range tbl {
			atomic.StoreUint64(entry.Ptr, 0)
		}
	}
	return nil
}

func (tbl AtomicCounters) Show(c cli.Commander, iw cli.Writer,
	in *cli.Input) error {

	var re parse.Regexp
	for !in.End() {
		switch {
		case in.Parse("m%*atching %v", &re):
		default:
			in.ParseError()
		}
	}
	w := bufio.NewWriter(iw)
	if re.Valid() {
		for _, entry := range tbl {
			if re.MatchString(entry.Name) {
				val := atomic.LoadUint64(entry.Ptr)
				fmt.Fprintf(w, "%20d %s\n", val, entry.Name)
			}
		}
	} else {
		for _, entry := range tbl {
			val := atomic.LoadUint64(entry.Ptr)
			fmt.Fprintf(w, "%20d %s\n", val, entry.Name)
		}
	}
	w.Flush()
	return nil
}
