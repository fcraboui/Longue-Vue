package store

import "testing"

func TestNamePattern(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain term wraps as substring", "du", "%du%"},
		{"uppercase folds to lower", "Du", "%du%"},
		{"trailing star anchors prefix", "du*", "du%"},
		{"leading star anchors suffix", "*du", "%du"},
		{"middle star anchors both ends", "prod-*-db", "prod-%-db"},
		{"underscore is literal", "my_app", `%my\_app%`},
		{"percent is literal", "50%", `%50\%%`},
		{"backslash is literal", `a\b`, `%a\\b%`},
		{"star with metachars", "my_app*", `my\_app%`},
		{"lone star matches everything", "*", "%"},
	}
	for _, tc := range cases {
		if got := namePattern(tc.in); got != tc.want {
			t.Errorf("%s: namePattern(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
