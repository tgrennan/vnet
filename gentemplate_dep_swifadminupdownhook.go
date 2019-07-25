// autogenerated: do not edit!
// generated from gentemplate [gentemplate -id SwIfAdminUpDownHook -d Package=vnet -d DepsType=SwIfAdminUpDownHookVec -d Type=SwIfAdminUpDownHook -d Data=hooks github.com/platinasystems/elib/dep/dep.tmpl]

// Copyright 2016 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

package vnet

import (
	"github.com/platinasystems/elib/dep"
)

type SwIfAdminUpDownHookVec struct {
	deps  dep.Deps
	hooks []SwIfAdminUpDownHook
}

func (t *SwIfAdminUpDownHookVec) Len() int {
	return t.deps.Len()
}

func (t *SwIfAdminUpDownHookVec) Get(i int) SwIfAdminUpDownHook {
	return t.hooks[t.deps.Index(i)]
}

func (t *SwIfAdminUpDownHookVec) Add(x SwIfAdminUpDownHook, ds ...*dep.Dep) {
	if len(ds) == 0 {
		t.deps.Add(&dep.Dep{})
	} else {
		t.deps.Add(ds[0])
	}
	t.hooks = append(t.hooks, x)
}
