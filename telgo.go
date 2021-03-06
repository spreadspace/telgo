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
		tl.SetOutput(os.Stderr)
	}
}

const (
	bEOT  = byte(4)
	bIAC  = byte(255)
	bDONT = byte(254)
	bDO   = byte(253)
	bWONT = byte(252)
	bWILL = byte(251)
	bSB   = byte(250)
	bGA   = byte(249)
	bEL   = byte(248)
	bEC   = byte(247)
	bAYT  = byte(246)
	bAO   = byte(245)
	bIP   = byte(244)
	bBREA = byte(243)
	bDM   = byte(242)
	bNOP  = byte(241)
	bSE   = byte(240)
)

type telnetCmd struct {
	length      int
	name        string
	description string
}

var (
	telnetCmds = map[byte]telnetCmd{
		bDONT: {3, "DONT", "don't use option"},
		bDO:   {3, "DO", "do use option"},
		bWONT: {3, "WONT", "won't use option"},
		bWILL: {3, "WILL", "will use option"},
		bSB:   {2, "SB", "Begin of subnegotiation parameters"},
		bGA:   {2, "GA", "go ahead signal"},
		bEL:   {2, "EL", "erase line"},
		bEC:   {2, "EC", "erase character"},
		bAYT:  {2, "AYT", "are you there"},
		bAO:   {2, "AO", "abort output"},
		bIP:   {2, "IP", "interrupt process"},
		bBREA: {2, "BREA", "break"},
		bDM:   {2, "DM", "data mark"},
		bNOP:  {2, "NOP", "no operation"},
		bSE:   {2, "SE", "End of subnegotiation parameters"},
	}
)

// Cmd is the signature of telgo command functions. It receives a pointer to
// the telgo client struct and a slice of strings containing the arguments the
// user has supplied. The first argument is always the command name itself.
// If this function returns true the client connection will be terminated.
type Cmd func(c *Client, args []string) bool

// CmdList is a list of telgo commands using the command name as the key.
type CmdList map[string]Cmd

// The Greeter interface is used to distinguish between default and greet functions
// supplied to run()
type Greeter interface {
	Exec(c *Client, args []string) bool
}

// Client is used to export the raw tcp connection to the client as well as the
// UserData to telgo command functions. The Prompt variable my be used to override
// the server prompt. Set it to the empty string to get the default prompt.
// The Cancel channel will get ready for reading when the user hits Ctrl-C or
// the connection got terminated. This can be used to abort long running telgo
// commands.
type Client struct {
	Conn     net.Conn
	UserData interface{}
	Cancel   chan bool
	scanner  *bufio.Scanner
	writer   *bufio.Writer
	prompt   string
	Prompt   string
	greeter  Greeter
	commands *CmdList
	dfltCmd  Cmd
	iacout   chan []byte
	stdout   chan []byte
	quitSend chan bool
}

func newClient(conn net.Conn, prompt string, greeter Greeter, commands *CmdList, dflt Cmd, userdata interface{}) (c *Client) {
	tl.Println("new client from:", conn.RemoteAddr())
	c = &Client{}
	c.Conn = conn
	c.scanner = bufio.NewScanner(conn)
	c.writer = bufio.NewWriter(conn)
	c.prompt = prompt
	c.Prompt = ""
	c.greeter = greeter
	c.commands = commands
	c.dfltCmd = dflt
	c.UserData = userdata
	c.stdout = make(chan []byte)
	c.quitSend = make(chan bool)
	c.Cancel = make(chan bool, 1)
	// the telnet split function needs some closures to handle inline telnet commands
	c.iacout = make(chan []byte)
	lastiiac := 0
	c.scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		return scanLines(data, atEOF, c.iacout, &lastiiac)
	})
	return c
}

// WriteString writes a 'raw' string to the client. For most purposes the usage of
// Say and Sayln is recommended. WriteString will take care of escaping IAC bytes
// inside your string. This function returns false if the client connection has been
// closed and the client is about to go away.
func (c *Client) WriteString(text string) bool {
	select {
	case _, ok := <-c.quitSend:
		if !ok {
			return false
		}
	default:
	}
	c.stdout <- bytes.Replace([]byte(text), []byte{bIAC}, []byte{bIAC, bIAC}, -1)
	return true
}

// Say is a simple Printf-like interface which sends responses to the client. If it
// returns false the client connection is about to be closed and therefore no output
// has been sent.
func (c *Client) Say(format string, a ...interface{}) bool {
	return c.WriteString(fmt.Sprintf(format, a...))
}

// Sayln is the same as Say but also adds a new-line at the end of the string.
func (c *Client) Sayln(format string, a ...interface{}) bool {
	return c.WriteString(fmt.Sprintf(format, a...) + "\r\n")
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

func (c *Client) handleCmd(cmdstr string, done chan<- bool) {
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
	if c.dfltCmd != nil {
		quit = c.dfltCmd(c, cmdslice)
		return
	}
	c.Sayln("unknown command '%s'", cmdslice[0])
}

func (c *Client) runGreeter(done chan<- bool) {
	done <- c.greeter.Exec(c, []string{"greeter"})
}

// parse the telnet command and send out out-of-band responses to them
func handleIac(iac []byte, iacout chan<- []byte) {
	switch iac[1] {
	case bWILL, bWONT:
		iac[1] = bDONT // deny the client to use any proposed options
	case bDO, bDONT:
		iac[1] = bWONT // refuse the usage of any requested options
	case bIP:
		// pass this through to client.handle which will cancel the process
	case bIAC:
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
		niiac := bytes.IndexByte(data[iiac:], bIAC)
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
			if data[iiac+1] == bIAC { // escaped IAC found
				token = append(token, bIAC)
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

	inl := bytes.IndexByte(data, '\n')  // index of first newline character
	ieot := bytes.IndexByte(data, bEOT) // index of first End of Transmission

	iiac := *lastiiac
	for {
		niiac := bytes.IndexByte(data[iiac:], bIAC) // index of first/next telnet IAC
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

func (c *Client) recv(in chan<- string) {
	defer close(in)

	for c.scanner.Scan() {
		b := c.scanner.Bytes()
		if len(b) > 0 && b[0] == bEOT {
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

func (c *Client) cancel() {
	select {
	case c.Cancel <- true:
	default: // process got canceled already
	}
}

func (c *Client) send() {
	for {
		select {
		case _, ok := <-c.quitSend:
			if !ok {
				return
			}
		case iac := <-c.iacout:
			if iac[1] == bIP {
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

func (c *Client) writePrompt() {
	if c.Prompt == "" {
		c.WriteString(c.prompt)
	} else {
		c.WriteString(c.Prompt)
	}
}

func (c *Client) handle() {
	defer c.Conn.Close()

	in := make(chan string)
	go c.recv(in)

	go c.send()
	defer close(c.quitSend)

	defer c.cancel() // make sure to cancel possible running job when closing connection

	done := make(chan bool)
	busy := false
	if c.greeter != nil {
		go c.runGreeter(done)
		busy = true
	} else {
		c.writePrompt()
	}
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
					c.writePrompt()
				}
			}
		case exit := <-done:
			if exit {
				return
			}
			c.writePrompt()
			busy = false
		}
	}
}

// Server contains all values needed to run the server. Use NewServer to create
// and Run to actually run the server.
type Server struct {
	ln       net.Listener
	prompt   string
	commands CmdList
	userdata interface{}
}

// NewServer creates a new telnet server struct. addr is the address to bind/listen to on and will be
// passed through to net.Listen(). The prompt will be sent to the client whenever the telgo server is ready
// for a new command.
// commands is a list of telgo commands to be used and userdata will be made available to called telgo commands
// through the client struct.
func NewServer(addr, prompt string, commands CmdList, userdata interface{}) (s *Server, err error) {
	s = &Server{}
	s.prompt = prompt
	s.commands = commands
	s.userdata = userdata
	s.ln, err = net.Listen("tcp", addr)
	return
}

// NewServerFromListener does the same as NewServer takes a net listener except for an address.
func NewServerFromListener(ln net.Listener, prompt string, commands CmdList, userdata interface{}) (s *Server, err error) {
	s = &Server{}
	s.prompt = prompt
	s.commands = commands
	s.userdata = userdata
	s.ln = ln
	return
}

// Run opens the server socket and runs the telnet server which spawns go routines for every
// connecting client. This function takes 2 optional parameters.
// Parameter who implement the Greeter interface will be passed to clients as greet function.
// These functions will be called before the first command prompt is shown. If the greeter function
// returns true the connection will be closed after it's completion. In this case the user won't be able
// to send any commands but might abort the running greet command using CTRl-C.
// If the parameter is a normal command function it will be used as a default command which will be called
// if the user entered an unknown command.
func (s *Server) Run(params ...interface{}) error {
	tl.Println("listening on", s.ln.Addr().String())

	var greeter Greeter
	var dflt Cmd
	for i, param := range params {
		switch param.(type) {
		case Greeter:
			if greeter != nil {
				panic(fmt.Sprintf("telgo.run() invalid parameter(%d): greeter may be supplied only once", i))
			}
			greeter = param.(Greeter)
		case func(c *Client, args []string) bool:
			if dflt != nil {
				panic(fmt.Sprintf("telgo.run() invalid parameter(%d): default command may be supplied only once", i))
			}
			dflt = param.(func(c *Client, args []string) bool)
		case Cmd:
			if dflt != nil {
				panic(fmt.Sprintf("telgo.run() invalid parameter(%d): default command may be supplied only once", i))
			}
			dflt = param.(Cmd)
		default:
			panic(fmt.Sprintf("telgo.run() invalid parameter(%d): type %T is not supported", i, param))
		}
	}

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			tl.Println("Accept() Error:", err)
			return err
		}

		c := newClient(conn, s.prompt, greeter, &s.commands, dflt, s.userdata)
		go c.handle()
	}
}
