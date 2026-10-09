package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
	"github.com/mark22013333/sshm/internal/tunnelstate"
)

// tunnelStatus 是單條 tunnel 的狀態。
type tunnelStatus int

const (
	tunnelStopped tunnelStatus = iota
	tunnelRunning
	tunnelUnknown // 無法確認：尚未查詢、Orca 無回應、清單不完整、PTY 重連中、啟動中、或不在 Orca 中
)

// tunnelRefreshInterval 是 Tunnel 頁自動刷新狀態的間隔；測試可調整。
var tunnelRefreshInterval = 3 * time.Second

// pendingTimeout 之後仍沒有 handle 的啟動保留紀錄視為中斷，予以清除。
const pendingTimeout = time.Minute

var errTunnelAlreadyStarted = errors.New("已有執行紀錄（可能由另一個 sshm 啟動），請等狀態刷新後再操作")

type tunnelTickMsg struct{}

type tunnelRefreshMsg struct {
	seq   int
	state *tunnelstate.State
	set   *connect.TerminalSet // nil 表示沒有查 Orca
	err   error
}

type tunnelActionMsg struct {
	state *tunnelstate.State
	note  string
	err   error
}

func (m *Model) tunnelTick() tea.Cmd {
	return tea.Tick(tunnelRefreshInterval, func(time.Time) tea.Msg { return tunnelTickMsg{} })
}

// loadTunnelState 同步讀取 state.json，讓有紀錄的 tunnel 在第一次查詢回來前就顯示為無法確認。
func (m *Model) loadTunnelState() {
	st, err := tunnelstate.Load(m.opts.StatePath)
	if err != nil {
		m.setStatus("讀取 tunnel 狀態失敗："+err.Error(), true)
		return
	}
	m.tState = st
}

// enterTunnelPage 進入 Tunnel 頁時立即刷新，並啟動每 3 秒一次的刷新。
func (m *Model) enterTunnelPage() tea.Cmd {
	m.loadTunnelState()
	cmds := []tea.Cmd{m.refreshTunnelsCmd()}
	if !m.tTicking {
		m.tTicking = true
		cmds = append(cmds, m.tunnelTick())
	}
	return tea.Batch(cmds...)
}

// onTunnelTick 上一次查詢還沒回來就跳過這一輪，避免查詢堆積。
func (m *Model) onTunnelTick() tea.Cmd {
	if m.page != pageTunnels {
		m.tTicking = false
		return nil
	}
	if m.tInFlight > 0 {
		return m.tunnelTick()
	}
	return tea.Batch(m.refreshTunnelsCmd(), m.tunnelTick())
}

// refreshTunnelsCmd 查 orca terminal list，清掉 handle 已不存在的紀錄；清單不完整時不清。
func (m *Model) refreshTunnelsCmd() tea.Cmd {
	m.tSeq++
	m.tInFlight++
	seq := m.tSeq
	path, orca, run, inOrca := m.opts.StatePath, m.opts.Orca, m.opts.Runner, m.opts.InOrca
	return func() tea.Msg {
		if !inOrca {
			st, err := tunnelstate.Load(path)
			return tunnelRefreshMsg{seq: seq, state: st, err: err}
		}
		listedAt := time.Now()
		set, err := orca.ListTerminals(run)
		if err != nil {
			st, _ := tunnelstate.Load(path)
			return tunnelRefreshMsg{seq: seq, state: st, err: err}
		}
		st, err := tunnelstate.Update(path, func(s *tunnelstate.State) error {
			for id, e := range s.Tunnels {
				started, perr := time.Parse(time.RFC3339Nano, e.StartedAt)
				if e.Handle == "" {
					// 啟動保留：超過時限仍沒有 handle 視為中斷
					if perr != nil || time.Since(started) > pendingTimeout {
						delete(s.Tunnels, id)
					}
					continue
				}
				// 查詢期間才啟動的 tunnel 不在這次清單裡，不能當成已停止
				if _, listed := set.Handles[e.Handle]; set.Complete && !listed && (perr != nil || started.Before(listedAt)) {
					delete(s.Tunnels, id)
				}
			}
			return nil
		})
		return tunnelRefreshMsg{seq: seq, state: st, set: &set, err: err}
	}
}

func (m *Model) applyTunnelRefresh(msg tunnelRefreshMsg) {
	if m.tInFlight > 0 {
		m.tInFlight--
	}
	// 較早發出、較晚回來的查詢結果已過時（例如啟動或停止之後才回來），丟掉
	if msg.seq < m.tMinSeq || msg.seq < m.tAppliedSeq {
		return
	}
	m.tAppliedSeq = msg.seq
	if msg.state != nil {
		m.tState = msg.state
	}
	m.tStatus = map[string]tunnelStatus{}
	if m.tState == nil {
		return
	}
	for id, e := range m.tState.Tunnels {
		connected, listed := false, false
		if msg.set != nil {
			connected, listed = msg.set.Handles[e.Handle]
		}
		if e.Handle != "" && listed && connected {
			m.tStatus[id] = tunnelRunning
		} else {
			m.tStatus[id] = tunnelUnknown
		}
	}
	if msg.err != nil {
		m.setStatus("無法查詢 tunnel 狀態："+msg.err.Error(), true)
	}
}

// tunnelStatusOf：查詢結果優先；沒有結果時，state 有紀錄就是無法確認，否則已停止。
func (m *Model) tunnelStatusOf(id string) tunnelStatus {
	if s, ok := m.tStatus[id]; ok {
		return s
	}
	if m.hasTunnelRecord(id) {
		return tunnelUnknown
	}
	return tunnelStopped
}

func (m *Model) hasTunnelRecord(id string) bool {
	if m.tState == nil {
		return false
	}
	_, ok := m.tState.Tunnels[id]
	return ok
}

func (m *Model) currentTunnel() (store.Tunnel, bool) {
	if m.tCursor < 0 || m.tCursor >= len(m.file.Tunnels) {
		return store.Tunnel{}, false
	}
	return m.file.Tunnels[m.tCursor], true
}

func (m *Model) updateTunnelList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.status = ""
	switch msg.String() {
	case "esc":
		return m, tea.Quit
	case "up":
		if m.tCursor > 0 {
			m.tCursor--
		}
	case "down":
		if m.tCursor < len(m.file.Tunnels)-1 {
			m.tCursor++
		}
	case "enter":
		return m.toggleTunnel()
	case "ctrl+n":
		if len(m.file.Hosts) == 0 {
			m.setStatus("請先在「機器」頁新增機器", true)
			return m, nil
		}
		return m, m.openTunnelForm(store.Tunnel{}, false)
	case "ctrl+g":
		return m.gotoTunnelTab()
	case "ctrl+e":
		if t, ok := m.currentTunnel(); ok {
			return m, m.openTunnelForm(t, true)
		}
	case "ctrl+x":
		t, ok := m.currentTunnel()
		if !ok {
			return m, nil
		}
		// 以檔案上的最新紀錄為準：只要有執行紀錄（含無法確認、不在 Orca 中）就不能刪
		m.loadTunnelState()
		if m.hasTunnelRecord(t.ID) {
			m.setStatus("「"+store.Printable(t.Name)+"」仍有執行紀錄，請先在 Orca 中按 Enter 停止再刪除", true)
			return m, nil
		}
		m.mode, m.deleteID = modeConfirmTunnelDelete, t.ID
	}
	return m, nil
}

// gotoTunnelTab 讓 Orca 切到該 tunnel 的分頁；◌ 時仍嘗試，失敗就顯示 Orca 的錯誤。
func (m *Model) gotoTunnelTab() (tea.Model, tea.Cmd) {
	t, ok := m.currentTunnel()
	if !ok {
		return m, nil
	}
	if !m.opts.InOrca {
		m.setStatus("需要在 Orca 中執行", true)
		return m, nil
	}
	m.loadTunnelState()
	var handle string
	if m.tState != nil {
		handle = m.tState.Tunnels[t.ID].Handle
	}
	if handle == "" {
		m.setStatus("這條 tunnel 沒有在執行", true)
		return m, nil
	}
	orca, run, name := m.opts.Orca, m.opts.Runner, store.Printable(t.Name)
	return m, func() tea.Msg {
		if err := orca.SwitchTerminal(run, handle); err != nil {
			return tunnelActionMsg{err: err}
		}
		return tunnelActionMsg{note: "已切到「" + name + "」分頁"}
	}
}

// toggleTunnel 執行中就停止，否則啟動。
func (m *Model) toggleTunnel() (tea.Model, tea.Cmd) {
	t, ok := m.currentTunnel()
	if !ok {
		return m, nil
	}
	if !m.opts.InOrca {
		m.setStatus("Tunnel 需要在 Orca 中執行", true)
		return m, nil
	}
	path, orca, run := m.opts.StatePath, m.opts.Orca, m.opts.Runner
	switch m.tunnelStatusOf(t.ID) {
	case tunnelRunning:
		handle := m.tState.Tunnels[t.ID].Handle
		m.mode, m.busyMsg = modeBusy, "正在停止「"+store.Printable(t.Name)+"」…"
		return m, func() tea.Msg {
			if err := orca.CloseTerminal(run, handle); err != nil {
				return tunnelActionMsg{err: err}
			}
			st, err := tunnelstate.Update(path, func(s *tunnelstate.State) error {
				if s.Tunnels[t.ID].Handle == handle {
					delete(s.Tunnels, t.ID)
				}
				return nil
			})
			return tunnelActionMsg{state: st, note: "已停止「" + store.Printable(t.Name) + "」", err: err}
		}
	case tunnelUnknown:
		m.setStatus("無法確認「"+store.Printable(t.Name)+"」是否仍在執行，請稍候狀態刷新", true)
		return m, nil
	}
	i := m.file.HostIndex(t.HostID)
	if i < 0 {
		m.setStatus("無法啟動「"+store.Printable(t.Name)+"」：機器已不存在", true)
		return m, nil
	}
	plan, err := connect.TunnelPlan(m.file.Hosts[i], t, m.opts.ScriptPath)
	if err != nil {
		m.setStatus("無法啟動："+err.Error(), true)
		return m, nil
	}
	m.mode, m.busyMsg = modeBusy, "正在啟動「"+store.Printable(t.Name)+"」…"
	return m, func() tea.Msg {
		// 在 state 的鎖內先保留：已有紀錄（含另一個 sshm 剛啟動的）就拒絕，不覆寫 handle
		stamp := time.Now().Format(time.RFC3339Nano)
		if _, err := tunnelstate.Update(path, func(s *tunnelstate.State) error {
			if _, exists := s.Tunnels[t.ID]; exists {
				return errTunnelAlreadyStarted
			}
			s.Tunnels[t.ID] = tunnelstate.Entry{StartedAt: stamp}
			return nil
		}); err != nil {
			st, _ := tunnelstate.Load(path)
			return tunnelActionMsg{state: st, err: fmt.Errorf("無法啟動「%s」：%w", store.Printable(t.Name), err)}
		}
		handle, oerr := orca.OpenTunnelTab(run, connect.TunnelTitle(t.Name), plan.Text)
		st, err := tunnelstate.Update(path, func(s *tunnelstate.State) error {
			if e, ok := s.Tunnels[t.ID]; !ok || e.StartedAt != stamp {
				return nil
			}
			if oerr != nil {
				delete(s.Tunnels, t.ID)
			} else {
				s.Tunnels[t.ID] = tunnelstate.Entry{Handle: handle, StartedAt: stamp}
			}
			return nil
		})
		if oerr != nil {
			return tunnelActionMsg{state: st, err: oerr}
		}
		if err != nil {
			err = fmt.Errorf("分頁已開啟，但記錄狀態失敗（之後無法從 sshm 停止）：%w", err)
		}
		return tunnelActionMsg{state: st, note: "已啟動「" + store.Printable(t.Name) + "」", err: err}
	}
}

func (m *Model) applyTunnelAction(msg tunnelActionMsg) tea.Cmd {
	m.mode = modeList
	// 動作之前發出的查詢結果都已過時
	m.tMinSeq = m.tSeq + 1
	if msg.state != nil {
		m.tState = msg.state
		m.tStatus = map[string]tunnelStatus{}
	}
	if msg.err != nil {
		m.setStatus(msg.err.Error(), true)
	} else {
		m.setStatus(msg.note, false)
	}
	return m.refreshTunnelsCmd()
}

func (m *Model) openTunnelForm(t store.Tunnel, editing bool) tea.Cmd {
	m.tForm = newTunnelForm(t, editing, m.file.Hosts)
	m.mode = modeTunnelForm
	return m.tForm.setFocus(0)
}

func (m *Model) updateTunnelForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	res, cmd := m.tForm.update(msg)
	switch res {
	case formCancel:
		m.tForm, m.mode = nil, modeList
		m.setStatus("已取消", false)
	case formSubmit:
		t, _ := m.tForm.tunnel()
		editing := m.tForm.editing
		var id string
		if err := m.mutate(func(f *store.File) error {
			var err error
			id, err = f.UpsertTunnel(t, editing)
			return err
		}); err != nil {
			m.tForm.err = "儲存失敗：" + err.Error()
			return m, cmd
		}
		m.tForm, m.mode = nil, modeList
		note := "已儲存「" + store.Printable(t.Name) + "」"
		if m.tunnelStatusOf(id) == tunnelRunning {
			note += "；執行中的 tunnel 要停止後重新啟動才會套用"
		}
		m.setStatus(note, false)
		if i := m.file.TunnelIndex(id); i >= 0 {
			m.tCursor = i
		}
	}
	return m, cmd
}

func (m *Model) updateTunnelConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	id := m.deleteID
	m.mode, m.deleteID = modeList, ""
	if msg.String() != "y" && msg.String() != "Y" {
		m.setStatus("已取消刪除", false)
		return m, nil
	}
	i := m.file.TunnelIndex(id)
	if i < 0 {
		return m, nil
	}
	name := m.file.Tunnels[i].Name
	if err := m.mutate(func(f *store.File) error { return f.DeleteTunnel(id) }); err != nil {
		m.setStatus("刪除失敗："+err.Error(), true)
		return m, nil
	}
	m.setStatus("已刪除「"+store.Printable(name)+"」", false)
	if m.tCursor >= len(m.file.Tunnels) {
		m.tCursor = max(0, len(m.file.Tunnels)-1)
	}
	return m, nil
}

func addrDisplay(a string) string {
	if strings.Contains(a, ":") && !strings.HasPrefix(a, "[") {
		return "[" + a + "]"
	}
	return a
}

// ruleSummary 例：L 13306→127.0.0.1:3306、D 1080。
func ruleSummary(r store.Rule) string {
	bind := strconv.Itoa(r.BindPort)
	if r.BindAddress != "" {
		bind = addrDisplay(r.BindAddress) + ":" + bind
	}
	if r.Type == "D" {
		return "D " + bind
	}
	return fmt.Sprintf("%s %s→%s:%d", r.Type, bind, addrDisplay(r.TargetHost), r.TargetPort)
}

func (m *Model) tunnelPageView() string {
	var b strings.Builder
	b.WriteString(m.tabsView() + "\n")
	if !m.opts.InOrca {
		b.WriteString(styleWarn.Render("Tunnel 需要在 Orca 中執行；目前唯讀，仍可新增、編輯、刪除設定。") + "\n")
	}
	if len(m.file.Tunnels) == 0 {
		b.WriteString("\n" + styleDim.Render("還沒有任何 tunnel，按 Ctrl+N 新增。") + "\n")
	}
	for i, t := range m.file.Tunnels {
		light := styleDim.Render("○")
		switch m.tunnelStatusOf(t.ID) {
		case tunnelRunning:
			light = styleInfo.Render("●")
		case tunnelUnknown:
			light = styleWarn.Render("◌")
		}
		host := styleError.Render("機器已不存在")
		if j := m.file.HostIndex(t.HostID); j >= 0 {
			host = store.Printable(m.file.Hosts[j].Name)
		}
		var rules []string
		for _, r := range t.Rules {
			rules = append(rules, store.Printable(ruleSummary(r)))
		}
		marker := "  "
		name := store.Printable(t.Name)
		if i == m.tCursor {
			marker = styleSelected.Render("› ")
			name = styleSelected.Render(name)
		}
		b.WriteString(fmt.Sprintf("%s%s %s  %s  %s\n", marker, light, name, host, styleDim.Render(strings.Join(rules, "、"))))
	}
	b.WriteString("\n")
	switch {
	case m.mode == modeConfirmTunnelDelete:
		name := ""
		if i := m.file.TunnelIndex(m.deleteID); i >= 0 {
			name = store.Printable(m.file.Tunnels[i].Name)
		}
		b.WriteString(styleWarn.Render("確定要刪除 tunnel「"+name+"」嗎？按 y 確認，其他鍵取消") + "\n")
	case m.mode == modeBusy:
		b.WriteString(styleInfo.Render(m.busyMsg) + "\n")
	case m.status != "" && m.statusErr:
		b.WriteString(styleError.Render(m.status) + "\n")
	case m.status != "":
		b.WriteString(styleInfo.Render(m.status) + "\n")
	}
	b.WriteString(styleHelp.Render("● 執行中 ○ 已停止 ◌ 無法確認 · Enter 啟動／停止 · Ctrl+G 前往分頁 · Ctrl+N 新增 · Ctrl+E 編輯 · Ctrl+X 刪除 · Tab 切頁 · Esc 離開"))
	return b.String()
}
