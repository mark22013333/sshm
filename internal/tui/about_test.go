package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/selfupdate"
)

// fakeUpdate 是假的更新服務，不連網、不動檔案。
type fakeUpdate struct {
	current  string
	latest   string
	checkErr error
	elig     selfupdate.Eligibility
	applyVer string
	applyErr error
	applied  int
	checks   int
}

func (f *fakeUpdate) CurrentVersion() string              { return f.current }
func (f *fakeUpdate) Eligibility() selfupdate.Eligibility { return f.elig }
func (f *fakeUpdate) Apply(context.Context) (string, error) {
	f.applied++
	return f.applyVer, f.applyErr
}
func (f *fakeUpdate) CheckLatest(context.Context) (string, error) {
	f.checks++
	return f.latest, f.checkErr
}

func newAboutModel(t *testing.T, up *fakeUpdate) *Model {
	t.Helper()
	dir := t.TempDir()
	opts := Options{
		Path: filepath.Join(dir, "sshm", "hosts.json"), File: sampleFile(),
		About: AboutPaths{Exe: "/opt/範例/bin/sshm", AgentConf: "/home/範例/.ssh/sshm_agents.conf", KeyDir: "/home/範例/.ssh/sshm"},
	}
	if up != nil {
		opts.Update = up
	}
	return New(opts)
}

// runInit 執行 Init 回傳的 Cmd（含 Batch），把非 nil 的訊息交回 Model。
func runInit(t *testing.T, m *Model) []tea.Msg {
	t.Helper()
	var msgs []tea.Msg
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, sub := range msg {
				walk(sub)
			}
		case nil:
		default:
			msgs = append(msgs, msg)
		}
	}
	walk(m.Init())
	for _, msg := range msgs {
		m.Update(msg)
	}
	return msgs
}

func TestTabCyclesThreePages(t *testing.T) {
	m := newAboutModel(t, nil)
	want := []page{pageTunnels, pageAbout, pageHosts, pageTunnels}
	for i, p := range want {
		send(t, m, "tab")
		if m.page != p {
			t.Fatalf("第 %d 次 Tab 後 page = %d，want %d", i+1, m.page, p)
		}
	}
	send(t, m, "tab")
	if !strings.Contains(m.View(), "關於") || !strings.Contains(m.tabsView(), "關於") {
		t.Fatal("頁籤列應有「關於」")
	}
}

func TestAboutPageContent(t *testing.T) {
	up := &fakeUpdate{current: "v0.3.0", latest: "v0.4.0", elig: selfupdate.Eligibility{OK: true}}
	m := newAboutModel(t, up)
	runInit(t, m)
	send(t, m, "tab", "tab")
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 200})
	v := m.View()
	for _, s := range []string{
		"目前版本", "v0.3.0", "最新版本", "v0.4.0", "按 U 更新",
		filepath.Join(filepath.Dir(m.opts.Path), "hosts.json"),
		"/home/範例/.ssh/sshm_agents.conf", "/home/範例/.ssh/sshm", "/opt/範例/bin/sshm",
		"機器頁", "Tunnel 頁", "機器表單", "Ctrl+R", "Ctrl+O", "Ctrl+G", "Ctrl+S",
		"sshm agent list --json", "ssh <別名>", "不防惡意", "不需要也不會接觸密碼",
		"https://github.com/mark22013333/sshm",
	} {
		if !strings.Contains(v, s) {
			t.Errorf("關於頁缺少 %q", s)
		}
	}
	// 依序：版本 → 檔案位置 → 快捷鍵 → AI agent → GitHub
	order := []string{"版本", "檔案位置", "快捷鍵", "AI agent 用法", "GitHub"}
	last := -1
	for _, s := range order {
		i := strings.Index(v, s)
		if i < last {
			t.Fatalf("區段順序錯誤：%q 出現在前一區段之前", s)
		}
		last = i
	}
}

func TestAboutPageDevBuildShowsManual(t *testing.T) {
	up := &fakeUpdate{current: "(devel)", latest: "v0.4.0",
		elig: selfupdate.Eligibility{Reason: "目前是開發版（(devel)），不自動更新", Manual: selfupdate.GoInstallCmd}}
	m := newAboutModel(t, up)
	runInit(t, m)
	send(t, m, "tab", "tab")
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 200})
	v := m.View()
	if !strings.Contains(v, "開發版，不自動更新") || !strings.Contains(v, selfupdate.GoInstallCmd) {
		t.Fatalf("開發版應標示並附手動指令：%s", v)
	}
	send(t, m, "u")
	if up.applied != 0 || !m.statusErr || !strings.Contains(m.status, selfupdate.GoInstallCmd) {
		t.Fatalf("開發版按 U 不應更新：applied=%d status=%q", up.applied, m.status)
	}
}

func TestAboutPageScroll(t *testing.T) {
	m := newAboutModel(t, &fakeUpdate{current: "v0.3.0", elig: selfupdate.Eligibility{OK: true}})
	send(t, m, "tab", "tab")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	top := m.View()
	if !strings.Contains(top, "目前版本") || strings.Contains(top, "https://github.com/mark22013333/sshm") {
		t.Fatalf("畫面太小時一開始應只看到開頭：%s", top)
	}
	if lines := strings.Count(top, "\n") + 1; lines > 12 {
		t.Fatalf("畫面高度 12，實際輸出 %d 行", lines)
	}
	for i := 0; i < 200; i++ {
		send(t, m, "down")
	}
	bottom := m.View()
	if !strings.Contains(bottom, "https://github.com/mark22013333/sshm") || strings.Contains(bottom, "目前版本") {
		t.Fatalf("捲到底應看到 GitHub：%s", bottom)
	}
	end := m.aboutOffset
	send(t, m, "down")
	if m.aboutOffset != end {
		t.Fatal("捲到底後不應再往下")
	}
	send(t, m, "pgup", "pgup", "pgup", "pgup", "pgup", "pgup", "pgup", "pgup", "pgup", "pgup")
	if m.aboutOffset != 0 || !strings.Contains(m.View(), "目前版本") {
		t.Fatal("PgUp 應捲回頂端")
	}
	send(t, m, "up")
	if m.aboutOffset != 0 {
		t.Fatal("頂端不應再往上")
	}
}

func TestStatusBarNewVersionNotice(t *testing.T) {
	up := &fakeUpdate{current: "v0.3.0", latest: "v0.4.0", elig: selfupdate.Eligibility{OK: true}}
	m := newAboutModel(t, up)
	runInit(t, m)
	if up.checks != 1 {
		t.Fatalf("啟動時應在背景檢查一次，實際 %d 次", up.checks)
	}
	notice := "有新版 v0.4.0，到「關於」按 U 更新"
	if !strings.Contains(m.View(), notice) {
		t.Fatalf("狀態列應提示新版：%s", m.View())
	}
	send(t, m, "a") // 打字搜尋會清掉一般狀態，但新版提示要留著
	if !strings.Contains(m.View(), notice) {
		t.Fatal("新版提示不應被按鍵清掉")
	}
}

func TestStatusBarNoNoticeWhenUpToDateOrOffline(t *testing.T) {
	cases := map[string]*fakeUpdate{
		"已是最新": {current: "v0.4.0", latest: "v0.4.0"},
		"離線":   {current: "v0.3.0", checkErr: errors.New("dial tcp: no route to host")},
		"開發版":  {current: "(devel)", latest: "v0.4.0"},
	}
	for name, up := range cases {
		t.Run(name, func(t *testing.T) {
			m := newAboutModel(t, up)
			runInit(t, m)
			v := m.View()
			if strings.Contains(v, "有新版") || strings.Contains(v, "no route") || m.statusErr {
				t.Fatalf("不應提示或報錯：%s", v)
			}
		})
	}
	m := newAboutModel(t, nil)
	if msgs := runInit(t, m); len(msgs) > 1 {
		t.Fatalf("沒有更新服務時不應檢查，收到 %v", msgs)
	}
}

func TestAboutUpdateSuccess(t *testing.T) {
	up := &fakeUpdate{current: "v0.3.0", latest: "v0.4.0", elig: selfupdate.Eligibility{OK: true}, applyVer: "v0.4.0"}
	m := newAboutModel(t, up)
	runInit(t, m)
	send(t, m, "tab", "tab")
	cmd := send(t, m, "U")
	if m.mode != modeBusy || cmd == nil || !strings.Contains(m.View(), "v0.4.0") {
		t.Fatalf("按 U 應進入下載中狀態：mode=%d view=%s", m.mode, m.View())
	}
	send(t, m, "u") // 下載中再按不應重複觸發
	m.Update(cmd())
	if up.applied != 1 {
		t.Fatalf("Apply 次數 = %d", up.applied)
	}
	if m.mode != modeList || m.statusErr || m.status != "已更新為 v0.4.0，請重開 sshm" {
		t.Fatalf("status = %q err=%v", m.status, m.statusErr)
	}
	send(t, m, "u")
	if up.applied != 1 {
		t.Fatal("已更新後再按 U 不應再下載")
	}
	send(t, m, "tab")
	if !strings.Contains(m.View(), "已更新為 v0.4.0，請重開 sshm") {
		t.Fatal("回機器頁後仍應提示重開")
	}
}

func TestAboutUpdateFailureAndUpToDate(t *testing.T) {
	up := &fakeUpdate{current: "v0.3.0", latest: "v0.4.0", elig: selfupdate.Eligibility{OK: true},
		applyErr: errors.New("sshm_darwin_arm64.tar.gz 的 SHA-256 與 checksums.txt 不符，已中止更新")}
	m := newAboutModel(t, up)
	send(t, m, "tab", "tab")
	m.Update(send(t, m, "u")())
	if !m.statusErr || !strings.Contains(m.status, "更新失敗") || !strings.Contains(m.status, "SHA-256") {
		t.Fatalf("失敗應回報：%q", m.status)
	}

	up2 := &fakeUpdate{current: "v0.4.0", elig: selfupdate.Eligibility{OK: true}, applyVer: "v0.4.0", applyErr: selfupdate.ErrUpToDate}
	m2 := newAboutModel(t, up2)
	send(t, m2, "tab", "tab")
	m2.Update(send(t, m2, "u")())
	if m2.statusErr || !strings.Contains(m2.status, "已是最新版") {
		t.Fatalf("已是最新版：%q", m2.status)
	}
}

func TestAboutUpdateNotWritableShowsManual(t *testing.T) {
	up := &fakeUpdate{current: "v0.3.0", latest: "v0.4.0",
		elig: selfupdate.Eligibility{Reason: "無法寫入 /opt/範例/bin（不會嘗試 sudo）", Manual: "curl -fsSL … | tar xz -C '/opt/範例/bin' sshm"}}
	m := newAboutModel(t, up)
	runInit(t, m)
	send(t, m, "tab", "tab")
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 200})
	if v := m.View(); !strings.Contains(v, "無法寫入 /opt/範例/bin") || !strings.Contains(v, "tar xz -C '/opt/範例/bin' sshm") {
		t.Fatalf("關於頁應顯示原因與手動指令：%s", v)
	}
	send(t, m, "u")
	if up.applied != 0 || !m.statusErr {
		t.Fatal("不可寫時按 U 不應嘗試更新")
	}
}

func TestAboutStatusClearedWhenBackToHosts(t *testing.T) {
	up := &fakeUpdate{current: "v0.3.0", latest: "v0.4.0", elig: selfupdate.Eligibility{OK: true},
		applyErr: errors.New("下載 checksums.txt 失敗：HTTP 503")}
	m := newAboutModel(t, up)
	runInit(t, m)
	send(t, m, "tab", "tab")
	m.Update(send(t, m, "u")())
	if !strings.Contains(m.status, "更新失敗") {
		t.Fatalf("前置條件：關於頁應有失敗訊息，status=%q", m.status)
	}
	send(t, m, "tab")
	if m.page != pageHosts {
		t.Fatalf("page = %d", m.page)
	}
	v := m.View()
	if m.status != "" || m.statusErr || strings.Contains(v, "更新失敗") {
		t.Fatalf("回機器頁應清掉關於頁的狀態：status=%q", m.status)
	}
	if !strings.Contains(v, "有新版 v0.4.0，到「關於」按 U 更新") {
		t.Fatalf("回機器頁應立即顯示新版提示：%s", v)
	}
}
