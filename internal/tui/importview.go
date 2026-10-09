package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/itermimport"
	"github.com/mark22013333/sshm/internal/store"
)

// ImportPreview 是 iTerm2 匯入的預覽畫面：逐台勾選、多 tag 時挑群組，確認後由呼叫端寫入。
type ImportPreview struct {
	plan      itermimport.Plan
	cursor    int
	offset    int
	height    int
	confirmed bool
}

// NewImportPreview 建立預覽畫面。
func NewImportPreview(plan itermimport.Plan) *ImportPreview {
	return &ImportPreview{plan: plan}
}

// Confirmed 回傳使用者是否按了確認。
func (m *ImportPreview) Confirmed() bool { return m.confirmed }

// Candidates 回傳（可能被使用者調整過的）候選。
func (m *ImportPreview) Candidates() []itermimport.Candidate { return m.plan.Candidates }

func (m *ImportPreview) Init() tea.Cmd { return nil }

func (m *ImportPreview) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.ensureVisible()
	case tea.KeyMsg:
		cands := m.plan.Candidates
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			return m, tea.Quit
		case "enter":
			m.confirmed = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(cands)-1 {
				m.cursor++
			}
		case " ", "x":
			if c := m.current(); c != nil && c.Status != itermimport.StatusInvalid {
				c.Selected = !c.Selected
			}
		case "left":
			if c := m.current(); c != nil {
				c.CycleGroup(-1)
			}
		case "right":
			if c := m.current(); c != nil {
				c.CycleGroup(1)
			}
		case "a":
			m.setAll(true)
		case "n":
			m.setAll(false)
		}
		m.ensureVisible()
	}
	return m, nil
}

func (m *ImportPreview) current() *itermimport.Candidate {
	if m.cursor < 0 || m.cursor >= len(m.plan.Candidates) {
		return nil
	}
	return &m.plan.Candidates[m.cursor]
}

// setAll 全選時不含「重複」與無法匯入的列。
func (m *ImportPreview) setAll(on bool) {
	for i := range m.plan.Candidates {
		c := &m.plan.Candidates[i]
		if c.Status == itermimport.StatusOK || !on {
			if c.Status != itermimport.StatusInvalid {
				c.Selected = on
			}
		}
	}
}

func (m *ImportPreview) listHeight() int {
	if m.height <= 0 {
		return 0
	}
	return max(3, m.height-5)
}

func (m *ImportPreview) ensureVisible() {
	h := m.listHeight()
	if h == 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
}

func (m *ImportPreview) selectedCount() int { return itermimport.CountSelected(m.plan.Candidates) }

func (m *ImportPreview) rowView(i int, c itermimport.Candidate) string {
	marker := "  "
	if i == m.cursor {
		marker = styleSelected.Render("› ")
	}
	box := "[ ]"
	if c.Selected {
		box = "[x]"
	}
	// 名稱、host 等來自外部設定檔，顯示前一律過濾控制字元
	p := store.Printable
	if c.Status == itermimport.StatusInvalid {
		return marker + styleDim.Render("[-] "+p(c.Name)+" — 無法匯入："+p(c.Reason))
	}
	text := fmt.Sprintf("%s %s  %s", box, p(c.Host.Name), styleDim.Render(fmt.Sprintf("%s@%s:%d", p(c.Host.User), p(c.Host.Host), c.Host.Port)))
	switch {
	case len(c.Tags) > 1:
		text += fmt.Sprintf("  群組：‹ %s › (%d/%d)", p(c.Group()), c.TagIndex+1, len(c.Tags))
	case len(c.Tags) == 1:
		text += "  群組：" + p(c.Group())
	default:
		text += styleDim.Render("  未分組")
	}
	if c.Status == itermimport.StatusDuplicate {
		text += styleWarn.Render("  重複：" + p(c.Reason))
	}
	if c.Note != "" {
		text += styleDim.Render("  （" + c.Note + "）")
	}
	if i == m.cursor {
		return marker + styleSelected.Render(text)
	}
	return marker + text
}

func (m *ImportPreview) View() string {
	var b strings.Builder
	b.WriteString(styleGroup.Render("從 iTerm2 匯入") + "\n")
	b.WriteString(styleDim.Render(fmt.Sprintf("找到 %d 台以 login.exp 登入的機器，另略過 %d 個其他 profile。", len(m.plan.Candidates), m.plan.Skipped)) + "\n")
	if len(m.plan.Candidates) == 0 {
		b.WriteString("\n沒有可匯入的機器。按 Esc 離開。")
		return b.String()
	}
	h := m.listHeight()
	var lines []string
	for i := m.offset; i < len(m.plan.Candidates); i++ {
		if h > 0 && len(lines) >= h {
			break
		}
		lines = append(lines, m.rowView(i, m.plan.Candidates[i]))
	}
	b.WriteString(strings.Join(lines, "\n") + "\n")
	b.WriteString(styleInfo.Render(fmt.Sprintf("已勾選 %d 台", m.selectedCount())) + "\n")
	b.WriteString(styleHelp.Render("↑↓ 移動 · 空白 勾選 · ←→ 切換群組（多 tag）· a 全選 · n 全不選 · Enter 匯入 · Esc 取消"))
	return b.String()
}
