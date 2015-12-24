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

// Package telgo contains a simple telnet server which can be used as a
// control/debug interface for applications.
// The telgo telnet server does all the client handling and runs configurable
// commands as go routines. It also supports handling of basic inline telnet
// commands used by variaus telnet clients to configure the client connection.
// For now every negotiable telnet option will be discarded but the telnet
// command IP (interrupt process) is understood and can be used to terminate
// long running user commands.
package telgo

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
)

var (
	tl = log.New(os.Stderr, "[telnet]\t", log.LstdFlags)
)

const (
	EOT  = byte(4)
	IP   = byte(244)
	WILL = byte(251)
	WONT = byte(252)
	DO   = byte(253)
	DONT = byte(254)
	IAC  = byte(255)
)

// This is the signature of telgo command functions. It receives a pointer to
// the telgo client struct and a slice of strings containing the arguments the
// user has supplied. The first argument is always the command name itself.
// The cancel channel will get ready for reading when the user hits Ctrl-C or
// the connection got terminated.
// If this function returns true the client connection will be terminated.
type TelgoCmd func(c *TelnetClient, args []string, cancel <-chan bool) bool
type TelgoCmdList map[string]TelgoCmd

// This struct is used to export the raw tcp connection to the client as well as
// the UserData which got supplied to NewTelnetServer.
type TelnetClient struct {
	Conn     net.Conn
	UserData interface{}
	scanner  *bufio.Scanner
	writer   *bufio.Writer
	prompt   string
	commands *TelgoCmdList
	iacout   chan []byte
}

func newTelnetClient(conn net.Conn, prompt string, commands *TelgoCmdList, userdata interface{}) (c *TelnetClient) {
	tl.Println("telgo: new client from:", conn.RemoteAddr())
	c = &TelnetClient{}
	c.Conn = conn
	c.scanner = bufio.NewScanner(conn)
	c.writer = bufio.NewWriter(conn)
	c.prompt = prompt
	c.commands = commands
	c.UserData = userdata
	// the telnet split function needs some closures to handle OOB telnet commands
	c.iacout = make(chan []byte)
	lastiiac := 0
	c.scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		return scanLines(data, atEOF, c.iacout, &lastiiac)
	})
	return c
}

// This writes a 'raw' string to the client. For the most part the usage of Say
// and Sayln is recommended. WriteString will take care of escaping IAC bytes
// inside your string.
func (c *TelnetClient) WriteString(text string) {
	defer c.writer.Flush()

	data := []byte(text)
	for {
		idx := bytes.IndexByte(data, IAC)
		if idx >= 0 {
			c.writer.Write(data[:idx+1])
			c.writer.WriteByte(IAC)
			data = data[idx+1:]
		} else {
			c.writer.Write(data)
			return
		}
	}
}

// This is a simple Printf like interface which sends responses to the client.
func (c *TelnetClient) Say(format string, a ...interface{}) {
	c.WriteString(fmt.Sprintf(format, a...))
}

// This is the same as Say but also adds a new-line at the end of the string.
func (c *TelnetClient) Sayln(format string, a ...interface{}) {
	c.WriteString(fmt.Sprintf(format, a...) + "\r\n")
}

// TODO: fix split function to respect "" and ''
func (c *TelnetClient) handleCmd(cmdstr string, done chan<- bool, cancel <-chan bool) {
	quit := false
	defer func() { done <- quit }()

	cmdslice := strings.Fields(cmdstr)
	if len(cmdslice) == 0 || cmdslice[0] == "" {
		return
	}

	for cmd, cmdfunc := range *c.commands {
		if cmdslice[0] == cmd {
			quit = cmdfunc(c, cmdslice, cancel)
			return
		}
	}
	c.Sayln("unknown command '%s'", cmdslice[0])
}

func handleIac(iac []byte, iacout chan<- []byte) {
	switch iac[1] {
	case WILL, WONT: // Don't accept any proposed options
		iac[1] = DONT
	case DO, DONT:
		iac[1] = WONT
	case IP:
		// pass this through to client.handle which will cancel the process
	default:
		tl.Printf("ignoring unimplemented telnet command: %X", iac[1])
		return
	}
	iacout <- iac
}

func dropCR(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == '\r' {
		return data[0 : len(data)-1]
	}
	return data
}

func dropIAC(data []byte) []byte {
	var token []byte
	iiac := 0
	for {
		niiac := bytes.IndexByte(data[iiac:], IAC)
		if niiac >= 0 {
			token = append(token, data[iiac:iiac+niiac]...)
			iiac += niiac
			if (len(data) - iiac) < 2 {
				return token
			}
			switch data[iiac+1] {
			case DONT, DO, WONT, WILL:
				if (len(data) - iiac) < 3 {
					return token
				}
				iiac += 3
			case IAC:
				token = append(token, IAC)
				fallthrough
			default:
				iiac += 2
			}
		} else {
			token = append(token, data[iiac:]...)
			break
		}
	}
	return token
}

func compareIdx(a, b int) int {
	if a < 0 {
		a = int(^uint(0) >> 1)
	}
	if b < 0 {
		b = int(^uint(0) >> 1)
	}
	return a - b
}

func scanLines(data []byte, atEOF bool, iacout chan<- []byte, lastiiac *int) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	inl := bytes.IndexByte(data, '\n') // index of first newline character
	ieot := bytes.IndexByte(data, EOT) // index of first End of Transmission

	iiac := *lastiiac
	for {
		niiac := bytes.IndexByte(data[iiac:], IAC) // index of first/next telnet IAC
		if niiac >= 0 {
			iiac += niiac
		} else {
			iiac = niiac
		}

		if inl >= 0 && compareIdx(inl, ieot) < 0 && compareIdx(inl, iiac) < 0 {
			*lastiiac = 0
			return inl + 1, dropCR(dropIAC(data[0:inl])), nil // found a complete line
		}
		if ieot >= 0 && compareIdx(ieot, iiac) < 0 {
			*lastiiac = 0
			return ieot + 1, data[ieot : ieot+1], nil // found a EOT (aka Ctrl-D)
		}
		if iiac >= 0 {
			l := 2
			if (len(data) - iiac) < 2 {
				return 0, nil, nil // data does not yet contain the telnet command code -> need more data
			}
			switch data[iiac+1] {
			case DONT, DO, WONT, WILL:
				if (len(data) - iiac) < 3 {
					return 0, nil, nil // this is a 3-byte command and data does not yet contain the option code -> need more data
				}
				l = 3
			case IAC:
				iiac += 2
				continue
			}
			handleIac(data[iiac:iiac+l], iacout)
			iiac += l
			*lastiiac = iiac
		} else {
			break
		}
	}
	if atEOF {
		return len(data), dropCR(data), nil // allow last line to have no new line
	}
	return 0, nil, nil // we have found none of the escape codes -> need more data
}

func (c *TelnetClient) recv(in chan<- string) {
	defer close(in)

	for c.scanner.Scan() {
		b := c.scanner.Bytes()
		if len(b) > 0 && b[0] == EOT {
			tl.Printf("telgo(%s): Ctrl-D received, closing", c.Conn.RemoteAddr())
			return
		}
		in <- string(b)
	}
	if err := c.scanner.Err(); err != nil {
		tl.Printf("telgo(%s): recv() error: %s", c.Conn.RemoteAddr(), err)
	} else {
		tl.Printf("telgo(%s): Connection closed by foreign host", c.Conn.RemoteAddr())
	}
}

func (c *TelnetClient) handle() {
	defer c.Conn.Close()

	in := make(chan string)
	go c.recv(in)

	done := make(chan bool)
	cancel := make(chan bool, 1)
	defer func() { // make sure to cancel possible running job when closing connection
		select {
		case cancel <- true:
		default:
		}
	}()

	var cmd_backlog []string
	busy := false
	c.WriteString(c.prompt)
	for {
		select {
		case cmd, ok := <-in:
			if !ok {
				return
			}
			if len(cmd) > 0 {
				if !busy {
					go c.handleCmd(cmd, done, cancel)
					busy = true
				} else {
					cmd_backlog = append(cmd_backlog, cmd)
				}
			} else {
				c.WriteString(c.prompt)
			}
		case exit := <-done:
			if exit {
				return
			}
			c.WriteString(c.prompt)
			if len(cmd_backlog) > 0 {
				go c.handleCmd(cmd_backlog[0], done, cancel)
				cmd_backlog = cmd_backlog[1:]
			} else {
				busy = false
			}
		case iac := <-c.iacout:
			if iac[1] == IP {
				select {
				case cancel <- true:
				default: // process got canceled already
				}
			} else {
				c.writer.Write(iac)
				c.writer.Flush()
			}
		}
	}
}

type TelnetServer struct {
	addr     string
	prompt   string
	commands TelgoCmdList
	userdata interface{}
}

// This creates a new telnet server. addr is the address to bind/listen to on and will be passed through
// to net.Listen(). The prompt will be sent to the client whenever the telgo server is ready for new command
// TelgoCmdList contains a list of available commands and userdata will be made available to called telgo
// commands through the client struct.
func NewTelnetServer(addr, prompt string, commands TelgoCmdList, userdata interface{}) (s *TelnetServer) {
	s = &TelnetServer{}
	s.addr = addr
	s.prompt = prompt
	s.commands = commands
	s.userdata = userdata
	return s
}

// This runs the telnet server and spawns go routines for every connecting client.
func (self *TelnetServer) Run() error {
	tl.Println("telgo: listening on", self.addr)

	server, err := net.Listen("tcp", self.addr)
	if err != nil {
		tl.Println("telgo: Listen() Error:", err)
		return err
	}

	for {
		conn, err := server.Accept()
		if err != nil {
			tl.Println("telgo: Accept() Error:", err)
			return err
		}

		c := newTelnetClient(conn, self.prompt, &self.commands, self.userdata)
		go c.handle()
	}
}
