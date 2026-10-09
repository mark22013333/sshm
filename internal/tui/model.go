// Package tui 是 sshm 的 Bubble Tea 介面。
package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
	"github.com/mark22013333/sshm/internal/tunnelstate"
)

// Options 是啟動 TUI 所需的資料與外部相依。
type Options struct {
	Path       string // hosts.json 路徑
	File       *store.File
	ScriptPath string // expect 登入腳本路徑
	Query      string // 預填搜尋字
	StartAdd   bool   // 直接開新增表單（sshm add）
	Orca       connect.OrcaEnv
	InOrca     bool
	Runner     connect.Runner // 測試可替換；nil 時用 connect.ExecRunner
	StatePath  string         // tunnel 狀態檔 state.json 的路徑
}

// Result 是 TUI 結束後交給呼叫端的動作。
type Result struct {
	// ExecArgv 非空時，呼叫端要以 syscall.Exec 原地執行。
	ExecArgv []string
}

type page int

const (
	pageHosts page = iota
	pageTunnels
)

type mode int

const (
	modeList mode = iota
	modeForm
	modeConfirmDelete
	modeBusy
	modeTunnelForm
	modeConfirmTunnelDelete
)

// Model 是 TUI 的狀態。
type Model struct {
	opts   Options
	file   *store.File
	page   page
	mode   mode
	search textinput.Model

	rows      []row
	cursor    int
	offset    int
	collapsed map[string]bool

	form       *hostForm
	exitOnForm bool // sshm add：表單結束即離開
	deleteID   string
	busyMsg    string

	// Tunnel 頁
	tCursor  int
	tStatus  map[string]tunnelStatus
	tState   *tunnelstate.State
	tTicking bool
	tForm    *tunnelForm
	// 查詢序號：丟棄過時結果、tick 時跳過仍在進行中的查詢
	tSeq, tAppliedSeq, tMinSeq, tInFlight int

	status    string
	statusErr bool
	width     int
	height    int
	result    Result
}

// New 建立 Model。
func New(opts Options) *Model {
	if opts.File == nil {
		opts.File = store.NewFile()
	}
	if opts.Runner == nil {
		opts.Runner = connect.ExecRunner
	}
	s := textinput.New()
	s.Prompt = "🔍 "
	s.Placeholder = "搜尋名稱、host、user、群組"
	s.CharLimit = 256
	s.SetValue(opts.Query)
	s.Focus()
	m := &Model{opts: opts, file: opts.File, search: s, collapsed: map[string]bool{}}
	m.rebuild("")
	if opts.StartAdd {
		m.exitOnForm = true
		m.openForm(store.Host{Port: 22, Auth: store.AuthPassword}, false)
	}
	return m
}

// Result 回傳結束後要執行的動作。
func (m *Model) Result() Result { return m.result }

func (m *Model) Init() tea.Cmd { return textinput.Blink }

func (m *Model) groupNames() []string {
	names := make([]string, 0, len(m.file.Groups))
	for _, g := range m.file.Groups {
		names = append(names, g.Name)
	}
	return names
}

func (m *Model) openForm(h store.Host, editing bool) tea.Cmd {
	m.form = newHostForm(h, editing, m.groupNames())
	m.mode = modeForm
	return m.form.setFocus(fieldName)
}

func (m *Model) setStatus(msg string, isErr bool) {
	m.status, m.statusErr = msg, isErr
}

// mutate 在檔案鎖內重讀最新內容後套用變更並寫回，成功才更新畫面資料，避免覆蓋其他 sshm 的變更。
func (m *Model) mutate(fn func(f *store.File) error) error {
	f, err := store.Update(m.opts.Path, fn)
	if err != nil {
		return err
	}
	m.file = f
	return nil
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.search.Width = max(10, msg.Width-6)
		m.ensureVisible()
		return m, nil
	case tunnelTickMsg:
		return m, m.onTunnelTick()
	case tunnelRefreshMsg:
		m.applyTunnelRefresh(msg)
		return m, nil
	case tunnelActionMsg:
		return m, m.applyTunnelAction(msg)
	case orcaDoneMsg:
		m.mode = modeList
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.mode {
		case modeBusy:
			return m, nil
		case modeForm:
			return m.updateForm(msg)
		case modeConfirmDelete:
			return m.updateConfirm(msg)
		case modeTunnelForm:
			return m.updateTunnelForm(msg)
		case modeConfirmTunnelDelete:
			return m.updateTunnelConfirm(msg)
		}
		if msg.String() == "tab" {
			if m.page == pageHosts {
				m.page = pageTunnels
				m.status = ""
				return m, m.enterTunnelPage()
			}
			m.page = pageHosts
			return m, nil
		}
		if m.page == pageTunnels {
			return m.updateTunnelList(msg)
		}
		return m.updateList(msg)
	}
	if m.mode == modeList && m.page == pageHosts {
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		return m, cmd
	}
	if m.mode == modeTunnelForm {
		if ti := m.tForm.input(m.tForm.current()); ti != nil {
			var cmd tea.Cmd
			*ti, cmd = ti.Update(msg)
			return m, cmd
		}
	}
	if m.mode == modeForm {
		if ti := m.form.input(m.form.focus); ti != nil {
			var cmd tea.Cmd
			*ti, cmd = ti.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *Model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	res, cmd := m.form.update(msg)
	switch res {
	case formCancel:
		m.form, m.mode = nil, modeList
		if m.exitOnForm {
			return m, tea.Quit
		}
		m.setStatus("已取消", false)
	case formSubmit:
		h, _ := m.form.host()
		var id string
		orig, editing := m.form.original, m.form.editing
		if err := m.mutate(func(f *store.File) error {
			if editing {
				id = orig.ID
				return f.ApplyHostEdit(orig, h)
			}
			var err error
			id, err = f.UpsertHost(h)
			return err
		}); err != nil {
			m.form.err = "儲存失敗：" + err.Error()
			return m, cmd
		}
		m.form, m.mode = nil, modeList
		if m.exitOnForm {
			return m, tea.Quit
		}
		m.setStatus("已儲存「"+h.Name+"」", false)
		m.rebuild(id)
	}
	return m, cmd
}

func (m *Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	id := m.deleteID
	m.mode, m.deleteID = modeList, ""
	if msg.String() != "y" && msg.String() != "Y" {
		m.setStatus("已取消刪除", false)
		return m, nil
	}
	i := m.file.HostIndex(id)
	if i < 0 {
		return m, nil
	}
	name := m.file.Hosts[i].Name
	if err := m.mutate(func(f *store.File) error { return f.DeleteHost(id) }); err != nil {
		m.setStatus("刪除失敗："+err.Error(), true)
	} else {
		m.setStatus("已刪除「"+name+"」", false)
	}
	m.rebuild("")
	return m, nil
}

func (m *Model) View() string {
	switch m.mode {
	case modeForm:
		return m.form.view()
	case modeTunnelForm:
		return m.tForm.view()
	}
	header := m.tabsView()
	if m.page == pageTunnels {
		return m.tunnelPageView()
	}
	return header + "\n" + m.search.View() + "\n" + m.listView() + "\n" + m.footerView()
}

func (m *Model) tabsView() string {
	hosts, tunnels := styleTabInactive.Render("機器"), styleTabInactive.Render("Tunnel")
	if m.page == pageHosts {
		hosts = styleTabActive.Render("機器")
	} else {
		tunnels = styleTabActive.Render("Tunnel")
	}
	return hosts + " " + tunnels
}

func (m *Model) footerView() string {
	var status string
	switch {
	case m.mode == modeConfirmDelete:
		name := ""
		if i := m.file.HostIndex(m.deleteID); i >= 0 {
			name = store.Printable(m.file.Hosts[i].Name)
		}
		q := "確定要刪除「" + name + "」嗎？"
		if used := m.file.TunnelsUsingHost(m.deleteID); len(used) > 0 {
			for i := range used {
				used[i] = store.Printable(used[i])
			}
			q += "以下 tunnel 引用這台機器，設定會保留但無法啟動：" + strings.Join(used, "、") + "。"
		}
		status = styleWarn.Render(q + "按 y 確認，其他鍵取消")
	case m.mode == modeBusy:
		status = styleInfo.Render("正在開啟 Orca 分頁…")
	case m.status != "" && m.statusErr:
		status = styleError.Render(m.status)
	case m.status != "":
		status = styleInfo.Render(m.status)
	}
	where := "Enter 原地連線"
	if m.opts.InOrca {
		where = "Enter 原地 · Shift+Enter／Ctrl+T 新分頁"
	}
	help := styleHelp.Render(where + " · ←→ 折疊 · Ctrl+N 新增 · Ctrl+E 編輯 · Ctrl+D 複製 · Ctrl+X 刪除 · Tab 切頁 · Esc 清空/離開")
	if status == "" {
		return help
	}
	return status + "\n" + help
}
