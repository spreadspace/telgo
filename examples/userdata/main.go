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
)

func whoami(c *telgo.TelnetClient, args []string, hostname string) bool {
	c.Sayln("%s @ (%s)", c.UserData.(string), hostname)
	return false
}

func setname(c *telgo.TelnetClient, args []string, hostname string) bool {
	if len(args) != 2 {
		c.Sayln("invalid number of arguments!")
		return false
	}
	c.UserData = args[1]
	return false
}

func main() {
	globalUserdata := "test"

	// This is one example of how to use the UserData field of the TelnetClient
	// struct.
	// Export data for all clients as closures to the telgo command functions
	// clients can then use the UserData field for client specific data which is
	// not shared between all connected clients without the need to have an extra
	// struct containing pointers to global and client specific data structures.
	cmdlist := make(telgo.TelgoCmdList)
	cmdlist["whoami"] = func(c *telgo.TelnetClient, args []string) bool { return whoami(c, args, globalUserdata) }
	cmdlist["setname"] = func(c *telgo.TelnetClient, args []string) bool { return setname(c, args, globalUserdata) }

	s := telgo.NewTelnetServer(":7023", "userdata> ", cmdlist, "anonymous")
	if err := s.Run(); err != nil {
		fmt.Printf("telnet server returned: %s", err)
	}
}
