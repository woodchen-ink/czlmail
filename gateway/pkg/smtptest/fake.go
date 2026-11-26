package smtptest

import (
	"bufio"
	"bytes"
	"net"
	"strings"
	"sync"
	"testing"
)

// Fake is a scripted SMTP server on loopback for error paths Mailpit cannot
// produce (refused recipients, 552, 421, 535). Plaintext, AUTH PLAIN and LOGIN.
//
// Reply decides the answer to each command ("MAIL", "RCPT", "DATA" for the
// end of the message data, "AUTH", "EHLO", "CONNECT" for the greeting): return a
// full reply line such as "550 5.1.1 unknown user", or "" for the normal answer.
type Fake struct {
	Addr  string
	Reply func(verb, arg string) string

	mu       sync.Mutex
	messages []FakeMessage
	conns    int
	ln       net.Listener
}

// FakeMessage is a message the fake accepted.
type FakeMessage struct {
	From string
	To   []string
	Data []byte
}

// NewFake starts a fake server; it stops when the test ends.
func NewFake(t testing.TB) *Fake {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &Fake{Addr: ln.Addr().String(), ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

// Messages returns the accepted messages.
func (f *Fake) Messages() []FakeMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeMessage(nil), f.messages...)
}

// Connections returns how many connections were made.
func (f *Fake) Connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns
}

func (f *Fake) reply(verb, arg, def string) string {
	if f.Reply != nil {
		if r := f.Reply(verb, arg); r != "" {
			return r
		}
	}
	return def
}

func (f *Fake) serve(c net.Conn) {
	defer c.Close()
	f.mu.Lock()
	f.conns++
	f.mu.Unlock()
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	send := func(line string) bool {
		w.WriteString(line + "\r\n")
		return w.Flush() == nil
	}
	greet := f.reply("CONNECT", "", "220 fake.test ESMTP")
	if !send(greet) || !strings.HasPrefix(greet, "2") {
		return
	}
	var cur *FakeMessage
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb, arg, _ := strings.Cut(line, " ")
		verb = strings.ToUpper(verb)
		switch verb {
		case "EHLO", "HELO":
			if rep := f.reply("EHLO", arg, ""); rep != "" {
				send(rep)
				continue
			}
			send("250-fake.test")
			send("250-8BITMIME")
			send("250 AUTH PLAIN LOGIN")
		case "AUTH":
			if strings.HasPrefix(strings.ToUpper(arg), "LOGIN") {
				send("334 VXNlcm5hbWU6")
				r.ReadString('\n')
				send("334 UGFzc3dvcmQ6")
				r.ReadString('\n')
			}
			send(f.reply("AUTH", arg, "235 2.7.0 Authentication successful"))
		case "MAIL":
			rep := f.reply("MAIL", arg, "250 2.1.0 OK")
			if strings.HasPrefix(rep, "2") {
				cur = &FakeMessage{From: addrOf(arg)}
			}
			send(rep)
		case "RCPT":
			rep := f.reply("RCPT", addrOf(arg), "250 2.1.5 OK")
			if strings.HasPrefix(rep, "2") && cur != nil {
				cur.To = append(cur.To, addrOf(arg))
			}
			send(rep)
		case "DATA":
			send("354 go ahead")
			var buf bytes.Buffer
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
				buf.WriteString(strings.TrimPrefix(l, "."))
			}
			rep := f.reply("DATA", "", "250 2.0.0 queued")
			if strings.HasPrefix(rep, "2") && cur != nil {
				cur.Data = buf.Bytes()
				f.mu.Lock()
				f.messages = append(f.messages, *cur)
				f.mu.Unlock()
			}
			cur = nil
			send(rep)
		case "RSET":
			cur = nil
			send("250 2.0.0 OK")
		case "NOOP":
			send("250 2.0.0 OK")
		case "QUIT":
			send("221 2.0.0 bye")
			return
		default:
			send("502 5.5.2 unknown command")
		}
	}
}

// addrOf extracts the address of "FROM:<a@b> SIZE=1" / "TO:<a@b>".
func addrOf(arg string) string {
	if i := strings.IndexByte(arg, '<'); i >= 0 {
		if j := strings.IndexByte(arg[i:], '>'); j >= 0 {
			return arg[i+1 : i+j]
		}
	}
	_, v, _ := strings.Cut(arg, ":")
	return strings.TrimSpace(v)
}
