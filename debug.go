// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build debug

package vnet

import (
	"github.com/platinasystems/elib/hw"

	"fmt"
	"unsafe"
)

func debugInit() {
	if got, want := unsafe.Sizeof(Ref{}), unsafe.Sizeof(hw.Ref{}); got != want {
		panic(fmt.Errorf("ref size %d %d", got, want))
	}
}
