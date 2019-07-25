// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build !nolog

package vnet

import "github.com/platinasystems/elib"

func logInit() {
	elib.Logger = &vnet.loop
}

var Logf = vnet.loop.Logf
var Logln = vnet.loop.Logln
