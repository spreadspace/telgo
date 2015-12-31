//
//  telgo
//
// Copyright (c) 2015 Christian Pointner <equinox@spreadspace.org>
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
//     * Redistributions of source code must retain the above copyright
//       notice, this list of conditions and the following disclaimer.
//     * Redistributions in binary form must reproduce the above copyright
//       notice, this list of conditions and the following disclaimer in the
//       documentation and/or other materials provided with the distribution.
//     * Neither the name of telgo nor the names of its contributors may be
//       used to endorse or promote products derived from this software without
//       specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
// ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
// WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
// DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
// FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
// DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
// SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
// CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
// OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
//

package main

import (
	"fmt"
	"github.com/spreadspace/telgo"
)

func whoami(c *telgo.Client, args []string, hostname string) bool {
	c.Sayln("%s @ (%s)", c.UserData.(string), hostname)
	return false
}

func setname(c *telgo.Client, args []string, hostname string) bool {
	if len(args) != 2 {
		c.Sayln("invalid number of arguments!")
		return false
	}
	c.UserData = args[1]
	return false
}

func main() {
	globalUserdata := "test"

	// This is one example of how to use the UserData field of the Client
	// struct.
	// Export data for all clients as closures to the telgo command functions
	// clients can then use the UserData field for client specific data which is
	// not shared between all connected clients without the need to have an extra
	// struct containing pointers to global and client specific data structures.
	cmdlist := make(telgo.CmdList)
	cmdlist["whoami"] = func(c *telgo.Client, args []string) bool { return whoami(c, args, globalUserdata) }
	cmdlist["setname"] = func(c *telgo.Client, args []string) bool { return setname(c, args, globalUserdata) }

	s := telgo.NewServer(":7023", "userdata> ", cmdlist, "anonymous")
	if err := s.Run(); err != nil {
		fmt.Printf("telnet server returned: %s", err)
	}
}
