// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnet

type RxTx int

const (
	Rx RxTx = iota
	Tx
)

func (rxtx RxTx) String() (s string) {
	if rxtx == Rx {
		return "rx"
	}
	return "tx"
}
