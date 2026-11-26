package service

import "testing"

func TestMaskParams(t *testing.T) {
	cases := map[string]string{
		`{"email":"a@example.com","password":"hunter2hunter2"}`:            `{"email":"a@example.com","password":"***"}`,
		`{"token":"abc","email":"a@b.c","name":"x","currentPassword":"1"}`: `{"currentPassword":"***","email":"a@b.c","name":"x","token":"***"}`,
		`{"oauth":{"client_secret":"s","api_key":"k","keyword":"kept"}}`:   `{"oauth":{"api_key":"***","client_secret":"***","keyword":"kept"}}`,
		`{"site.registration_open":true}`:                                  `{"site.registration_open":true}`,
		`[{"newPassword":"x"}]`:                                            `[{"newPassword":"***"}]`,
		`not json`:                                                         `{"_unparsed_bytes":8}`,
		``:                                                                 ``,
	}
	for in, want := range cases {
		if got := MaskParams([]byte(in)); got != want {
			t.Errorf("MaskParams(%s) = %s, want %s", in, got, want)
		}
	}
}
