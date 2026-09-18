package store

import "testing"

func TestLooksLikeHTML(t *testing.T) {
	html := []string{"<p>hi</p>", "<div class=\"a\">x</div>", "<br/>", "<!-- c -->", "x<BR>y"}
	text := []string{"", "hi", "reply to <zn@czl.net>", "see <https://czl.net>", "if a < b then c", "1 <2> 3"}
	for _, s := range html {
		if !looksLikeHTML(s) {
			t.Errorf("looksLikeHTML(%q) = false, want true", s)
		}
	}
	for _, s := range text {
		if looksLikeHTML(s) {
			t.Errorf("looksLikeHTML(%q) = true, want false", s)
		}
	}
}
