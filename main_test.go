package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark22013333/sshm/internal/itermimport"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args       []string
		name, want string
	}{
		{nil, "tui", ""},
		{[]string{"acme", "prod"}, "tui", "acme prod"},
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

func TestExportArgs(t *testing.T) {
	cases := []struct {
		args      []string
		file      string
		pw, force bool
		wantErr   bool
	}{
		{[]string{"out.json"}, "out.json", false, false, false},
		{[]string{"--with-passwords", "out.json"}, "out.json", true, false, false},
		{[]string{"out.json", "--force", "--with-passwords"}, "out.json", true, true, false},
		{[]string{"--", "--weird.json"}, "--weird.json", false, false, false},
		{[]string{}, "", false, false, true},
		{[]string{"a", "b"}, "", false, false, true},
		{[]string{"--bogus", "a"}, "", false, false, true},
	}
	for _, c := range cases {
		file, opt, err := exportArgs(c.args)
		if (err != nil) != c.wantErr {
			t.Errorf("%q: err = %v", c.args, err)
			continue
		}
		if err == nil && (file != c.file || opt.WithPasswords != c.pw || opt.Force != c.force) {
			t.Errorf("%q: file=%q opt=%+v", c.args, file, opt)
		}
	}
}

func TestPlistArg(t *testing.T) {
	if p, err := plistArg([]string{"--plist", "/x.plist"}); err != nil || p != "/x.plist" {
		t.Fatalf("%q %v", p, err)
	}
	if p, err := plistArg([]string{"--plist=/y.plist"}); err != nil || p != "/y.plist" {
		t.Fatalf("%q %v", p, err)
	}
	t.Setenv("HOME", "/home/test")
	if p, _ := plistArg(nil); p != "/home/test/Library/Preferences/com.googlecode.iterm2.plist" {
		t.Fatalf("default = %q", p)
	}
	if _, err := plistArg([]string{"extra"}); err == nil {
		t.Fatal("expected usage error")
	}
}

func TestParseArgsSubcommandArgs(t *testing.T) {
	c := parseArgs([]string{"export", "--with-passwords", "f.json"})
	if c.name != "export" || len(c.args) != 2 || c.args[1] != "f.json" {
		t.Fatalf("%+v", c)
	}
}

func TestApplyITermZeroSelectionDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	var out bytes.Buffer
	cands := []itermimport.Candidate{{Name: "a", Status: itermimport.StatusOK, Selected: false}}
	if err := applyITerm(path, cands, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "未選任何機器") {
		t.Fatalf("out = %q", out.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("不應建立或寫入 hosts.json：%v", err)
	}
}
