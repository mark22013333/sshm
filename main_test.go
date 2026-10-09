package main

import "testing"

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args       []string
		name, want string
	}{
		{nil, "tui", ""},
		{[]string{"tao", "prod"}, "tui", "tao prod"},
		{[]string{"add"}, "add", ""},
		{[]string{"--", "add"}, "tui", "add"},
		{[]string{"--", "import", "x"}, "tui", "import x"},
		{[]string{"zsh-widget"}, "zsh-widget", ""},
		{[]string{"export", "f.json"}, "export", ""},
		{[]string{"--help"}, "help", ""},
	}
	for _, c := range cases {
		got := parseArgs(c.args)
		if got.name != c.name || got.query != c.want {
			t.Errorf("parseArgs(%q) = %+v, want %s/%q", c.args, got, c.name, c.want)
		}
	}
}
