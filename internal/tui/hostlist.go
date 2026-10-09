package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/catalog"
	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
)

type row struct {
	group  string // 群組列與機器列都記錄所屬群組
	count  int    // 群組列：符合的機器數
	color  string
	host   *store.Host
	isHost bool
}

func (r row) height() int {
	if r.isHost {
		return 2
	}
	return 1
}

const ungroupedLabel = "未分組"

// rebuild 依搜尋字重建列；selectID 非空時把游標移到該機器，否則盡量保持原位置。
func (m *Model) rebuild(selectID string) {
	prevGroup, prevHost := "", selectID
	if selectID == "" && m.cursor < len(m.rows) {
		r := m.rows[m.cursor]
		prevGroup = r.group
		if r.isHost {
			prevHost = r.host.ID
		}
	}
	query := m.search.Value()
	searching := strings.TrimSpace(query) != ""
	m.rows = m.rows[:0]
	for _, sec := range catalog.Build(m.file, query) {
		m.rows = append(m.rows, row{group: sec.Name, count: len(sec.Hosts), color: sec.Color})
		if m.collapsed[sec.Name] && !searching {
			continue
		}
		for i := range sec.Hosts {
			h := sec.Hosts[i]
			m.rows = append(m.rows, row{group: sec.Name, color: m.file.EffectiveColor(h), host: &h, isHost: true})
		}
	}
	m.cursor = m.findCursor(prevHost, prevGroup, searching)
	m.ensureVisible()
}

func (m *Model) findCursor(hostID, group string, searching bool) int {
	if hostID != "" {
		for i, r := range m.rows {
			if r.isHost && r.host.ID == hostID {
				return i
			}
		}
	}
	if !searching && group != "" {
		for i, r := range m.rows {
			if !r.isHost && r.group == group {
				return i
			}
		}
	}
	// 預設停在第一台機器，讓搜尋後直接按 Enter 就能連線
	for i, r := range m.rows {
		if r.isHost {
			return i
		}
	}
	return 0
}

func (m *Model) current() (row, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return row{}, false
	}
	return m.rows[m.cursor], true
}

func (m *Model) currentHost() (store.Host, bool) {
	r, ok := m.current()
	if !ok || !r.isHost {
		return store.Host{}, false
	}
	return *r.host, true
}

func (m *Model) listHeight() int {
	if m.height <= 0 {
		return 0
	}
	// 頁籤、搜尋框、狀態列、說明列
	return max(2, m.height-4)
}

func (m *Model) ensureVisible() {
	avail := m.listHeight()
	if avail == 0 || len(m.rows) == 0 {
		m.offset = 0
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	for m.offset < m.cursor {
		used := 0
		for i := m.offset; i <= m.cursor; i++ {
			used += m.rows[i].height()
		}
		if used <= avail {
			break
		}
		m.offset++
	}
}

func (m *Model) moveCursor(delta int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor = min(max(m.cursor+delta, 0), len(m.rows)-1)
	m.ensureVisible()
}

func (m *Model) setCollapsed(collapse bool) {
	r, ok := m.current()
	if !ok || strings.TrimSpace(m.search.Value()) != "" {
		return
	}
	m.collapsed[r.group] = collapse
	m.rebuild("")
	if collapse {
		m.cursor = m.findCursor("", r.group, false)
		m.ensureVisible()
	}
}

func (m *Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.status = ""
	switch msg.String() {
	case "up":
		m.moveCursor(-1)
		return m, nil
	case "down":
		m.moveCursor(1)
		return m, nil
	case "pgup":
		m.moveCursor(-10)
		return m, nil
	case "pgdown":
		m.moveCursor(10)
		return m, nil
	case "left":
		m.setCollapsed(true)
		return m, nil
	case "right":
		m.setCollapsed(false)
		return m, nil
	case "enter":
		r, ok := m.current()
		if !ok {
			return m, nil
		}
		if !r.isHost {
			m.setCollapsed(!m.collapsed[r.group])
			return m, nil
		}
		return m.connect(*r.host, false)
	// Orca 把 Shift+Enter 送成 ESC CR，與 alt+enter 同一序列。
	case "alt+enter", "ctrl+t":
		if h, ok := m.currentHost(); ok {
			return m.connect(h, m.opts.InOrca)
		}
		return m, nil
	case "ctrl+n":
		group := ""
		if r, ok := m.current(); ok {
			group = r.group
		}
		return m, m.openForm(store.Host{Port: 22, Auth: store.AuthPassword, Group: group}, false)
	case "ctrl+e":
		if h, ok := m.currentHost(); ok {
			return m, m.openForm(h, true)
		}
		m.setStatus("請先選一台機器", true)
		return m, nil
	case "ctrl+d":
		h, ok := m.currentHost()
		if !ok {
			m.setStatus("請先選一台機器", true)
			return m, nil
		}
		var id string
		if err := m.mutate(func(f *store.File) error {
			var err error
			id, err = f.DuplicateHost(h.ID)
			return err
		}); err != nil {
			m.setStatus("複製失敗："+err.Error(), true)
			return m, nil
		}
		m.setStatus("已複製「"+h.Name+"」", false)
		m.rebuild(id)
		return m, nil
	case "ctrl+x":
		if h, ok := m.currentHost(); ok {
			m.mode, m.deleteID = modeConfirmDelete, h.ID
		} else {
			m.setStatus("請先選一台機器", true)
		}
		return m, nil
	case "esc":
		if m.search.Value() != "" {
			m.search.SetValue("")
			m.rebuild("")
			return m, nil
		}
		return m, tea.Quit
	}
	before := m.search.Value()
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	if m.search.Value() != before {
		m.cursor = 0
		m.rebuild("")
		m.cursor = m.findCursor("", "", true)
		m.ensureVisible()
	}
	return m, cmd
}

type orcaDoneMsg struct{ err error }

// connect 依位置開連線：newTab 且在 Orca 中時開分頁，否則結束 TUI 後原地執行。
func (m *Model) connect(h store.Host, newTab bool) (tea.Model, tea.Cmd) {
	plan, err := connect.HostPlan(h, m.opts.ScriptPath)
	if err != nil {
		m.setStatus("無法連線："+err.Error(), true)
		return m, nil
	}
	if !newTab {
		m.result.ExecArgv = plan.Argv
		return m, tea.Quit
	}
	title := connect.Title(store.ColorEmoji(m.file.EffectiveColor(h)), h.Name)
	orca, run := m.opts.Orca, m.opts.Runner
	m.mode = modeBusy
	return m, func() tea.Msg {
		_, err := orca.OpenTab(run, title, plan.Text)
		return orcaDoneMsg{err: err}
	}
}

func (m *Model) rowView(r row, selected bool) string {
	marker := "  "
	if selected {
		marker = styleSelected.Render("› ")
	}
	if !r.isHost {
		arrow := "▾"
		if m.collapsed[r.group] && strings.TrimSpace(m.search.Value()) == "" {
			arrow = "▸"
		}
		name := r.group
		if name == "" {
			name = ungroupedLabel
		}
		emoji := store.ColorEmoji(r.color)
		if emoji != "" {
			emoji += " "
		}
		text := fmt.Sprintf("%s %s%s (%d)", arrow, emoji, name, r.count)
		if selected {
			return marker + styleSelected.Render(text)
		}
		return marker + styleGroup.Render(text)
	}
	h := r.host
	bar := colorBar(r.color)
	name := h.Name
	if selected {
		name = styleSelected.Render(name)
	}
	port := h.Port
	if port <= 0 {
		port = 22
	}
	sub := styleDim.Render(fmt.Sprintf("%s@%s:%d", h.User, h.Host, port))
	return marker + "  " + bar + " " + name + "\n" + "    " + bar + " " + sub
}

func (m *Model) listView() string {
	if len(m.file.Hosts) == 0 {
		return styleDim.Render("還沒有任何機器，按 Ctrl+N 新增。")
	}
	if len(m.rows) == 0 {
		return styleDim.Render("沒有符合的機器。")
	}
	avail := m.listHeight()
	var lines []string
	used := 0
	for i := m.offset; i < len(m.rows); i++ {
		r := m.rows[i]
		if avail > 0 && used+r.height() > avail {
			break
		}
		used += r.height()
		lines = append(lines, m.rowView(r, i == m.cursor))
	}
	return strings.Join(lines, "\n")
}
