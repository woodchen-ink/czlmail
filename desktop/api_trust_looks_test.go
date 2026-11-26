package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeAccountLooks(t *testing.T) {
	got := normalizeAccountLooks(map[string]AccountLook{
		"c":         {Name: "  Work  ", Color: "blue"},
		"s2~c":      {Color: "chartreuse"},
		"s2~e":      {Name: strings.Repeat("é", 70)},
		"signin:s2": {Name: "Gmail", Color: "orange"},
		"":          {Name: "nobody"},
	})
	want := map[string]AccountLook{
		"c":         {Name: "Work", Color: "blue"},
		"s2~e":      {Name: strings.Repeat("é", 64)},
		"signin:s2": {Name: "Gmail", Color: "orange"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalizeAccountLooks = %#v, want %#v", got, want)
	}
}
