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
// commands used by variaus telnet clients to configure the connection.
// For now every negotiable telnet option will be discarded but the telnet
// command IP (interrupt process) is understood and can be used to terminate
// long running user commands.
// If the environment contains the variable TELGO_DEBUG logging will be enabled.
// By default telgo doesn't log anything.
package telgo

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"regexp"
	"strings"
	"unicode"
)

var (
	tl = log.New(ioutil.Discard, "[telgo]\t", log.LstdFlags)
)

func init() {
	if _, exists := os.LookupEnv("TELGO_DEBUG"); exists {
		tl = log.New(os.Stderr, "[telgo]\t", log.LstdFlags)
	}
}

const (
	EOT  = byte(4)
	IAC  = byte(255)
	DONT = byte(254)
	DO   = byte(253)
	WONT = byte(252)
	WILL = byte(251)
	SB   = byte(250)
	GA   = byte(249)
	EL   = byte(248)
	EC   = byte(247)
	AYT  = byte(246)
	AO   = byte(245)
	IP   = byte(244)
	BREA = byte(243)
	DM   = byte(242)
	NOP  = byte(241)
	SE   = byte(240)
)

type telnetCmd struct {
	length      int
	name        string
	description string
}

var (
	telnetCmds = map[byte]telnetCmd{
		DONT: telnetCmd{3, "DONT", "don't use option"},
		DO:   telnetCmd{3, "DO", "do use option"},
		WONT: telnetCmd{3, "WONT", "won't use option"},
		WILL: telnetCmd{3, "WILL", "will use option"},
		SB:   telnetCmd{2, "SB", "Begin of subnegotiation parameters"},
		GA:   telnetCmd{2, "GA", "go ahead signal"},
		EL:   telnetCmd{2, "EL", "erase line"},
		EC:   telnetCmd{2, "EC", "erase character"},
		AYT:  telnetCmd{2, "AYT", "are you there"},
		AO:   telnetCmd{2, "AO", "abort output"},
		IP:   telnetCmd{2, "IP", "interrupt process"},
		BREA: telnetCmd{2, "BREA", "break"},
		DM:   telnetCmd{2, "DM", "data mark"},
		NOP:  telnetCmd{2, "NOP", "no operation"},
		SE:   telnetCmd{2, "SE", "End of subnegotiation parameters"},
	}
)

// This is the signature of telgo command functions. It receives a pointer to
// the telgo client struct and a slice of strings containing the arguments the
// user has supplied. The first argument is always the command name itself.
// If this function returns true the client connection will be terminated.
type TelgoCmd func(c *TelnetClient, args []string) bool
type TelgoCmdList map[string]TelgoCmd

// This struct is used to export the raw tcp connection to the client as well as
// the UserData which got supplied to NewTelnetServer.
// The Cancel channel will get ready for reading when the user hits Ctrl-C or
// the connection got terminated. This can be used for long running telgo commands
// to be aborted.
type TelnetClient struct {
	Conn     net.Conn
	UserData interface{}
	Cancel   chan bool
	scanner  *bufio.Scanner
	writer   *bufio.Writer
	prompt   string
	commands *TelgoCmdList
	iacout   chan []byte
	stdout   chan []byte
}

func newTelnetClient(conn net.Conn, prompt string, commands *TelgoCmdList, userdata interface{}) (c *TelnetClient) {
	tl.Println("new client from:", conn.RemoteAddr())
	c = &TelnetClient{}
	c.Conn = conn
	c.scanner = bufio.NewScanner(conn)
	c.writer = bufio.NewWriter(conn)
	c.prompt = prompt
	c.commands = commands
	c.UserData = userdata
	c.stdout = make(chan []byte)
	c.Cancel = make(chan bool, 1)
	// the telnet split function needs some closures to handle inline telnet commands
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
	c.stdout <- bytes.Replace([]byte(text), []byte{IAC}, []byte{IAC, IAC}, -1)
}

// This is a simple Printf like interface which sends responses to the client.
func (c *TelnetClient) Say(format string, a ...interface{}) {
	c.WriteString(fmt.Sprintf(format, a...))
}

// This is the same as Say but also adds a new-line at the end of the string.
func (c *TelnetClient) Sayln(format string, a ...interface{}) {
	c.WriteString(fmt.Sprintf(format, a...) + "\r\n")
}

var (
	escapeRe = regexp.MustCompile("\\\\.")
)

func replEscapeChars(m string) (r string) {
	switch m {
	case "\\a":
		return "\a"
	case "\\b":
		return "\b"
	case "\\t":
		return "\t"
	case "\\n":
		return "\n"
	case "\\v":
		return "\v"
	case "\\f":
		return "\f"
	case "\\r":
		return "\r"
	}
	return string(m[1])
}

func stripEscapeChars(s string) string {
	return escapeRe.ReplaceAllStringFunc(s, replEscapeChars)
}

func spacesAndQuotes(r rune) bool {
	return unicode.IsSpace(r) || r == rune('"')
}

func backslashAndQuotes(r rune) bool {
	return r == rune('\\') || r == rune('"')
}

func splitCmdArguments(cmdstr string) (cmds []string, err error) {
	sepFunc := spacesAndQuotes
	foundQuote := false
	lastesc := 0
	for {
		i := strings.IndexFunc(cmdstr[lastesc:], sepFunc)
		if i < 0 {
			if foundQuote {
				err = fmt.Errorf("closing \" is missing")
			}
			if len(cmdstr) > 0 {
				cmds = append(cmds, cmdstr)
			}
			return
		}
		i += lastesc
		switch cmdstr[i] {
		case '\t', ' ':
			if i > 0 {
				cmds = append(cmds, cmdstr[0:i])
			}
			lastesc = 0
			cmdstr = cmdstr[i+1:]
		case '"':
			if foundQuote {
				cmds = append(cmds, stripEscapeChars(cmdstr[0:i]))
				foundQuote = false
				sepFunc = spacesAndQuotes
				if len(cmdstr) == i+1 { // is this the end?
					return
				}
				if !unicode.IsSpace(rune(cmdstr[i+1])) {
					err = fmt.Errorf("there must be a space after a closing \"")
					return
				}
			} else {
				foundQuote = true
				sepFunc = backslashAndQuotes
			}
			lastesc = 0
			cmdstr = cmdstr[i+1:]
		case '\\':
			if len(cmdstr[i:]) < 2 {
				err = fmt.Errorf("sole \\ at the end and no closing \"")
				return
			}
			lastesc = i + 2
		}
	}
}

func (c *TelnetClient) handleCmd(cmdstr string, done chan<- bool) {
	quit := false
	defer func() { done <- quit }()

	cmdslice, err := splitCmdArguments(cmdstr)
	if err != nil {
		c.Sayln("can't parse command: %s", err)
		return
	}

	if len(cmdslice) == 0 || cmdslice[0] == "" {
		return
	}

	select {
	case <-c.Cancel: // consume potentially pending cancel request
	default:
	}
	for cmd, cmdfunc := range *c.commands {
		if cmdslice[0] == cmd {
			quit = cmdfunc(c, cmdslice)
			return
		}
	}
	c.Sayln("unknown command '%s'", cmdslice[0])
}

// parse the telnet command and send out out-of-band responses to them
func handleIac(iac []byte, iacout chan<- []byte) {
	switch iac[1] {
	case WILL, WONT:
		iac[1] = DONT // deny the client to use any proposed options
	case DO, DONT:
		iac[1] = WONT // refuse the usage of any requested options
	case IP:
		// pass this through to client.handle which will cancel the process
	case IAC:
		return // just an escaped IAC, this will be dealt with by dropIAC
	default:
		tl.Printf("ignoring unimplemented telnet command: %s (%s)", telnetCmds[iac[1]].name, telnetCmds[iac[1]].description)
		return
	}
	iacout <- iac
}

// remove the carriage return at the end of the line
func dropCR(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == '\r' {
		return data[0 : len(data)-1]
	}
	return data
}

// remove all telnet commands which are still on the read buffer and were
// handled already using out-of-band messages
func dropIAC(data []byte) []byte {
	token := []byte("")
	iiac := 0
	for {
		niiac := bytes.IndexByte(data[iiac:], IAC)
		if niiac >= 0 {
			token = append(token, data[iiac:iiac+niiac]...)
			iiac += niiac
			if (len(data) - iiac) < 2 { // check if the data at least contains a command code
				return token // something is fishy.. found an IAC but this is the last byte of the token...
			}
			l := 2 // if we don't know this command - assume it has a length of 2
			if cmd, found := telnetCmds[data[iiac+1]]; found {
				l = cmd.length
			}
			if (len(data) - iiac) < l { // check if the command is complete
				return token // something is fishy.. found an IAC but the command is too short...
			}
			if data[iiac+1] == IAC { // escaped IAC found
				token = append(token, IAC)
			}
			iiac += l
		} else {
			token = append(token, data[iiac:]...)
			break
		}
	}
	return token
}

// This compares two indexes as returned by bytes.IndexByte treating -1 as the
// highest possible index.
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
			return inl + 1, dropIAC(dropCR(data[0:inl])), nil // found a complete line and no EOT or IAC
		}
		if ieot >= 0 && compareIdx(ieot, iiac) < 0 {
			*lastiiac = 0
			return ieot + 1, data[ieot : ieot+1], nil // found an EOT (aka Ctrl-D was hit) and no IAC
		}
		if iiac >= 0 { // found an IAC
			if (len(data) - iiac) < 2 {
				return 0, nil, nil // data does not yet contain the telnet command code -> need more data
			}
			l := 2 // if we don't know this command - assume it has a length of 2
			if cmd, found := telnetCmds[data[iiac+1]]; found {
				l = cmd.length
			}
			if (len(data) - iiac) < l {
				return 0, nil, nil // data does not yet contain the complete telnet command -> need more data
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
			tl.Printf("client(%s): Ctrl-D received, closing", c.Conn.RemoteAddr())
			return
		}
		in <- string(b)
	}
	if err := c.scanner.Err(); err != nil {
		tl.Printf("client(%s): recv() error: %s", c.Conn.RemoteAddr(), err)
	} else {
		tl.Printf("client(%s): Connection closed by foreign host", c.Conn.RemoteAddr())
	}
}

func (c *TelnetClient) cancel() {
	select {
	case c.Cancel <- true:
	default: // process got canceled already
	}
}

func (c *TelnetClient) send(quit <-chan bool) {
	for {
		select {
		case <-quit:
			return
		case iac := <-c.iacout:
			if iac[1] == IP {
				c.cancel()
			} else {
				c.writer.Write(iac)
				c.writer.Flush()
			}
		case data := <-c.stdout:
			c.writer.Write(data)
			c.writer.Flush()
		}
	}
}

func (c *TelnetClient) handle() {
	defer c.Conn.Close()

	in := make(chan string)
	go c.recv(in)

	quitSend := make(chan bool)
	go c.send(quitSend)
	defer func() { quitSend <- true }()

	defer c.cancel() // make sure to cancel possible running job when closing connection

	done := make(chan bool)
	busy := false
	c.WriteString(c.prompt)
	for {
		select {
		case cmd, ok := <-in:
			if !ok { // Ctrl-D or recv error (connection closed...)
				return
			}
			if !busy { // ignore everything except Ctrl-D while executing a command
				if len(cmd) > 0 {
					go c.handleCmd(cmd, done)
					busy = true
				} else {
					c.WriteString(c.prompt)
				}
			}
		case exit := <-done:
			if exit {
				return
			}
			c.WriteString(c.prompt)
			busy = false
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
// to net.Listen(). The prompt will be sent to the client whenever the telgo server is ready for a new command.
// TelgoCmdList is a list of available commands and userdata will be made available to called telgo
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
	tl.Println("listening on", self.addr)

	server, err := net.Listen("tcp", self.addr)
	if err != nil {
		tl.Println("Listen() Error:", err)
		return err
	}

	for {
		conn, err := server.Accept()
		if err != nil {
			tl.Println("Accept() Error:", err)
			return err
		}

		c := newTelnetClient(conn, self.prompt, &self.commands, self.userdata)
		go c.handle()
	}
}
