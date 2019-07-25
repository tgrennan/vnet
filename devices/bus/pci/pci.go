// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pci

import (
	"github.com/platinasystems/elib/hw/pci"
	"github.com/platinasystems/vnet"
)

type pciDiscover struct {
	vnet.Package
}

func (d *pciDiscover) Init() error {
	return pci.DiscoverDevices(pci.DefaultBus, d.Vnet)
}

func (d *pciDiscover) Exit() error {
	return pci.CloseDiscoveredDevices(pci.DefaultBus)
}

func Init() {
	name := "pci-discovery"
	if _, ok := vnat.PackageByName(name); !ok {
		vnet.AddPackage(name, &pciDiscover{})
	}
}
