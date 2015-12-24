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
	"strconv"
	"strings"
	"time"
)

func simple_echo(c *telgo.TelnetClient, args []string) bool {
	c.Sayln(strings.Join(args[1:], " "))
	return false
}

func simple_run(c *telgo.TelnetClient, args []string) bool {
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

func simple_quit(c *telgo.TelnetClient, args []string) bool {
	return true
}

func main() {
	cmdlist := make(telgo.TelgoCmdList)
	cmdlist["echo"] = simple_echo
	cmdlist["run"] = simple_run
	cmdlist["quit"] = simple_quit

	s := telgo.NewTelnetServer(":7023", "simple> ", cmdlist, nil)
	if err := s.Run(); err != nil {
		fmt.Printf("telnet server returned: %s", err)
	}
}
