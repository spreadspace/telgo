//
//  telgo
//
//
//  Copyright (C) 2015 Christian Pointner <equinox@helsinki.at>
//
//  This file is part of telgo.
//
//  telgo is free software: you can redistribute it and/or modify
//  it under the terms of the GNU General Public License as published by
//  the Free Software Foundation, either version 3 of the License, or
//  any later version.
//
//  telgo is distributed in the hope that it will be useful,
//  but WITHOUT ANY WARRANTY; without even the implied warranty of
//  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
//  GNU General Public License for more details.
//
//  You should have received a copy of the GNU General Public License
//  along with telgo. If not, see <http://www.gnu.org/licenses/>.
//

package main

import (
	"fmt"
	"github.com/spreadspace/telgo"
	"strings"
)

func simple_echo(c *telgo.TelnetClient, args []string, cancel <-chan bool) bool {
	c.Say(strings.Join(args, " "))
	return false
}

func main() {
	cmdlist := make(telgo.TelgoCmdList)
	cmdlist["echo"] = simple_echo

	s := telgo.NewTelnetServer(":7023", "simple> ", cmdlist, nil)
	if err := s.Run(); err != nil {
		fmt.Printf("telnet server returned: %s", err)
	}
}
