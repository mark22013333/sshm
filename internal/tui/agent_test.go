package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/agentaccess"
	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
)

type agentTUI struct {
	m          *Model
	path       string
	home       string
	remoteHome string
	remoteFail error
	remoteRuns int
}

// newAgentTUI 建立有一台可開通機器的 TUI；遠端以本機 sh 在臨時目錄模擬，HOME 也是臨時目錄。
func newAgentTUI(t *testing.T, hosts ...store.Host) *agentTUI {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("沒有 ssh-keygen")
	}
	path, f := setup(t)
	a := &agentTUI{path: path, home: t.TempDir(), remoteHome: t.TempDir()}
	t.Setenv("HOME", a.home)
	if len(hosts) == 0 {
		hosts = []store.Host{{ID: "h_1", Name: "範例客戶-PROD-VM", Group: "範例客戶", Host: "10.0.0.24", Port: 22, User: "helpdesk", Auth: store.AuthPassword, Password: "test-pass"}}
	}
	f.Hosts = hosts
	if err := store.Save(path, f); err != nil {
		t.Fatal(err)
	}
	mgr := &agentaccess.Manager{
		Paths:     agentaccess.PathsFor(a.home, "/nonexistent"),
		HostsPath: path,
		Keygen:    connect.ExecRunner,
		Remote: func(_ context.Context, h store.Host, user, script string) (string, error) {
			a.remoteRuns++
			if a.remoteFail != nil {
				return "Connection timed out", a.remoteFail
			}
			cmd := exec.Command("/bin/sh", "-c", agentaccess.RemoteCommand(script))
			cmd.Env = append(os.Environ(), "HOME="+a.remoteHome)
			out, err := cmd.CombinedOutput()
			return string(out), err
		},
		Verify: func(context.Context, string) error { return nil },
	}
	a.m = New(Options{Path: path, File: f, ScriptPath: "/s.exp", Agent: mgr})
	a.m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	return a
}

// run 執行 Update 回傳的非同步工作並把結果送回 Model（模擬 Bubble Tea 的事件迴圈）。
func (a *agentTUI) run(cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			return
		}
		_, cmd = a.m.Update(msg)
	}
}

func (a *agentTUI) saved(t *testing.T) store.Host {
	t.Helper()
	f, err := store.Open(a.path)
	if err != nil {
		t.Fatal(err)
	}
	return f.Hosts[0]
}

func TestAgentHotkeyShowsConfirmAndCancel(t *testing.T) {
	a := newAgentTUI(t)
	send(t, a.m, "ctrl+o")
	if a.m.mode != modeAgentConfirm {
		t.Fatalf("Ctrl+O 應開確認畫面，mode=%v status=%q", a.m.mode, a.m.status)
	}
	view := a.m.View()
	t.Logf("確認畫面：\n%s", view)
	for _, want := range []string{"開通", "範例客戶-PROD-VM", "helpdesk", "SHA256:", "sshm-prod-vm", "sshm_agents.conf", "按 y"} {
		if !strings.Contains(view, want) {
			t.Errorf("確認畫面缺 %q：\n%s", want, view)
		}
	}
	if strings.Contains(view, "test-pass") {
		t.Fatal("確認畫面不得顯示密碼")
	}
	send(t, a.m, "n")
	if a.m.mode != modeList || !strings.Contains(a.m.status, "已取消") || a.remoteRuns != 0 {
		t.Fatalf("取消後 mode=%v status=%q remote=%d", a.m.mode, a.m.status, a.remoteRuns)
	}
	if entries, _ := os.ReadDir(filepath.Join(a.home, ".ssh", "sshm")); len(entries) != 0 {
		t.Fatalf("取消後不應留下金鑰：%v", entries)
	}
}

func TestAgentEnableDisableViaTUI(t *testing.T) {
	a := newAgentTUI(t)
	send(t, a.m, "ctrl+o")
	a.run(send(t, a.m, "y"))
	if a.m.mode != modeList || a.m.statusErr || !strings.Contains(a.m.status, "ssh sshm-prod-vm") {
		t.Fatalf("開通後 mode=%v status=%q", a.m.mode, a.m.status)
	}
	if h := a.saved(t); h.Agent == nil || h.Agent.Alias != "sshm-prod-vm" {
		t.Fatalf("agent = %+v", h.Agent)
	}
	// 清單顯示標記
	t.Logf("開通後清單：\n%s", a.m.View())
	if view := a.m.View(); !strings.Contains(view, agentMark+" sshm-prod-vm") {
		t.Fatalf("清單缺開通標記：\n%s", view)
	}
	// 已開通的機器不能直接刪
	send(t, a.m, "ctrl+x")
	if a.m.mode == modeConfirmDelete || !strings.Contains(a.m.status, "Ctrl+O") {
		t.Fatalf("已開通的機器應先停用，mode=%v status=%q", a.m.mode, a.m.status)
	}
	// 再按 Ctrl+O 是停用
	send(t, a.m, "ctrl+o")
	if a.m.mode != modeAgentConfirm || !strings.Contains(a.m.View(), "停用") {
		t.Fatalf("應顯示停用確認：\n%s", a.m.View())
	}
	a.run(send(t, a.m, "y"))
	if a.m.mode != modeList || a.m.statusErr || !strings.Contains(a.m.status, "已停用") {
		t.Fatalf("停用後 mode=%v status=%q", a.m.mode, a.m.status)
	}
	if h := a.saved(t); h.Agent != nil {
		t.Fatal("停用後應清除開通狀態")
	}
	if strings.Contains(a.m.View(), agentMark) {
		t.Fatal("停用後不應再顯示標記")
	}
}

func TestAgentDisableRemoteFailureAsksLocalOnly(t *testing.T) {
	a := newAgentTUI(t)
	send(t, a.m, "ctrl+o")
	a.run(send(t, a.m, "y"))
	a.remoteFail = errors.New("逾時")
	send(t, a.m, "ctrl+o")
	a.run(send(t, a.m, "y"))
	if a.m.mode != modeAgentLocalOnly {
		t.Fatalf("遠端失敗應詢問是否只清本機，mode=%v status=%q", a.m.mode, a.m.status)
	}
	view := a.m.View()
	if !strings.Contains(view, "仍有行尾為 sshm-agent:sshm-prod-vm 的公鑰") || !strings.Contains(view, "只清除本機") {
		t.Fatalf("畫面 = %s", view)
	}
	send(t, a.m, "n")
	if a.m.mode != modeList || a.saved(t).Agent == nil {
		t.Fatal("回答 n 時本機狀態應保留")
	}
	send(t, a.m, "ctrl+o")
	a.run(send(t, a.m, "y"))
	a.run(send(t, a.m, "y"))
	if a.saved(t).Agent != nil || !strings.Contains(a.m.status, "遠端公鑰仍在") {
		t.Fatalf("只清本機後 status=%q", a.m.status)
	}
}

func TestAgentHotkeyRejectsUnsupported(t *testing.T) {
	a := newAgentTUI(t, store.Host{ID: "h_1", Name: "範例客戶-KEY-VM", Host: "10.0.0.25", Port: 22, User: "ops", Auth: store.AuthKey, IdentityFile: "/k"})
	send(t, a.m, "ctrl+o")
	if a.m.mode != modeList || !a.m.statusErr || !strings.Contains(a.m.status, "只支援 password") {
		t.Fatalf("mode=%v status=%q", a.m.mode, a.m.status)
	}
}

func TestAgentHotkeyWithoutManager(t *testing.T) {
	path, f := setup(t)
	f.Hosts = []store.Host{{ID: "h_1", Name: "a", Host: "10.0.0.1", User: "u", Auth: store.AuthPassword, Password: "test-pass"}}
	m := New(Options{Path: path, File: f})
	send(t, m, "ctrl+o")
	if m.mode != modeList || !m.statusErr {
		t.Fatalf("沒有 Manager 時應顯示錯誤：mode=%v status=%q", m.mode, m.status)
	}
}

func TestHelpMentionsAgentHotkey(t *testing.T) {
	a := newAgentTUI(t)
	if !strings.Contains(a.m.View(), "Ctrl+O AI agent") {
		t.Fatalf("說明列缺 Ctrl+O：\n%s", a.m.View())
	}
}

func TestEditHostResyncsAgentConf(t *testing.T) {
	a := newAgentTUI(t)
	send(t, a.m, "ctrl+o")
	a.run(send(t, a.m, "y"))
	// 編輯：host 改成 10.0.0.99
	send(t, a.m, "ctrl+e", "tab", "tab")
	ti := a.m.form.input(fieldHost)
	ti.SetValue("10.0.0.99")
	send(t, a.m, "ctrl+s")
	if a.m.mode != modeList {
		t.Fatalf("存檔失敗：%s", errMsg(a.m))
	}
	conf, _ := os.ReadFile(filepath.Join(a.home, ".ssh", "sshm_agents.conf"))
	if !strings.Contains(string(conf), "HostName 10.0.0.99") {
		t.Fatalf("conf 應跟著更新：%s", conf)
	}
	if !strings.Contains(a.m.status, "需重新 enable") || !strings.Contains(a.m.status, "舊位置的公鑰會在 disable 時清除") {
		t.Fatalf("應提醒需重新 enable 與舊位置清除：%q", a.m.status)
	}
	if view := a.m.View(); !strings.Contains(view, "位置已變更，需重新 enable") {
		t.Fatalf("清單應標示需重新 enable：\n%s", view)
	}
	if h := a.saved(t); h.Agent == nil {
		t.Fatal("編輯不應清掉開通狀態")
	}
}

// 審查 #2：已開通的機器被改成 ssh 設定無法表示的 user，別名停用且清單與狀態列都看得到，不寫壞 conf。
func TestEditHostUnsafeUserShowsAliasDisabled(t *testing.T) {
	a := newAgentTUI(t)
	send(t, a.m, "ctrl+o")
	a.run(send(t, a.m, "y"))
	send(t, a.m, "ctrl+e", "tab", "tab", "tab", "tab")
	a.m.form.input(fieldUser).SetValue("o'brien")
	send(t, a.m, "ctrl+s")
	if a.m.mode != modeList {
		t.Fatalf("存檔失敗：%s", errMsg(a.m))
	}
	conf, _ := os.ReadFile(filepath.Join(a.home, ".ssh", "sshm_agents.conf"))
	if strings.Contains(string(conf), "o'brien") || strings.Contains(string(conf), "Host sshm-") {
		t.Fatalf("不合法的帳號不應寫進 conf：%s", conf)
	}
	if !strings.Contains(a.m.status, "已停用") {
		t.Fatalf("狀態列應說明別名已停用：%q", a.m.status)
	}
	if view := a.m.View(); !strings.Contains(view, "別名已停用") {
		t.Fatalf("清單應標示別名已停用：\n%s", view)
	}
}
