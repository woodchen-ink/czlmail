package main

import (
	"reflect"
	"testing"
)

func TestHintHosts(t *testing.T) {
	got := hintHosts("mail.czl.net")
	want := []string{"mail.czl.net", "czl.net"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if got := hintHosts("localhost"); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}
