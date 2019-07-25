// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnet

import "sync"

type InitHooks struct {
	sync.Mutex
	hooks []func()
}

func (ih *InitHooks) Add(hook func()) {
	ih.Lock()
	defer ih.Unlock()
	ih.hooks = append(ih.hooks, hook)
}

func (ih *InitHooks) Run() {
	ih.Lock()
	defer ih.Unlock()
	for _, hook := range ih.hooks {
		hook()
	}
}
