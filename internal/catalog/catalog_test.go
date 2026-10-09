package catalog

import (
	"testing"

	"github.com/mark22013333/sshm/internal/store"
)

func names(ss []Section) (out [][]string) {
	for _, s := range ss {
		row := []string{s.Name}
		for _, h := range s.Hosts {
			row = append(row, h.Name)
		}
		out = append(out, row)
	}
	return out
}

func TestBuild(t *testing.T) {
	f := &store.File{
		Groups: []store.Group{{Name: "B", Color: "blue"}, {Name: "Empty"}, {Name: "A"}},
		Hosts: []store.Host{
			{Name: "a1", Group: "A", Host: "10.0.0.1", User: "root"},
			{Name: "loose", Host: "192.168.1.1", User: "pi"},
			{Name: "b1", Group: "B", Host: "db.example", User: "HelpDesk"},
			{Name: "x1", Group: "X", Host: "x", User: "u"},
		},
	}
	cases := []struct {
		query string
		want  [][]string
	}{
		{"", [][]string{{"B", "b1"}, {"A", "a1"}, {"X", "x1"}, {"", "loose"}}},
		{"HELPDESK", [][]string{{"B", "b1"}}},
		{"192.168", [][]string{{"", "loose"}}},
		{"a", [][]string{{"B", "b1"}, {"A", "a1"}}},
		{"x", [][]string{{"B", "b1"}, {"X", "x1"}}},
		{"nomatch", nil},
	}
	for _, c := range cases {
		got := names(Build(f, c.query))
		if len(got) != len(c.want) {
			t.Errorf("Build(%q) = %v, want %v", c.query, got, c.want)
			continue
		}
		for i := range got {
			if len(got[i]) != len(c.want[i]) {
				t.Errorf("Build(%q) = %v, want %v", c.query, got, c.want)
				break
			}
			for j := range got[i] {
				if got[i][j] != c.want[i][j] {
					t.Errorf("Build(%q) = %v, want %v", c.query, got, c.want)
				}
			}
		}
	}
	if s := Build(f, ""); s[0].Color != "blue" {
		t.Errorf("section color = %q", s[0].Color)
	}
}
