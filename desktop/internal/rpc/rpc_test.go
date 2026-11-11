package rpc

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type item struct {
	ID   string   `json:"id"`
	Tags []string `json:"tags"`
}

type svc struct{ last string }

func (s *svc) Echo(a string, n int) (string, error) { return strings.Repeat(a, n), nil }
func (s *svc) Fail() error                          { return errors.New("2001 boom") }
func (s *svc) Set(v string)                         { s.last = v }
func (s *svc) Struct(it *item) item                 { return *it }
func (s *svc) Optional(a string, it *item) bool     { return it == nil }
func (s *svc) Panics() (int, error)                 { panic("bad") }
func (s *svc) TooMany() (int, int, error)           { return 0, 0, nil }
func (s *svc) hidden()                              {}

func call(t *testing.T, d *Dispatcher, name, args string) (string, error) {
	t.Helper()
	out, err := d.Call(name, json.RawMessage(args))
	return string(out), err
}

func TestCall(t *testing.T) {
	s := &svc{}
	d := New(s)

	if out, err := call(t, d, "Echo", `["ab",3]`); err != nil || out != `"ababab"` {
		t.Fatalf("Echo = %s, %v", out, err)
	}
	if _, err := call(t, d, "Fail", `[]`); err == nil || err.Error() != "2001 boom" {
		t.Fatalf("Fail err = %v", err)
	}
	if out, err := call(t, d, "Set", `["x"]`); err != nil || out != "null" || s.last != "x" {
		t.Fatalf("Set = %s, %v, last=%q", out, err, s.last)
	}
	if out, err := call(t, d, "Struct", `[{"id":"1","tags":["a"]}]`); err != nil || out != `{"id":"1","tags":["a"]}` {
		t.Fatalf("Struct = %s, %v", out, err)
	}
	// 少传与 null 都按零值。
	if out, _ := call(t, d, "Optional", `["a"]`); out != "true" {
		t.Fatalf("Optional missing = %s", out)
	}
	if out, _ := call(t, d, "Optional", `["a",null]`); out != "true" {
		t.Fatalf("Optional null = %s", out)
	}
	if out, _ := call(t, d, "Echo", ``); out != `""` {
		t.Fatalf("Echo no args = %s", out)
	}
}

func TestCallErrors(t *testing.T) {
	d := New(&svc{})
	if _, err := call(t, d, "Nope", `[]`); !errors.Is(err, ErrUnknownMethod) {
		t.Fatalf("unknown err = %v", err)
	}
	if d.Has("hidden") || d.Has("TooMany") {
		t.Fatal("unexported or unsupported method exposed")
	}
	if _, err := call(t, d, "Echo", `["a",1,2]`); err == nil {
		t.Fatal("extra argument accepted")
	}
	if _, err := call(t, d, "Echo", `[1,1]`); err == nil {
		t.Fatal("wrong type accepted")
	}
	if _, err := call(t, d, "Echo", `{"a":1}`); err == nil {
		t.Fatal("non-array arguments accepted")
	}
	if _, err := call(t, d, "Panics", `[]`); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("panic err = %v", err)
	}
}
