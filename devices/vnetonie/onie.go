// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnetonie

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"

	"github.com/platinasystems/elib/cli"
	"github.com/platinasystems/elib/parse"
	"github.com/platinasystems/vnet"
)

const PackageName = "onie"
const topdn = "/sys/devices/platform"

type thisT struct {
	vnet.Package
}

var this thisT

var (
	ProductName   string
	PartNumber    string
	SerialNumber  string
	MacBase       net.HardwareAddr
	DeviceVersion uint8
	LabelRevision string
	PlatformName  string
	OnieVersion   string
	NumMacs       int
	Manufacturer  string
	CountryCode   string
	Vendor        string
	DiagVersion   string
	ServiceTag    string
)

var scanners = []struct {
	name string
	scan func(io.Reader)
}{
	{"product_name", func(r io.Reader) {
		if b, err := ioutil.ReadAll(r); err == nil {
			ProductName = string(b)
		}
	}},
	{"part_number", func(r io.Reader) {
		if b, err := ioutil.ReadAll(r); err == nil {
			PartNumber = string(b)
		}
	}},
	{"serial_number", func(r io.Reader) {
		if b, err := ioutil.ReadAll(r); err == nil {
			SerialNumber = string(b)
		}
	}},
	{"mac_base", func(r io.Reader) {
		if b, err := ioutil.ReadAll(r); err == nil {
			MacBase, _ = net.ParseMAC(string(b))
		}
	}},
	{"device_version", func(r io.Reader) {
		fmt.Fscan(r, &DeviceVersion)
	}},
	{"label_revision", func(r io.Reader) {
		if b, err := ioutil.ReadAll(r); err == nil {
			LabelRevision = string(b)
		}
	}},
	{"platform_name", func(r io.Reader) {
		if b, err := ioutil.ReadAll(r); err == nil {
			PlatformName = string(b)
		}
	}},
	{"onie_version", func(r io.Reader) {
		if b, err := ioutil.ReadAll(r); err == nil {
			OnieVersion = string(b)
		}
	}},
	{"num_macs", func(r io.Reader) {
		fmt.Fscan(r, &NumMacs)
	}},
	{"manufacturer", func(r io.Reader) {
		if b, err := ioutil.ReadAll(r); err == nil {
			Manufacturer = string(b)
		}
	}},
	{"country_code", func(r io.Reader) {
		if b, err := ioutil.ReadAll(r); err == nil {
			CountryCode = string(b)
		}
	}},
	{"vendor", func(r io.Reader) {
		if b, err := ioutil.ReadAll(r); err == nil {
			Vendor = string(b)
		}
	}},
	{"diag_version", func(r io.Reader) {
		if b, err := ioutil.ReadAll(r); err == nil {
			DiagVersion = string(b)
		}
	}},
	{"service_tag", func(r io.Reader) {
		if b, err := ioutil.ReadAll(r); err == nil {
			ServiceTag = string(b)
		}
	}},
}

var printers = []struct {
	name string
	show func(io.Writer)
}{
	{"product_name", func(w io.Writer) {
		fmt.Fprint(w, ProductName)
	}},
	{"part_number", func(w io.Writer) {
		fmt.Fprint(w, PartNumber)
	}},
	{"serial_number", func(w io.Writer) {
		fmt.Fprint(w, SerialNumber)
	}},
	{"mac_base", func(w io.Writer) {
		fmt.Fprint(w, MacBase)
	}},
	{"device_version", func(w io.Writer) {
		fmt.Fprint(w, DeviceVersion)
	}},
	{"label_revision", func(w io.Writer) {
		fmt.Fprint(w, LabelRevision)
	}},
	{"platform_name", func(w io.Writer) {
		fmt.Fprint(w, PlatformName)
	}},
	{"onie_version", func(w io.Writer) {
		fmt.Fprint(w, OnieVersion)
	}},
	{"num_macs", func(w io.Writer) {
		fmt.Fprint(w, NumMacs)
	}},
	{"manufacturer", func(w io.Writer) {
		fmt.Fprint(w, Manufacturer)
	}},
	{"country_code", func(w io.Writer) {
		fmt.Fprint(w, CountryCode)
	}},
	{"vendor", func(w io.Writer) {
		fmt.Fprint(w, Vendor)
	}},
	{"diag_version", func(w io.Writer) {
		fmt.Fprint(w, DiagVersion)
	}},
	{"service_tag", func(w io.Writer) {
		fmt.Fprint(w, ServiceTag)
	}},
}

func RunInit() {
	var err error
	defer func() {
		if err != nil {
			fmt.Println("onie:", err)
		}
	}()
	if _, ok := vnet.PackageByName(PackageName); ok {
		return
	}
	for _, cmd := range []*cli.Command{
		&cli.Command{
			Name:      "show onie",
			ShortHelp: "show all or matching onie parameters",
			Action:    show,
		},
	} {
		vnet.CliAdd(cmd)
	}
	vnet.AddPackage(PackageName, &this)
	topfis, err := ioutil.ReadDir(topdn)
	if err != nil {
		return
	}
	for _, fi := range topfis {
		oniedn := filepath.Join(topdn, fi.Name(), "onie")
		_, terr := os.Stat(oniedn)
		if terr == nil {
			for _, entry := range scanners {
				fn := filepath.Join(oniedn, entry.name)
				rc, err := os.Open(fn)
				if err == nil {
					entry.scan(rc)
					rc.Close()
				}
			}
			return
		}
	}
	err = fmt.Errorf("%s/PLATFORM/onie not found", topdn)
}

func show(c cli.Commander, iw cli.Writer, in *cli.Input) error {
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
		for _, entry := range printers {
			if re.MatchString(entry.name) {
				fmt.Fprint(w, entry.name, ": ")
				entry.show(w)
				fmt.Fprintln(w)
			}
		}
	} else {
		for _, entry := range printers {
			fmt.Fprint(w, entry.name, ": ")
			entry.show(w)
			fmt.Fprintln(w)
		}
	}
	w.Flush()
	return nil
}
