package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mark22013333/sshm/internal/selfupdate"
)

// UpdateService 是「關於」頁需要的版本檢查與自我更新；*selfupdate.Updater 即為實作。
type UpdateService interface {
	CurrentVersion() string
	Eligibility() selfupdate.Eligibility
	CheckLatest(ctx context.Context) (string, error)
	Apply(ctx context.Context) (string, error)
}

// AboutPaths 是「關於」頁列出的檔案位置（皆為實際路徑）。
type AboutPaths struct {
	Exe       string // 執行檔（已解析 symlink）
	AgentConf string // ~/.ssh/sshm_agents.conf
	KeyDir    string // ~/.ssh/sshm
}

type updateCheckMsg struct {
	latest string
	err    error
}

type updateDoneMsg struct {
	version string
	err     error
}

// shortcut 是關於頁快捷鍵一覽的一列；內容須與各頁實際的按鍵處理一致。
type shortcut struct{ key, desc string }

var aboutShortcuts = []struct {
	title string
	keys  []shortcut
}{
	{"機器頁", []shortcut{
		{"直接打字", "搜尋名稱、host、user、群組"},
		{"↑↓", "移動"},
		{"PgUp／PgDn", "一次移動 10 列"},
		{"Enter", "原地連線（在群組列上是折疊／展開）"},
		{"Shift+Enter／Ctrl+T", "在 Orca 開新分頁連線（不在 Orca 時同 Enter）"},
		{"←→", "折疊／展開群組"},
		{"Ctrl+N", "新增機器"},
		{"Ctrl+E", "編輯機器"},
		{"Ctrl+D", "複製機器"},
		{"Ctrl+X", "刪除機器（按 y 確認）"},
		{"Ctrl+O", "開通／停用 AI agent 存取（按 y 確認）"},
		{"Tab", "切到下一頁"},
		{"Esc", "清空搜尋，再按一次離開"},
	}},
	{"Tunnel 頁", []shortcut{
		{"↑↓", "移動"},
		{"Enter", "啟動／停止（需在 Orca 中）"},
		{"Ctrl+G", "前往該 tunnel 的 Orca 分頁"},
		{"Ctrl+N", "新增 tunnel"},
		{"Ctrl+E", "編輯 tunnel"},
		{"Ctrl+X", "刪除 tunnel（按 y 確認）"},
		{"Tab", "切到下一頁"},
		{"Esc", "離開"},
	}},
	{"機器表單", []shortcut{
		{"Tab／↓", "下一欄"},
		{"Shift+Tab／↑", "上一欄"},
		{"Enter", "下一欄（在「自訂指令」欄時儲存）"},
		{"←→／空白", "切換認證方式、顏色"},
		{"Ctrl+G", "群組欄：循環選既有群組"},
		{"Ctrl+R", "認證方式為 password 時顯示／隱藏密碼"},
		{"Ctrl+S", "儲存"},
		{"Esc", "取消"},
	}},
	{"關於頁", []shortcut{
		{"↑↓／PgUp／PgDn", "捲動"},
		{"U", "更新到最新版"},
		{"Tab", "切到下一頁"},
		{"Esc", "離開"},
	}},
	{"全域", []shortcut{
		{"Ctrl+C", "任何畫面都直接離開"},
	}},
}

func (m *Model) checkUpdateCmd() tea.Cmd {
	svc := m.opts.Update
	return func() tea.Msg {
		latest, err := svc.CheckLatest(context.Background())
		return updateCheckMsg{latest: latest, err: err}
	}
}

// applyUpdateCheck 背景檢查失敗時靜默，只保留已知的最新版。
func (m *Model) applyUpdateCheck(msg updateCheckMsg) {
	if msg.latest != "" {
		m.latest = msg.latest
	}
}

func (m *Model) currentVersion() string {
	if m.opts.Update == nil {
		return "（未提供）"
	}
	return m.opts.Update.CurrentVersion()
}

// updateNotice 是機器頁狀態列的常駐提示；不會被按鍵清掉。
func (m *Model) updateNotice() string {
	if m.opts.Update == nil {
		return ""
	}
	if m.updatedTo != "" {
		return "已更新為 " + m.updatedTo + "，請重開 sshm"
	}
	if selfupdate.IsNewer(m.latest, m.currentVersion()) {
		return "有新版 " + m.latest + "，到「關於」按 U 更新"
	}
	return ""
}

func (m *Model) startUpdate() (tea.Model, tea.Cmd) {
	svc := m.opts.Update
	if svc == nil {
		m.setStatus("這個版本沒有啟用自動更新", true)
		return m, nil
	}
	if m.updatedTo != "" {
		m.setStatus("已更新為 "+m.updatedTo+"，請重開 sshm", false)
		return m, nil
	}
	if e := svc.Eligibility(); !e.OK {
		m.setStatus(e.Reason+"。手動更新："+e.Manual, true)
		return m, nil
	}
	target := m.latest
	if target == "" {
		target = "最新版"
	}
	m.status = ""
	m.mode, m.busyMsg = modeBusy, "正在下載 "+target+" 並驗證 SHA-256…"
	return m, func() tea.Msg {
		v, err := svc.Apply(context.Background())
		return updateDoneMsg{version: v, err: err}
	}
}

func (m *Model) applyUpdateDone(msg updateDoneMsg) {
	m.mode, m.busyMsg = modeList, ""
	switch {
	case errors.Is(msg.err, selfupdate.ErrUpToDate):
		if msg.version != "" {
			m.latest = msg.version
		}
		m.setStatus("已是最新版 "+m.currentVersion(), false)
	case msg.err != nil:
		m.setStatus("更新失敗："+oneLine(msg.err.Error()), true)
	default:
		m.updatedTo, m.latest = msg.version, msg.version
		m.setStatus("已更新為 "+msg.version+"，請重開 sshm", false)
	}
}

func (m *Model) updateAbout(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, tea.Quit
	case "up":
		m.scrollAbout(-1)
	case "down":
		m.scrollAbout(1)
	case "pgup":
		m.scrollAbout(-max(1, m.aboutBodyHeight()))
	case "pgdown":
		m.scrollAbout(max(1, m.aboutBodyHeight()))
	case "u", "U":
		return m.startUpdate()
	}
	return m, nil
}

func (m *Model) scrollAbout(delta int) {
	m.aboutOffset += delta
	m.clampAbout()
}

func (m *Model) clampAbout() {
	limit := 0
	if avail := m.aboutBodyHeight(); avail > 0 {
		limit = max(0, len(m.aboutLines())-avail)
	}
	m.aboutOffset = min(max(m.aboutOffset, 0), limit)
}

// wrapLines 依畫面寬度折行，讓捲動以實際顯示的行數計算。
func (m *Model) wrapLines(lines []string) []string {
	if m.width <= 0 {
		return lines
	}
	var out []string
	for _, l := range lines {
		wrapped := lipgloss.NewStyle().Width(m.width).Render(l)
		for _, w := range strings.Split(wrapped, "\n") {
			out = append(out, strings.TrimRight(w, " "))
		}
	}
	return out
}

func (m *Model) aboutFooter() []string {
	var lines []string
	switch {
	case m.mode == modeBusy:
		lines = append(lines, styleInfo.Render(m.busyMsg))
	case m.status != "" && m.statusErr:
		lines = append(lines, styleError.Render(m.status))
	case m.status != "":
		lines = append(lines, styleInfo.Render(m.status))
	}
	lines = append(lines, styleHelp.Render("↑↓／PgUp／PgDn 捲動 · U 更新 · Tab 切頁 · Esc 離開"))
	return m.wrapLines(lines)
}

// aboutBodyHeight 是內容區可用的行數；0 表示尚未取得畫面大小（全部顯示）。
func (m *Model) aboutBodyHeight() int {
	if m.height <= 0 {
		return 0
	}
	return max(1, m.height-1-len(m.aboutFooter()))
}

func row2(key, desc string) string {
	return "  " + lipgloss.NewStyle().Width(26).Render(key) + desc
}

func (m *Model) aboutLines() []string {
	var l []string
	section := func(title string) {
		if len(l) > 0 {
			l = append(l, "")
		}
		l = append(l, styleGroup.Render(title))
	}

	section("版本")
	cur := m.currentVersion()
	l = append(l, row2("目前版本", cur))
	latest := m.latest
	if latest == "" {
		latest = styleDim.Render("尚未取得（每天在背景檢查一次，離線時略過）")
	}
	l = append(l, row2("最新版本", latest))
	if svc := m.opts.Update; svc != nil {
		e := svc.Eligibility()
		var state string
		switch {
		case m.updatedTo != "":
			state = styleInfo.Render("已更新為 " + m.updatedTo + "，請重開 sshm")
		case !selfupdate.IsRelease(cur):
			state = styleWarn.Render("開發版，不自動更新")
		case !e.OK:
			state = styleWarn.Render(e.Reason)
		case selfupdate.IsNewer(m.latest, cur):
			state = styleWarn.Render("有新版 " + m.latest + " 可更新")
		case m.latest != "":
			state = "已是最新版"
		default:
			state = "尚未確認"
		}
		l = append(l, row2("更新狀態", state))
		switch {
		case m.updatedTo != "":
		case !e.OK:
			l = append(l, row2("手動更新", e.Manual))
		case selfupdate.IsNewer(m.latest, cur):
			l = append(l, row2("", "按 U 更新到 "+m.latest))
		default:
			l = append(l, row2("", "按 U 立即檢查並更新"))
		}
	}

	section("檔案位置")
	hosts := m.opts.Path
	if real, err := filepath.EvalSymlinks(hosts); err == nil && real != hosts {
		hosts += " → " + real
	}
	l = append(l, row2("機器清單", hosts))
	conf, keys := m.opts.About.AgentConf, m.opts.About.KeyDir
	if mgr := m.opts.Agent; mgr != nil {
		if conf == "" {
			conf = mgr.Paths.Conf
		}
		if keys == "" {
			keys = mgr.Paths.KeyDir
		}
	}
	if keys != "" {
		keys += string(filepath.Separator)
	}
	l = append(l, row2("agent 別名設定", orUnknown(conf)))
	l = append(l, row2("agent 金鑰目錄", orUnknown(keys)))
	l = append(l, row2("執行檔", orUnknown(m.opts.About.Exe)))

	for _, sec := range aboutShortcuts {
		section("快捷鍵：" + sec.title)
		for _, k := range sec.keys {
			l = append(l, row2(k.key, k.desc))
		}
	}

	section("AI agent 用法")
	l = append(l, row2("sshm agent list --json", "找已開通的機器與別名（不含密碼）"))
	l = append(l, row2("ssh <別名> '<指令>'", "連線，不需要密碼"))
	l = append(l, "  安全界線：確認關卡只防誤開通，不防惡意繞過（agent 與 sshm 同一個 OS 帳號，技術上讀得到密碼與私鑰）；")
	l = append(l, "  照 sshm agent guide 的方式使用時只需要 ssh <別名>，不需要也不會接觸密碼。")

	section("GitHub")
	l = append(l, "  "+selfupdate.RepoURL)
	return m.wrapLines(l)
}

func orUnknown(s string) string {
	if s == "" {
		return "（取不到）"
	}
	return s
}

func (m *Model) aboutView() string {
	lines := m.aboutLines()
	m.clampAbout()
	if avail := m.aboutBodyHeight(); avail > 0 {
		end := min(len(lines), m.aboutOffset+avail)
		lines = lines[m.aboutOffset:end]
	}
	return m.tabsView() + "\n" + strings.Join(lines, "\n") + "\n" + strings.Join(m.aboutFooter(), "\n")
}
