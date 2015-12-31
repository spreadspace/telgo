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
	"strconv"
	"strings"
	"time"
)

func echo(c *telgo.TelnetClient, args []string) bool {
	c.Sayln(strings.Join(args[1:], " "))
	return false
}

func run(c *telgo.TelnetClient, args []string) bool {
	if len(args) != 2 {
		c.Sayln("usage: run <duration>")
		return false
	}
	var duration uint
	if d, err := strconv.ParseUint(args[1], 10, 32); err != nil {
		c.Sayln("'%s' is not a vaild duration: must be a positive integer", args[1])
		return false
	} else {
		duration = uint(d)
	}
	c.Sayln("this will run for %d seconds (type Ctrl-C to abort)", duration)
	c.Say("running ...   0.0%%\r")
	for i := uint(0); i < duration*10; i++ {
		select {
		case <-c.Cancel:
			c.Sayln("\r\naborted.")
			return false
		default:
		}
		time.Sleep(100 * time.Millisecond)
		c.Say("running ... %5.1f%%\r", (float64(i)/float64(duration*10))*100.0)
	}
	c.Sayln("running ... 100.0%% ... done.")
	return false
}

func quit(c *telgo.TelnetClient, args []string) bool {
	return true
}

func main() {
	cmdlist := make(telgo.TelgoCmdList)
	cmdlist["echo"] = echo
	cmdlist["run"] = run
	cmdlist["quit"] = quit

	s := telgo.NewTelnetServer(":7023", "simple> ", cmdlist, nil)
	if err := s.Run(); err != nil {
		fmt.Printf("telnet server returned: %s", err)
	}
}
