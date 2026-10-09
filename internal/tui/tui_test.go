package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "alt+enter":
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+n":
		return tea.KeyMsg{Type: tea.KeyCtrlN}
	case "ctrl+e":
		return tea.KeyMsg{Type: tea.KeyCtrlE}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+x":
		return tea.KeyMsg{Type: tea.KeyCtrlX}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+g":
		return tea.KeyMsg{Type: tea.KeyCtrlG}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func send(t *testing.T, m *Model, keys ...string) tea.Cmd {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = m.Update(key(k))
	}
	return cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func setup(t *testing.T) (string, *store.File) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := store.HostsPath()
	if err != nil {
		t.Fatal(err)
	}
	f, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, f
}

// sshm add 的非互動路徑：直接驅動表單並確認寫出的檔案。
func TestAddFormSavesWithPermissions(t *testing.T) {
	path, f := setup(t)
	f.Groups = append(f.Groups, store.Group{Name: "範例客戶-ACME", Color: "blue"})
	m := New(Options{Path: path, File: f, ScriptPath: "/s.exp", StartAdd: true})
	if m.mode != modeForm {
		t.Fatal("sshm add should open the form")
	}
	send(t, m, "範例 PROD", "tab", "ctrl+g", "tab", "10.0.0.24", "tab")
	send(t, m, "tab", "helpdesk", "tab", "tab", "p w$'x")
	cmd := send(t, m, "ctrl+s")
	if !isQuit(cmd) {
		t.Fatalf("submit should quit in add mode; form err=%q", errMsg(m))
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("hosts.json perm = %o, want 600", st.Mode().Perm())
	}
	saved, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Hosts) != 1 {
		t.Fatalf("hosts = %+v", saved.Hosts)
	}
	h := saved.Hosts[0]
	if h.Name != "範例 PROD" || h.Group != "範例客戶-ACME" || h.Host != "10.0.0.24" || h.Port != 22 ||
		h.User != "helpdesk" || h.Auth != "password" || h.Password != "p w$'x" || !strings.HasPrefix(h.ID, "h_") {
		t.Fatalf("saved host = %+v", h)
	}
}

func errMsg(m *Model) string {
	if m.form == nil {
		return ""
	}
	return m.form.err
}

func TestFormRequiresFields(t *testing.T) {
	path, f := setup(t)
	m := New(Options{Path: path, File: f, StartAdd: true})
	if isQuit(send(t, m, "ctrl+s")) {
		t.Fatal("empty form should not submit")
	}
	if !strings.Contains(m.form.err, "名稱") || !strings.Contains(m.form.err, "host") || !strings.Contains(m.form.err, "user") {
		t.Fatalf("err = %q", m.form.err)
	}
	send(t, m, "n", "tab", "tab", "h", "tab", "x", "tab", "u")
	send(t, m, "ctrl+s")
	if !strings.Contains(m.form.err, "port") {
		t.Fatalf("bad port should be rejected, err=%q", m.form.err)
	}
}

func sampleFile() *store.File {
	f := store.NewFile()
	f.Groups = []store.Group{{Name: "G", Color: "red"}}
	f.Hosts = []store.Host{
		{ID: "h_a", Name: "alpha", Group: "G", Host: "10.0.0.1", Port: 22, User: "root", Auth: "password", Password: "pw 1"},
		{ID: "h_b", Name: "beta", Group: "G", Host: "10.0.0.2", Port: 2222, User: "ops", Auth: "none"},
		{ID: "h_c", Name: "gamma", Host: "db", Port: 22, User: "dba", Auth: "key", IdentityFile: "/k"},
	}
	return f
}

func TestEnterOpensOrcaTab(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	var got []string
	run := func(_ context.Context, argv []string) ([]byte, []byte, error) {
		got = argv
		return []byte(`{"ok":true,"result":{"terminal":{"handle":"t1"}}}`), nil, nil
	}
	m := New(Options{Path: path, File: sampleFile(), ScriptPath: "/cfg/sshm-login.exp", Query: "alpha",
		Orca: connect.OrcaEnv{BinDir: "/orca"}, InOrca: true, Runner: run})
	cmd := send(t, m, "enter")
	if m.mode != modeBusy || cmd == nil {
		t.Fatal("enter should start orca command")
	}
	_, next := m.Update(cmd())
	if !isQuit(next) {
		t.Fatal("successful orca create should quit")
	}
	want := []string{"/orca/orca", "terminal", "create", "--worktree", "active", "--title=🔴 alpha",
		"--command= /cfg/sshm-login.exp 22 root 10.0.0.1 'pw 1'", "--focus", "--json"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q", got)
	}
	if m.Result().ExecArgv != nil {
		t.Fatal("orca path should not exec in place")
	}
}

func TestOrcaFailureStaysInTUI(t *testing.T) {
	run := func(context.Context, []string) ([]byte, []byte, error) {
		return []byte(`{"ok":false,"error":{"message":"沒有 active worktree"}}`), nil, errors.New("exit status 1")
	}
	m := New(Options{Path: filepath.Join(t.TempDir(), "h.json"), File: sampleFile(), ScriptPath: "/s", Query: "beta",
		Orca: connect.OrcaEnv{BinDir: "/orca"}, InOrca: true, Runner: run})
	cmd := send(t, m, "enter")
	_, next := m.Update(cmd())
	if isQuit(next) || m.mode != modeList {
		t.Fatal("failure should stay in TUI")
	}
	if !m.statusErr || !strings.Contains(m.status, "沒有 active worktree") {
		t.Fatalf("status = %q", m.status)
	}
	if m.Result().ExecArgv != nil {
		t.Fatal("failure must not fall back to in-place connect")
	}
}

func TestAltEnterAndNonOrcaExecInPlace(t *testing.T) {
	called := false
	run := func(context.Context, []string) ([]byte, []byte, error) { called = true; return nil, nil, nil }
	m := New(Options{Path: filepath.Join(t.TempDir(), "h.json"), File: sampleFile(), Query: "beta",
		Orca: connect.OrcaEnv{BinDir: "/orca"}, InOrca: true, Runner: run})
	if !isQuit(send(t, m, "alt+enter")) {
		t.Fatal("alt+enter should quit")
	}
	want := "ssh -o StrictHostKeyChecking=accept-new -p 2222 -- ops@10.0.0.2"
	if got := strings.Join(m.Result().ExecArgv, " "); got != want || called {
		t.Fatalf("exec = %q called=%v", got, called)
	}

	m = New(Options{Path: filepath.Join(t.TempDir(), "h.json"), File: sampleFile(), Query: "gamma", Runner: run})
	if !isQuit(send(t, m, "enter")) || called {
		t.Fatal("enter outside Orca should exec in place")
	}
	if got := strings.Join(m.Result().ExecArgv, " "); !strings.Contains(got, "-i /k -- dba@db") {
		t.Fatalf("exec = %q", got)
	}
}

func TestSearchCollapseAndEsc(t *testing.T) {
	m := New(Options{Path: filepath.Join(t.TempDir(), "h.json"), File: sampleFile()})
	if len(m.rows) != 5 { // G + 2 台、未分組 + 1 台
		t.Fatalf("rows = %d", len(m.rows))
	}
	send(t, m, "left") // 游標在 alpha，折疊 G
	if len(m.rows) != 3 || m.rows[m.cursor].isHost {
		t.Fatalf("collapse failed: rows=%d cursor=%d", len(m.rows), m.cursor)
	}
	send(t, m, "right")
	if len(m.rows) != 5 {
		t.Fatalf("expand failed: rows=%d", len(m.rows))
	}
	send(t, m, "O", "P", "S")
	if h, ok := m.currentHost(); !ok || h.ID != "h_b" {
		t.Fatalf("search should select beta, got %+v", h)
	}
	if isQuit(send(t, m, "esc")) || m.search.Value() != "" {
		t.Fatal("first esc clears search")
	}
	if !isQuit(send(t, m, "esc")) {
		t.Fatal("second esc quits")
	}
}

func TestDuplicateEditDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	if err := store.Save(path, sampleFile()); err != nil {
		t.Fatal(err)
	}
	m := New(Options{Path: path, File: sampleFile(), Query: "alpha"})
	send(t, m, "ctrl+d")
	saved, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Hosts) != 4 || saved.Hosts[1].Name != "alpha (複本)" {
		t.Fatalf("duplicate: %+v", saved.Hosts)
	}

	send(t, m, "ctrl+e")
	if m.mode != modeForm || !m.form.editing {
		t.Fatal("ctrl+e should open edit form")
	}
	send(t, m, "!", "ctrl+s")
	saved, _ = store.Open(path)
	if saved.Hosts[1].Name != "alpha (複本)!" || saved.Hosts[1].Password != "pw 1" {
		t.Fatalf("edit: %+v", saved.Hosts[1])
	}

	send(t, m, "ctrl+x", "n")
	if saved, _ = store.Open(path); len(saved.Hosts) != 4 {
		t.Fatal("non-y should cancel delete")
	}
	send(t, m, "ctrl+x", "y")
	if saved, _ = store.Open(path); len(saved.Hosts) != 3 {
		t.Fatalf("delete failed: %d", len(saved.Hosts))
	}
}

func TestTunnelPagePlaceholder(t *testing.T) {
	m := New(Options{Path: filepath.Join(t.TempDir(), "h.json"), File: sampleFile()})
	send(t, m, "tab")
	if !strings.Contains(m.View(), "下一階段實作") {
		t.Fatalf("view = %q", m.View())
	}
	send(t, m, "tab")
	if m.page != pageHosts {
		t.Fatal("tab should switch back")
	}
}
