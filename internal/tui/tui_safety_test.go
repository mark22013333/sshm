package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
)

func TestCtrlTOpensOrcaTab(t *testing.T) {
	var got []string
	run := func(_ context.Context, argv []string) ([]byte, []byte, error) {
		got = argv
		return []byte(`{"ok":true,"result":{"terminal":{"handle":"t1"}}}`), nil, nil
	}
	m := New(Options{Path: filepath.Join(t.TempDir(), "h.json"), File: sampleFile(), Query: "beta",
		Orca: connect.OrcaEnv{BinDir: "/orca"}, InOrca: true, Runner: run})
	if !strings.Contains(m.View(), "Enter 原地 · Shift+Enter／Ctrl+T 新分頁") {
		t.Fatalf("說明列應顯示新鍵位：%q", m.View())
	}
	cmd := send(t, m, "ctrl+t")
	if m.mode != modeBusy || cmd == nil {
		t.Fatal("ctrl+t 應開 Orca 分頁")
	}
	if _, next := m.Update(cmd()); !isQuit(next) || len(got) == 0 || m.Result().ExecArgv != nil {
		t.Fatalf("ctrl+t 應呼叫 orca 且不原地執行：argv=%q", got)
	}
}

// 兩個 sshm 從同一份舊快照各新增一台，存檔後兩台都要在。
func TestTwoInstancesAddKeepsBoth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	if err := store.Save(path, sampleFile()); err != nil {
		t.Fatal(err)
	}
	open := func() *Model {
		f, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		return New(Options{Path: path, File: f})
	}
	m1, m2 := open(), open()
	send(t, m1, "ctrl+n", "one", "tab", "tab", "10.1.1.1", "tab", "tab", "u1", "ctrl+s")
	send(t, m2, "ctrl+n", "two", "tab", "tab", "10.2.2.2", "tab", "tab", "u2", "ctrl+s")
	if m1.mode != modeList || m2.mode != modeList {
		t.Fatalf("表單應已送出：m1=%q m2=%q", errMsg(m1), errMsg(m2))
	}
	f, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, h := range f.Hosts {
		names = append(names, h.Name)
	}
	if got := strings.Join(names, ","); got != "alpha,beta,gamma,one,two" {
		t.Fatalf("hosts = %s", got)
	}
	if len(m2.file.Hosts) != 5 {
		t.Fatalf("第二個實例存檔後應看到最新的 5 台，got %d", len(m2.file.Hosts))
	}
}

func TestFormRejectsDashPrefix(t *testing.T) {
	path, f := setup(t)
	m := New(Options{Path: path, File: f, StartAdd: true})
	send(t, m, "n", "tab", "tab", "h", "tab", "tab", "-oProxyCommand=evil")
	if isQuit(send(t, m, "ctrl+s")) || !strings.Contains(m.form.err, "user 不可以 - 開頭") {
		t.Fatalf("err = %q", m.form.err)
	}
}

func TestFormKeyRequiresIdentityAndClearsPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	if err := store.Save(path, sampleFile()); err != nil {
		t.Fatal(err)
	}
	f, _ := store.Open(path)
	m := New(Options{Path: path, File: f, Query: "alpha"})
	send(t, m, "ctrl+e", "tab", "tab", "tab", "tab", "tab") // 移到「認證方式」
	if m.form.focus != fieldAuth {
		t.Fatalf("focus = %d", m.form.focus)
	}
	send(t, m, "right", "ctrl+s") // password → key，金鑰檔空白
	if m.mode != modeForm || !strings.Contains(m.form.err, "金鑰檔") {
		t.Fatalf("key 無金鑰檔應被擋，err=%q", m.form.err)
	}
	send(t, m, "tab", "/k/id", "ctrl+s")
	if m.mode != modeList {
		t.Fatalf("應已儲存，err=%q", errMsg(m))
	}
	saved, _ := store.Open(path)
	h := saved.Hosts[0]
	if h.Auth != "key" || h.IdentityFile != "/k/id" || h.Password != "" {
		t.Fatalf("saved = %+v", h)
	}
}

func openAt(t *testing.T, path, query string) *Model {
	t.Helper()
	f, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{Path: path, File: f, Query: query})
}

// 兩個實例以同一份舊快照編輯同一台的不同欄位，兩邊的改動都要保留。
func TestStaleEditKeepsOtherInstanceFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	if err := store.Save(path, sampleFile()); err != nil {
		t.Fatal(err)
	}
	m1, m2 := openAt(t, path, "alpha"), openAt(t, path, "alpha")
	// 實例 1：改 port（名稱 → 群組 → host → port）
	send(t, m1, "ctrl+e", "tab", "tab", "tab", "9", "ctrl+s")
	// 實例 2：只改名稱
	send(t, m2, "ctrl+e", "X", "ctrl+s")
	if m1.mode != modeList || m2.mode != modeList {
		t.Fatalf("未儲存：m1=%q m2=%q", errMsg(m1), errMsg(m2))
	}
	f, _ := store.Open(path)
	h := f.Hosts[0]
	if h.Name != "alphaX" || h.Port != 229 || h.Password != "pw 1" || len(f.Hosts) != 3 {
		t.Fatalf("合併結果 = %+v", h)
	}
}

// 實例 1 刪除後，實例 2 以舊快照編輯存檔要得到錯誤，不能把機器加回來。
func TestStaleEditOfDeletedHostFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	if err := store.Save(path, sampleFile()); err != nil {
		t.Fatal(err)
	}
	m1, m2 := openAt(t, path, "alpha"), openAt(t, path, "alpha")
	send(t, m1, "ctrl+x", "y")
	send(t, m2, "ctrl+e", "X", "ctrl+s")
	if m2.mode != modeForm || !strings.Contains(m2.form.err, "已被其他 sshm 刪除") {
		t.Fatalf("應顯示已刪除錯誤，mode=%d err=%q", m2.mode, errMsg(m2))
	}
	f, _ := store.Open(path)
	for _, h := range f.Hosts {
		if strings.HasPrefix(h.Name, "alpha") {
			t.Fatalf("已刪除的機器被加回：%+v", f.Hosts)
		}
	}
}

// 實例 1 改了 host、實例 2 以舊表單改密碼：顯示衝突，要求重開表單，不把密碼配到新 host。
func TestStaleEditCredentialConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	if err := store.Save(path, sampleFile()); err != nil {
		t.Fatal(err)
	}
	m1, m2 := openAt(t, path, "alpha"), openAt(t, path, "alpha")
	send(t, m1, "ctrl+e", "tab", "tab", "9", "ctrl+s") // host 10.0.0.1 → 10.0.0.19
	send(t, m2, "ctrl+e", "tab", "tab", "tab", "tab", "tab", "tab", "X", "ctrl+s")
	if m2.mode != modeForm || !strings.Contains(m2.form.err, "這台機器剛被其他 sshm 修改，請重新開啟表單") {
		t.Fatalf("mode=%d err=%q", m2.mode, errMsg(m2))
	}
	f, _ := store.Open(path)
	if h := f.Hosts[0]; h.Host != "10.0.0.19" || h.Password != "pw 1" {
		t.Fatalf("host = %+v", h)
	}
}
