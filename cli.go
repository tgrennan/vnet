// Copyright 2016 Platina Systems, Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vnet

import (
	"github.com/platinasystems/elib/cli"
	"github.com/platinasystems/elib/parse"
)

var CliAdd = vnet.loop.CliAdd
var Fatalf = vnet.loop.Fatalf

type cliListener struct {
	socketConfig string
	serverConfig cli.ServerConfig
	server       *cli.Server
}

func (l *cliListener) Parse(in *parse.Input) {
	for !in.End() {
		switch {
		case in.Parse("no-prompt"):
			l.serverConfig.DisablePrompt = true
		case in.Parse("enable-quit"):
			l.serverConfig.EnableQuit = true
		case in.Parse("socket %s", &l.socketConfig):
		default:
			in.ParseError()
		}
	}
}

type cliMain struct {
	Package
	enableStdin bool
	listeners   []cliListener
}

func cliInit() {
	AddPackage("cli", &vnet.cliMain)
}

func (m *cliMain) Configure(in *parse.Input) {
	for !in.End() {
		var (
			l  cliListener
			li parse.Input
		)
		switch {
		case in.Parse("listen %v", &li) && li.Parse("%v", &l):
			m.listeners = append(m.listeners, l)
		case in.Parse("stdin"):
			m.enableStdin = true
		default:
			in.ParseError()
		}
	}
}

func (m *cliMain) Init() (err error) {
	vnet.loop.Cli.Prompt = "vnet# "
	vnet.loop.Cli.SetEventNode(&eventNode)
	if m.enableStdin {
		vnet.loop.Cli.AddStdin()
	}
	for i := range m.listeners {
		l := &m.listeners[i]
		l.server, err = vnet.loop.Cli.AddServer(l.socketConfig, l.serverConfig)
		if err != nil {
			return
		}
	}
	vnet.loop.Cli.Start()
	return
}

func (m *cliMain) Exit() (err error) {
	for i := range m.listeners {
		l := &m.listeners[i]
		l.server.Close()
	}
	vnet.loop.Cli.Exit()
	return
}
