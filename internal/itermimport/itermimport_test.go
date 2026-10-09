package itermimport

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/mark22013333/sshm/internal/store"
)

const fixture = "../../testdata/iterm/iterm2-fixture.plist"

func execRunner(ctx context.Context, argv []string) ([]byte, []byte, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

func existing() *store.File {
	f := store.NewFile()
	f.Hosts = []store.Host{{ID: "h_x", Name: "smoke", Host: "10.9.9.9", Port: 22, User: "tester", Auth: "password"}}
	return f
}

func byName(p Plan) map[string]Candidate {
	m := map[string]Candidate{}
	for _, c := range p.Candidates {
		m[c.Name] = c
	}
	return m
}

// 以合成的二進位 plist 實跑 plutil，驗證每一種 profile 形狀。
func TestFixtureEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("系統沒有 plutil")
	}
	bms, err := ReadBookmarks(execRunner, fixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(bms) != 11 {
		t.Fatalf("bookmarks = %d", len(bms))
	}
	p := Build(bms, existing())
	if p.Skipped != 2 {
		t.Errorf("非 login.exp 應略過 2 個，got %d", p.Skipped)
	}
	m := byName(p)
	if len(m) != 9 {
		t.Fatalf("candidates = %d：%v", len(m), m)
	}

	check := func(name string, status Status, selected bool, reason string) Candidate {
		t.Helper()
		c, ok := m[name]
		if !ok {
			t.Fatalf("缺少候選 %q", name)
		}
		if c.Status != status || c.Selected != selected || !strings.Contains(c.Reason, reason) {
			t.Errorf("%s: status=%d selected=%v reason=%q", name, c.Status, c.Selected, c.Reason)
		}
		return c
	}
	c := check("範例-PROD", StatusOK, true, "")
	if c.Host.Host != "10.0.0.24" || c.Host.Port != 22 || c.Host.User != "helpdesk" || c.Host.Password != "Pa55word" || c.Group() != "範例客戶-ACME" {
		t.Errorf("單 tag: %+v group=%q", c.Host, c.Group())
	}
	c = check("台北-DB", StatusOK, true, "")
	if c.Host.Port != 2222 || c.Host.Password != `p w$d"x` || c.Group() != "台北市" || len(c.Tags) != 2 {
		t.Errorf("多 tag: %+v tags=%v", c.Host, c.Tags)
	}
	c = check("無群組機", StatusOK, true, "")
	if c.Group() != "" {
		t.Errorf("無 tag 應無群組：%q", c.Group())
	}
	c = check("特殊密碼", StatusOK, true, "")
	if c.Host.Password != `it's a [secret] $HOME` {
		t.Errorf("password = %q", c.Host.Password)
	}
	check("壞的-少參數", StatusInvalid, false, "4 個參數")
	check("壞的-port", StatusInvalid, false, "port")
	check("壞的-dash", StatusInvalid, false, "- 開頭")
	check("範例-PROD 副本", StatusDuplicate, false, "相同")
	check("既有機", StatusDuplicate, false, "相同")

	// 原因說明不得含密碼
	for _, c := range p.Candidates {
		for _, secret := range []string{"Pa55word", "p w$d", "secret", "simple", " pw"} {
			if strings.Contains(c.Reason, secret) {
				t.Errorf("%s 的原因含密碼片段：%q", c.Name, c.Reason)
			}
		}
	}
}

func TestReadBookmarksError(t *testing.T) {
	run := func(context.Context, []string) ([]byte, []byte, error) {
		return nil, []byte("Could not extract value"), errors.New("exit status 1")
	}
	if _, err := ReadBookmarks(run, "/x.plist"); err == nil || !strings.Contains(err.Error(), "Could not extract value") {
		t.Fatalf("err = %v", err)
	}
	var gotArgv []string
	run = func(_ context.Context, argv []string) ([]byte, []byte, error) {
		gotArgv = argv
		return []byte(`[]`), nil, nil
	}
	if _, err := ReadBookmarks(run, "/p.plist"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotArgv, " ") != "plutil -extract New Bookmarks json -o - /p.plist" {
		t.Fatalf("argv = %q", gotArgv)
	}
}

func TestParseBookmarksLenient(t *testing.T) {
	bms, err := ParseBookmarks([]byte(`[{"Name":"a","Tags":"not-array","Initial Text":5},{"Name":"b","Tags":[" x ",""]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if bms[0].Name != "a" || bms[0].Tags != nil || bms[0].InitialText != "" || len(bms[1].Tags) != 1 || bms[1].Tags[0] != "x" {
		t.Fatalf("%+v", bms)
	}
	if _, err := ParseBookmarks([]byte(`{}`)); err == nil {
		t.Fatal("非陣列應回錯")
	}
}

func TestApply(t *testing.T) {
	bms := []Bookmark{
		{Name: "a", Tags: []string{"T1", "T2"}, InitialText: "~/login.exp 22 u 10.0.0.1 pw"},
		{Name: "b", InitialText: "~/login.exp 22 u 10.0.0.2 pw"},
		{Name: "dup", InitialText: "~/login.exp 22 tester 10.9.9.9 pw"},
		{Name: "bad", InitialText: "~/login.exp 22 u"},
	}
	p := Build(bms, existing())
	p.Candidates[0].CycleGroup(1) // 改用第二個 tag
	p.Candidates[1].Selected = false
	f := existing()
	r, err := Apply(f, p.Candidates)
	if err != nil {
		t.Fatal(err)
	}
	if r.Added != 1 || r.Skipped != 3 {
		t.Fatalf("result = %+v", r)
	}
	added := f.Hosts[1]
	if added.Name != "a" || added.Group != "T2" || !strings.HasPrefix(added.ID, "h_") {
		t.Fatalf("added = %+v", added)
	}
	if !f.HasGroup("T2") || f.GroupColor("T2") != "" {
		t.Fatalf("新群組應自動建立且不設顏色：%+v", f.Groups)
	}
	// 寫入前另一個 sshm 已加入同一台：寫入時再判斷，略過
	p2 := Build([]Bookmark{{Name: "c", InitialText: "~/login.exp 22 u 10.0.0.3 pw"}}, store.NewFile())
	f2 := store.NewFile()
	if _, err := f2.UpsertHost(store.Host{Name: "c0", Host: "10.0.0.3", Port: 22, User: "u"}); err != nil {
		t.Fatal(err)
	}
	if r, _ := Apply(f2, p2.Candidates); r.Added != 0 || r.Skipped != 1 {
		t.Fatalf("result = %+v", r)
	}
}

func TestTagsAndDuplicateNotes(t *testing.T) {
	bms, err := ParseBookmarks([]byte(`[
	  {"Name":"a","Initial Text":"./login.exp 22 u h1 p","Tags":"single"},
	  {"Name":"b","Initial Text":"./login.exp 22 u h2 p","Tags":[1,"x"]},
	  {"Name":"c","Initial Text":"./login.exp 22 u h3 p","Tags":["ok","bad\u0007"]},
	  {"Name":"d","Initial Text":"./login.exp 22 u h3 p"}]`))
	if err != nil {
		t.Fatal(err)
	}
	p := Build(bms, store.NewFile())
	m := byName(p)
	if m["a"].Note != "Tags 不是清單，已忽略" || len(m["a"].Tags) != 0 {
		t.Errorf("a: %+v", m["a"])
	}
	if !strings.Contains(m["b"].Note, "不是文字") || len(m["b"].Tags) != 1 || m["b"].Tags[0] != "x" {
		t.Errorf("b: %+v", m["b"])
	}
	if !strings.Contains(m["c"].Note, "控制字元") || len(m["c"].Tags) != 1 {
		t.Errorf("c: %+v", m["c"])
	}
	if m["d"].Status != StatusDuplicate || !strings.Contains(m["d"].Reason, "與本批另一個 profile 重複") {
		t.Errorf("d: %+v", m["d"])
	}
	// 含控制字元的 tag 已被濾掉，切換群組後仍可寫入
	f := store.NewFile()
	c := m["c"]
	c.CycleGroup(1)
	if r, err := Apply(f, []Candidate{c}); err != nil || r.Added != 1 {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	if CountSelected(p.Candidates) != 3 {
		t.Errorf("selected = %d", CountSelected(p.Candidates))
	}
}

func TestBuildRejectsAtInHost(t *testing.T) {
	p := Build([]Bookmark{{Name: "x", InitialText: "~/login.exp 22 root victim@attacker pw"}}, store.NewFile())
	if c := p.Candidates[0]; c.Status != StatusInvalid || !strings.Contains(c.Reason, "host 不可包含 @") {
		t.Fatalf("%+v", c)
	}
}
