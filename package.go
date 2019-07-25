// Copyright © 2016-2019 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnet

import (
	"bufio"
	"fmt"
	"os"
	"sort"

	"github.com/platinasystems/elib/cli"
	"github.com/platinasystems/elib/dep"
	"github.com/platinasystems/elib/elog"
	"github.com/platinasystems/elib/parse"
)

type Packager interface {
	GetPackage() *Package
	Configure(in *parse.Input)
	Init() (err error)
	Exit() (err error)
}

const (
	// Dependencies: packages this package depends on.
	forward = iota
	// Anti-dependencies: packages that are dependent on this package.
	anti
	nDepType
)

type Package struct {
	name string

	depMap [nDepType]map[string]struct{}

	dep dep.Dep
}

func (p *Package) GetPackage() *Package { return p }
func (p *Package) Init() (err error)    { return } // likely overridden
func (p *Package) Exit() (err error)    { return } // likely overridden
func (p *Package) Configure(in *parse.Input) {
	panic(cli.ParseError)
}

var packageMain struct {
	packageByName parse.StringMap
	packages      []Packager
	deps          dep.Deps
}

func (p *Package) addDep(name string, typ int) {
	if len(name) == 0 {
		panic("empty dependency")
	}
	if p.depMap[typ] == nil {
		p.depMap[typ] = make(map[string]struct{})
	}
	p.depMap[typ][name] = struct{}{}
}
func (p *Package) DependsOn(names ...string) {
	for i := range names {
		p.addDep(names[i], forward)
	}
}
func (p *Package) DependedOnBy(names ...string) {
	for i := range names {
		p.addDep(names[i], anti)
	}
}

func AddPackage(name string, r Packager) (pi uint) {
	// Package with index zero is always empty.
	// Protects against uninitialized package index variables.
	if len(packageMain.packages) == 0 {
		packageMain.packages =
			append(packageMain.packages, &Package{name: "(empty)"})
	}

	// Already registered
	var ok bool
	if pi, ok = packageMain.packageByName[name]; ok {
		return
	}

	pi = uint(len(packageMain.packages))
	packageMain.packageByName.Set(name, pi)
	packageMain.packages = append(packageMain.packages, r)
	p := r.GetPackage()
	p.name = name
	return
}

func PackageByName(name string) (i uint, ok bool) {
	i, ok = packageMain.packageByName[name]
	return
}

func GetPackage(i uint) Packager {
	return packageMain.packages[i]
}

func (p *Package) configure(r Packager, in *parse.Input) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("configure %s: %s: %s", p.name, e, in)
		}
	}()
	r.Configure(in)
	return
}

func ConfigurePackages(in *parse.Input) (err error) {
	// Parse package configuration.
	for !in.End() {
		var (
			i     uint
			subIn parse.Input
		)
		switch {
		case in.Parse("%v %v", packageMain.packageByName, &i, &subIn):
			r := packageMain.packages[i]
			p := r.GetPackage()
			if err = p.configure(r, &subIn); err != nil {
				return
			}
		case in.Parse("vnet %v", &subIn):
			if err = Configure(&subIn); err != nil {
				return
			}
		case in.Parse("elog %v", &subIn):
			if err = elog.Configure(&subIn); err != nil {
				return
			}
		default:
			in.ParseError()
		}
	}
	return
}

func Configure(in *parse.Input) (err error) {
	for !in.End() {
		var logFile string
		switch {
		case in.Parse("log %v", &logFile):
			var f *os.File
			f, err = os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE|os.O_SYNC, 0644)
			if err != nil {
				return
			}
			vnet.loop.Config.LogWriter = f
		case in.Parse("quit %f", &vnet.loop.Config.QuitAfterDuration):
		case in.Parse("quit"):
			vnet.loop.Config.QuitAfterDuration = 1e-6 // must be positive to enable
		default:
			in.ParseError()
		}
	}
	return
}

func showPackages(c cli.Commander, iw cli.Writer, in *cli.Input) error {
	var names []string

	for name := range packageMain.packageByName {
		names = append(names, name)
	}
	sort.Strings(names)
	w := bufio.NewWriter(iw)
	for _, name := range names {
		fmt.Fprint(w, name)
		w.WriteByte('\n')
	}
	w.Flush()
	return nil
}

func packageCliInit() {
	for _, cmd := range []*cli.Command{
		&cli.Command{
			Name:      "show packages",
			ShortHelp: "show registered vnet packages",
			Action:    showPackages,
		},
	} {
		CliAdd(cmd)
	}
}

func InitPackages() (err error) {
	// Resolve package dependencies.
	for i := range packageMain.packages {
		p := packageMain.packages[i].GetPackage()
		for typ := range p.depMap {
			for name := range p.depMap[typ] {
				j, ok := packageMain.packageByName[name]
				if ok {
					d := packageMain.packages[j].GetPackage()
					if typ == forward {
						p.dep.Deps = append(p.dep.Deps, &d.dep)
					} else {
						p.dep.AntiDeps = append(p.dep.AntiDeps, &d.dep)
					}
				} else {
					panic(fmt.Errorf("%s: unknown dependent package `%s'", p.name, name))
				}
			}
		}
		packageMain.deps.Add(&p.dep)
	}

	// Call package init functions.
	for i := range packageMain.packages {
		p := packageMain.packages[packageMain.deps.Index(i)]
		err = p.Init()
		if err != nil {
			return
		}
	}
	return
}

func ExitPackages() (err error) {
	for i := range packageMain.packages {
		p := packageMain.packages[packageMain.deps.IndexReverse(i)]
		err = p.Exit()
		if err != nil {
			return
		}
	}
	return
}
