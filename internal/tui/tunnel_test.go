package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
	"github.com/mark22013333/sshm/internal/tunnelstate"
)

// fakeOrca 模擬 orca CLI：create 回傳 handle、list 回傳目前存在的 handle、close 移除 handle。
type fakeOrca struct {
	mu        sync.Mutex
	live      map[string]bool
	truncated bool
	calls     [][]string
	next      string
}

func (o *fakeOrca) run(_ context.Context, argv []string) ([]byte, []byte, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, argv)
	switch argv[2] {
	case "create":
		o.live[o.next] = true
		return []byte(`{"ok":true,"result":{"terminal":{"handle":"` + o.next + `"}}}`), nil, nil
	case "list":
		var ts []map[string]string
		for h := range o.live {
			ts = append(ts, map[string]string{"handle": h, "title": "zsh"})
		}
		data, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminals": ts, "truncated": o.truncated,
			"hostScope": map[string]any{"hostIds": []string{"local"}, "omittedHostIds": []string{}}}})
		return data, nil, nil
	case "switch":
		return []byte(`{"ok":true,"result":{}}`), nil, nil
	case "close":
		delete(o.live, strings.TrimPrefix(argv[3], "--terminal="))
		return []byte(`{"ok":true,"result":{}}`), nil, nil
	}
	return nil, nil, nil
}

func (o *fakeOrca) callsOf(sub string) [][]string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out [][]string
	for _, c := range o.calls {
		if c[2] == sub {
			out = append(out, c)
		}
	}
	return out
}

func tunnelFile() *store.File {
	f := sampleFile()
	f.Tunnels = []store.Tunnel{
		{ID: "t_db", Name: "db", HostID: "h_a", Rules: []store.Rule{
			{Type: "L", BindPort: 13306, TargetHost: "127.0.0.1", TargetPort: 3306},
			{Type: "D", BindPort: 1080},
		}},
		{ID: "t_orphan", Name: "orphan", HostID: "h_gone", Rules: []store.Rule{{Type: "D", BindPort: 1081}}},
	}
	return f
}

func setupTunnels(t *testing.T, inOrca bool) (*Model, *fakeOrca, string) {
	t.Helper()
	old := tunnelRefreshInterval
	tunnelRefreshInterval = time.Millisecond
	t.Cleanup(func() { tunnelRefreshInterval = old })
	dir := filepath.Join(t.TempDir(), "sshm")
	path := filepath.Join(dir, "hosts.json")
	if err := store.Save(path, tunnelFile()); err != nil {
		t.Fatal(err)
	}
	f, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fo := &fakeOrca{live: map[string]bool{}, next: "term_new"}
	statePath := filepath.Join(dir, "state.json")
	m := New(Options{Path: path, File: f, ScriptPath: "/cfg/sshm-login.exp", StatePath: statePath,
		Orca: connect.OrcaEnv{BinDir: "/orca"}, InOrca: inOrca, Runner: fo.run})
	return m, fo, statePath
}

// drain 執行 cmd 並把產生的訊息送回 Model；tick 不再展開，避免無窮迴圈。
func drain(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			drain(t, m, c)
		}
	case tunnelTickMsg, nil:
	default:
		_, next := m.Update(msg)
		drain(t, m, next)
	}
}

func TestTunnelListView(t *testing.T) {
	m, _, _ := setupTunnels(t, true)
	drain(t, m, send(t, m, "tab"))
	view := m.View()
	for _, want := range []string{"○ db", "alpha", "L 13306→127.0.0.1:3306、D 1080", "orphan", "機器已不存在"} {
		if !strings.Contains(view, want) {
			t.Errorf("畫面缺 %q：\n%s", want, view)
		}
	}
	if strings.Contains(view, "需要在 Orca") {
		t.Error("在 Orca 中不應顯示唯讀提示")
	}
}

func TestTunnelStartAndStop(t *testing.T) {
	m, fo, statePath := setupTunnels(t, true)
	drain(t, m, send(t, m, "tab"))
	drain(t, m, send(t, m, "enter"))
	creates := fo.callsOf("create")
	if len(creates) != 1 {
		t.Fatalf("create calls = %d", len(creates))
	}
	argv := strings.Join(creates[0], "\x00")
	for _, want := range []string{"--title=⇄ db", "--command= /cfg/sshm-login.exp 22 root 10.0.0.1 'pw 1' -N", "-L\x00"} {
		if !strings.Contains(strings.ReplaceAll(argv, "\x00", " "), strings.ReplaceAll(want, "\x00", " ")) {
			t.Errorf("create argv 缺 %q：%q", want, creates[0])
		}
	}
	if strings.Contains(argv, "--focus") {
		t.Error("tunnel 分頁不應搶焦點")
	}
	st, err := tunnelstate.Load(statePath)
	if err != nil || st.Tunnels["t_db"].Handle != "term_new" {
		t.Fatalf("state = %+v err=%v", st, err)
	}
	if info, _ := os.Stat(statePath); info.Mode().Perm() != 0o600 {
		t.Fatalf("state.json perm = %o", info.Mode().Perm())
	}
	if !strings.Contains(m.View(), "● db") {
		t.Fatalf("啟動後應為執行中：\n%s", m.View())
	}
	drain(t, m, send(t, m, "enter"))
	closes := fo.callsOf("close")
	if len(closes) != 1 || closes[0][3] != "--terminal=term_new" {
		t.Fatalf("close = %q", closes)
	}
	if st, _ := tunnelstate.Load(statePath); len(st.Tunnels) != 0 {
		t.Fatalf("停止後應清掉紀錄：%+v", st)
	}
	if !strings.Contains(m.View(), "○ db") {
		t.Fatalf("停止後應為已停止：\n%s", m.View())
	}
}

func writeState(t *testing.T, path string, entries map[string]tunnelstate.Entry) {
	t.Helper()
	if _, err := tunnelstate.Update(path, func(s *tunnelstate.State) error {
		s.Tunnels = entries
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// handle 已不在 orca 清單中：視為已停止並清掉紀錄；清單被截斷時不清，顯示無法確認。
func TestTunnelRefreshClearsStaleState(t *testing.T) {
	m, fo, statePath := setupTunnels(t, true)
	past := time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	writeState(t, statePath, map[string]tunnelstate.Entry{"t_db": {Handle: "term_gone", StartedAt: past}})

	fo.truncated = true
	drain(t, m, send(t, m, "tab"))
	if st, _ := tunnelstate.Load(statePath); st.Tunnels["t_db"].Handle != "term_gone" {
		t.Fatal("清單不完整時不可清掉紀錄")
	}
	if !strings.Contains(m.View(), "◌ db") {
		t.Fatalf("應顯示無法確認：\n%s", m.View())
	}

	fo.truncated = false
	drain(t, m, m.refreshTunnelsCmd())
	if st, _ := tunnelstate.Load(statePath); len(st.Tunnels) != 0 {
		t.Fatalf("應清掉已不存在的 handle：%+v", st)
	}
	if !strings.Contains(m.View(), "○ db") {
		t.Fatalf("應為已停止：\n%s", m.View())
	}
}

// 狀態以 handle 判斷，不看 title；查詢期間才啟動的紀錄不會被清掉。
func TestTunnelRefreshUsesHandleAndKeepsJustStarted(t *testing.T) {
	m, fo, statePath := setupTunnels(t, true)
	future := time.Now().Add(time.Minute).Format(time.RFC3339Nano)
	writeState(t, statePath, map[string]tunnelstate.Entry{
		"t_db":     {Handle: "term_live", StartedAt: time.Now().Add(-time.Hour).Format(time.RFC3339Nano)},
		"t_orphan": {Handle: "term_fresh", StartedAt: future},
	})
	fo.live["term_live"] = true
	drain(t, m, send(t, m, "tab"))
	st, _ := tunnelstate.Load(statePath)
	if st.Tunnels["t_db"].Handle != "term_live" || st.Tunnels["t_orphan"].Handle != "term_fresh" {
		t.Fatalf("state = %+v", st)
	}
	if !strings.Contains(m.View(), "● db") {
		t.Fatalf("\n%s", m.View())
	}
}

func TestTunnelTickOnlyOnTunnelPage(t *testing.T) {
	m, _, _ := setupTunnels(t, true)
	send(t, m, "tab")
	if !m.tTicking {
		t.Fatal("進入 Tunnel 頁應開始刷新")
	}
	if _, cmd := m.Update(tunnelTickMsg{}); cmd == nil {
		t.Fatal("Tunnel 頁收到 tick 應排下一次刷新")
	}
	send(t, m, "tab")
	if _, cmd := m.Update(tunnelTickMsg{}); cmd != nil || m.tTicking {
		t.Fatal("離開 Tunnel 頁後應停止刷新")
	}
}

func TestTunnelOutsideOrcaIsReadOnly(t *testing.T) {
	m, fo, _ := setupTunnels(t, false)
	drain(t, m, send(t, m, "tab"))
	drain(t, m, send(t, m, "enter"))
	if !strings.Contains(m.View(), "Tunnel 需要在 Orca 中執行") || len(fo.calls) != 0 {
		t.Fatalf("calls=%v\n%s", fo.calls, m.View())
	}
	send(t, m, "ctrl+n")
	if m.mode != modeTunnelForm {
		t.Fatal("不在 Orca 中仍可新增設定")
	}
}

func TestTunnelStartMissingHost(t *testing.T) {
	m, fo, _ := setupTunnels(t, true)
	drain(t, m, send(t, m, "tab"))
	send(t, m, "down", "enter")
	if !strings.Contains(m.status, "機器已不存在") || len(fo.callsOf("create")) != 0 {
		t.Fatalf("status = %q", m.status)
	}
}

func TestTunnelFormCreate(t *testing.T) {
	m, _, _ := setupTunnels(t, false)
	send(t, m, "tab", "ctrl+n")
	send(t, m, "pg", "tab", "b", "e", "t", "a", "tab") // 名稱、搜尋機器 beta
	send(t, m, "tab", "tab", "1", "5", "4", "3", "2", "tab", "1", "2", "7", ".", "0", ".", "0", ".", "1", "tab", "5", "4", "3", "2")
	send(t, m, "ctrl+a", "right", "right", "tab", ":", ":", "1", "tab", "1", "0", "8", "0")
	send(t, m, "ctrl+s")
	if m.mode != modeList {
		t.Fatalf("未儲存：%q", m.tForm.err)
	}
	f, _ := store.Open(m.opts.Path)
	got := f.Tunnels[len(f.Tunnels)-1]
	if got.Name != "pg" || got.HostID != "h_b" || len(got.Rules) != 2 || !strings.HasPrefix(got.ID, "t_") {
		t.Fatalf("tunnel = %+v", got)
	}
	if ruleSummary(got.Rules[0]) != "L 15432→127.0.0.1:5432" || ruleSummary(got.Rules[1]) != "D [::1]:1080" {
		t.Fatalf("rules = %q / %q", ruleSummary(got.Rules[0]), ruleSummary(got.Rules[1]))
	}
}

func TestTunnelFormValidation(t *testing.T) {
	m, _, _ := setupTunnels(t, false)
	send(t, m, "tab", "ctrl+n", "x", "tab", "a", "l", "p", "h", "a", "tab", "tab", "tab", "a", "b", "c", "ctrl+s")
	if m.mode != modeTunnelForm || !strings.Contains(m.tForm.err, "bindPort 必須是 1–65535") {
		t.Fatalf("err = %q", m.tForm.err)
	}
	send(t, m, "ctrl+d", "ctrl+s") // 刪掉唯一的規則
	if !strings.Contains(m.tForm.err, "沒有任何規則") {
		t.Fatalf("err = %q", m.tForm.err)
	}
}

func TestTunnelEditDeleteAndRunningGuard(t *testing.T) {
	m, fo, statePath := setupTunnels(t, true)
	drain(t, m, send(t, m, "tab"))
	send(t, m, "ctrl+e", "2", "ctrl+s")
	f, _ := store.Open(m.opts.Path)
	if f.Tunnels[0].Name != "db2" || len(f.Tunnels[0].Rules) != 2 {
		t.Fatalf("edit: %+v", f.Tunnels[0])
	}
	drain(t, m, send(t, m, "enter")) // 啟動
	send(t, m, "ctrl+x")
	if m.mode == modeConfirmTunnelDelete || !strings.Contains(m.status, "請先在 Orca 中按 Enter 停止") {
		t.Fatalf("執行中不可刪除：status=%q", m.status)
	}
	drain(t, m, send(t, m, "enter")) // 停止
	send(t, m, "ctrl+x", "n")
	if f, _ := store.Open(m.opts.Path); len(f.Tunnels) != 2 {
		t.Fatal("n 應取消刪除")
	}
	send(t, m, "ctrl+x", "y")
	if f, _ := store.Open(m.opts.Path); len(f.Tunnels) != 1 || f.Tunnels[0].ID != "t_orphan" {
		t.Fatalf("delete: %+v", f.Tunnels)
	}
	_ = fo
	_ = statePath
}

// 刪除機器時列出引用它的 tunnel，確認後 tunnel 設定保留。
func TestHostDeleteListsReferencingTunnels(t *testing.T) {
	m, _, _ := setupTunnels(t, false)
	m.search.SetValue("alpha")
	m.rebuild("")
	send(t, m, "ctrl+x")
	if !strings.Contains(m.View(), "以下 tunnel 引用這台機器") || !strings.Contains(m.View(), "db") {
		t.Fatalf("\n%s", m.View())
	}
	send(t, m, "y")
	f, _ := store.Open(m.opts.Path)
	if f.HostIndex("h_a") >= 0 || f.TunnelIndex("t_db") < 0 {
		t.Fatalf("hosts=%d tunnels=%+v", len(f.Hosts), f.Tunnels)
	}
}
