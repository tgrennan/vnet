// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnet

import "fmt"

// To clarify units: 1e9 * vnet.Bps
const (
	Bps    = 1e0
	Kbps   = 1e3
	Mbps   = 1e6
	Gbps   = 1e9
	Tbps   = 1e12
	Bytes  = 1
	Kbytes = 1 << 10
	Mbytes = 1 << 20
	Gbytes = 1 << 30
)

type Bandwidth float64

func (b Bandwidth) Format(f fmt.State, c rune) {
	var unit Bandwidth
	var suffix string
	switch {
	case b == 0:
		fmt.Fprint(f, "autoneg")
		return
	case b < Kbps:
		unit = Bps
	case b <= Mbps:
		unit = Kbps
		suffix = "Kbps"
	case b <= Gbps:
		unit = Mbps
		suffix = "Mbps"
	case b <= Tbps:
		unit = Gbps
		suffix = "Gbps"
	default:
		unit = Tbps
		suffix = "Tbps"
	}
	fmt.Fprintf(f, "%g%s", b/unit, suffix)
}
