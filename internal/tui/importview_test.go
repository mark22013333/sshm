package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/itermimport"
	"github.com/mark22013333/sshm/internal/store"
)

func previewPlan() itermimport.Plan {
	existing := store.NewFile()
	existing.Hosts = []store.Host{{ID: "h_x", Name: "old", Host: "10.9.9.9", Port: 22, User: "tester"}}
	return itermimport.Build([]itermimport.Bookmark{
		{Name: "multi", Tags: []string{"T1", "T2", "T3"}, InitialText: "~/login.exp 22 u 10.0.0.1 'sec ret'"},
		{Name: "single", Tags: []string{"S"}, InitialText: "~/login.exp 22 u 10.0.0.2 pw2"},
		{Name: "dup", InitialText: "~/login.exp 22 tester 10.9.9.9 pw3"},
		{Name: "bad", InitialText: "~/login.exp x u 10.0.0.4 pw4"},
		{Name: "other", InitialText: "ssh a@b"},
	}, existing)
}

func pkey(t *testing.T, m *ImportPreview, keys ...string) tea.Cmd {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		msg := key(k)
		if k == " " {
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		}
		_, cmd = m.Update(msg)
	}
	return cmd
}

func TestImportPreviewDefaultsAndView(t *testing.T) {
	m := NewImportPreview(previewPlan())
	view := m.View()
	for _, want := range []string{"找到 4 台", "略過 1 個", "群組：‹ T1 › (1/3)", "重複", "無法匯入", "已勾選 2 台"} {
		if !strings.Contains(view, want) {
			t.Errorf("畫面缺 %q：\n%s", want, view)
		}
	}
	for _, secret := range []string{"sec ret", "pw2", "pw3", "pw4"} {
		if strings.Contains(view, secret) {
			t.Fatalf("預覽畫面不得顯示密碼 %q", secret)
		}
	}
}

func TestImportPreviewKeys(t *testing.T) {
	m := NewImportPreview(previewPlan())
	pkey(t, m, "right", "right") // multi：T1 → T3
	if g := m.Candidates()[0].Group(); g != "T3" {
		t.Fatalf("group = %q", g)
	}
	pkey(t, m, "left")
	if g := m.Candidates()[0].Group(); g != "T2" {
		t.Fatalf("group = %q", g)
	}
	pkey(t, m, "down", " ") // 取消勾選 single
	if m.Candidates()[1].Selected {
		t.Fatal("空白應切換勾選")
	}
	pkey(t, m, "down", " ") // 勾選重複的 dup
	if !m.Candidates()[2].Selected {
		t.Fatal("重複的列可手動勾選")
	}
	pkey(t, m, "down", " ") // 無法匯入的列不能勾
	if m.Candidates()[3].Selected {
		t.Fatal("無法匯入的列不可勾選")
	}
	pkey(t, m, "n")
	if m.selectedCount() != 0 {
		t.Fatal("n 應全不選")
	}
	pkey(t, m, "a")
	if m.selectedCount() != 2 || m.Candidates()[2].Selected {
		t.Fatal("a 應全選但不含重複")
	}
	if cmd := pkey(t, m, "enter"); !isQuit(cmd) || !m.Confirmed() {
		t.Fatal("Enter 應確認並結束")
	}
}

func TestImportPreviewCancel(t *testing.T) {
	m := NewImportPreview(previewPlan())
	if cmd := pkey(t, m, "esc"); !isQuit(cmd) || m.Confirmed() {
		t.Fatal("Esc 應取消")
	}
}

// 預覽確認後走 store 的鎖內更新寫入；新群組自動建立、不設顏色。
func TestImportPreviewApplyThroughStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	if err := store.Save(path, store.NewFile()); err != nil {
		t.Fatal(err)
	}
	m := NewImportPreview(previewPlan())
	pkey(t, m, "right", "enter")
	var res itermimport.Result
	if _, err := store.Update(path, func(f *store.File) error {
		var err error
		res, err = itermimport.Apply(f, m.Candidates())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 || res.Skipped != 2 {
		t.Fatalf("result = %+v", res)
	}
	f, _ := store.Open(path)
	if f.Hosts[0].Group != "T2" || f.Hosts[0].Password != "sec ret" || !f.HasGroup("S") || f.GroupColor("T2") != "" {
		t.Fatalf("hosts=%+v groups=%+v", f.Hosts, f.Groups)
	}
}

// 名稱等外部字串含終端控制序列時，畫面上必須被過濾。
func TestImportPreviewSanitizesControlChars(t *testing.T) {
	plan := itermimport.Build([]itermimport.Bookmark{
		{Name: "evil\x1b[31m", InitialText: "~/login.exp x u 10.0.0.4 pw"},
		{Name: "ok\x1b]0;title\x07", InitialText: "~/login.exp 22 u 10.0.0.5 pw"},
	}, store.NewFile())
	view := NewImportPreview(plan).View()
	if strings.Contains(view, "\x1b[31m") || strings.Contains(view, "\x1b]0;") || strings.Contains(view, "\x07") {
		t.Fatalf("畫面含未過濾的控制序列：%q", view)
	}
	if !strings.Contains(view, "evil?[31m") {
		t.Fatalf("view = %q", view)
	}
}
