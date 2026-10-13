package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/agentaccess"
	"github.com/mark22013333/sshm/internal/store"
)

// agentMark 是清單中已開通給 AI agent 的標記。
const agentMark = "🤖"

type agentEnableDoneMsg struct {
	res agentaccess.EnableResult
	err error
}

type agentRemoteDoneMsg struct{ err error }

type agentLocalDoneMsg struct {
	remoteFailed bool
	err          error
}

// toggleAgent 是 Ctrl+O：未開通 → 開通確認；已開通 → 停用確認。
func (m *Model) toggleAgent() (tea.Model, tea.Cmd) {
	h, ok := m.currentHost()
	if !ok {
		m.setStatus("請先選一台機器", true)
		return m, nil
	}
	mgr := m.opts.Agent
	if mgr == nil {
		m.setStatus("無法使用 AI agent 開通（找不到家目錄或 expect 腳本）", true)
		return m, nil
	}
	if h.Agent != nil {
		plan, err := mgr.PrepareDisable(m.file, h.ID)
		if err != nil {
			m.setStatus("無法停用："+err.Error(), true)
			return m, nil
		}
		m.agentDisable, m.mode = plan, modeAgentConfirm
		return m, nil
	}
	plan, err := mgr.PrepareEnable(context.Background(), m.file, h.ID, "", "")
	if err != nil {
		m.setStatus("無法開通："+err.Error(), true)
		return m, nil
	}
	m.agentEnable, m.mode = plan, modeAgentConfirm
	return m, nil
}

func isYes(msg tea.KeyMsg) bool { return msg.String() == "y" || msg.String() == "Y" }

func (m *Model) updateAgentConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	mgr := m.opts.Agent
	enable, disable := m.agentEnable, m.agentDisable
	if !isYes(msg) {
		mgr.Discard(enable)
		m.clearAgent()
		m.setStatus("已取消，未做任何變更", false)
		return m, nil
	}
	if enable != nil {
		m.mode, m.busyMsg = modeBusy, "正在登入「"+store.Printable(enable.Host.Name)+"」安裝公鑰…"
		return m, func() tea.Msg {
			res, err := mgr.Enable(context.Background(), enable)
			return agentEnableDoneMsg{res: res, err: err}
		}
	}
	m.mode, m.busyMsg = modeBusy, "正在登入「"+store.Printable(disable.Host.Name)+"」移除公鑰…"
	return m, func() tea.Msg {
		_, err := mgr.DisableRemote(context.Background(), disable)
		return agentRemoteDoneMsg{err: err}
	}
}

// updateAgentLocalOnly 遠端移除失敗後，詢問是否只清本機。
func (m *Model) updateAgentLocalOnly(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !isYes(msg) {
		m.clearAgent()
		m.setStatus("已取消，本機與遠端都維持原狀", false)
		return m, nil
	}
	return m, m.disableLocal(true)
}

func (m *Model) disableLocal(remoteFailed bool) tea.Cmd {
	mgr, plan := m.opts.Agent, m.agentDisable
	m.mode, m.busyMsg = modeBusy, "正在清除本機的別名與金鑰…"
	return func() tea.Msg {
		_, err := mgr.DisableLocal(plan)
		return agentLocalDoneMsg{remoteFailed: remoteFailed, err: err}
	}
}

func (m *Model) clearAgent() {
	m.mode, m.agentEnable, m.agentDisable, m.agentRemoteErr, m.busyMsg = modeList, nil, nil, "", ""
}

// reloadAfterAgent 重讀 hosts.json（開通／停用由 Manager 在鎖內寫入）。
func (m *Model) reloadAfterAgent() {
	if f, err := store.Open(m.opts.Path); err == nil {
		m.file = f
	}
	m.rebuild("")
}

func (m *Model) applyAgentMsg(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case agentEnableDoneMsg:
		name := store.Printable(m.agentEnable.Host.Name)
		m.clearAgent()
		m.reloadAfterAgent()
		if msg.err != nil {
			m.setStatus("開通「"+name+"」失敗："+oneLine(msg.err.Error()), true)
			return nil
		}
		note := "已開通「" + name + "」，AI agent 可用 ssh " + msg.res.Alias + " 連線"
		if len(msg.res.Warnings) > 0 {
			m.setStatus(note+"。"+oneLine(strings.Join(msg.res.Warnings, " ")), true)
			return nil
		}
		m.setStatus(note, false)
	case agentRemoteDoneMsg:
		if msg.err != nil {
			m.mode, m.agentRemoteErr = modeAgentLocalOnly, oneLine(msg.err.Error())
			return nil
		}
		return m.disableLocal(false)
	case agentLocalDoneMsg:
		plan := m.agentDisable
		m.clearAgent()
		m.reloadAfterAgent()
		if msg.err != nil {
			m.setStatus("清除本機狀態失敗："+msg.err.Error(), true)
			return nil
		}
		note := "已停用「" + store.Printable(plan.Host.Name) + "」的 AI agent 存取"
		if msg.remoteFailed {
			m.setStatus(note+"（只清本機；遠端公鑰仍在，行尾為 "+agentaccess.KeyComment(plan.Alias)+"）", true)
			return nil
		}
		m.setStatus(note, false)
	}
	return nil
}

// oneLine 把多行訊息壓成一行，適合狀態列。
func oneLine(s string) string {
	return strings.Join(strings.Fields(store.Printable(strings.ReplaceAll(s, "\n", " "))), " ")
}

// agentView 是開通／停用的確認畫面。
func (m *Model) agentView() string {
	var b strings.Builder
	var title string
	var lines []string
	switch {
	case m.mode == modeAgentLocalOnly:
		p := m.agentDisable
		title = "無法移除遠端公鑰"
		lines = []string{
			"原因：" + m.agentRemoteErr,
			fmt.Sprintf("遠端 %s 的 ~/.ssh/authorized_keys 仍有行尾為 %s 的公鑰，之後請自行刪除該行。", p.User, agentaccess.KeyComment(p.Alias)),
		}
		b.WriteString(styleWarn.Render(title) + "\n\n")
		for _, l := range lines {
			b.WriteString("  " + l + "\n")
		}
		b.WriteString("\n" + styleWarn.Render("是否只清除本機（別名區塊、金鑰與開通狀態）？按 y 確認，其他鍵取消") + "\n")
		return b.String()
	case m.agentEnable != nil:
		title, lines = "開通給 AI agent", m.agentEnable.Summary()
	case m.agentDisable != nil:
		title, lines = "停用 AI agent 存取", m.agentDisable.Summary(m.opts.Agent.Paths)
	}
	b.WriteString(styleTabActive.Render(title) + "\n\n")
	for _, l := range lines {
		b.WriteString("  " + store.Printable(l) + "\n")
	}
	b.WriteString("\n" + styleWarn.Render("按 y 確認執行，其他鍵取消") + "\n")
	return b.String()
}

// agentRowMark 是機器第二行的開通標記；資料不合法而沒寫進 ssh 設定時明示別名已停用。
func (m *Model) agentRowMark(h *store.Host) string {
	if h.Agent == nil {
		return ""
	}
	if mgr := m.opts.Agent; mgr != nil {
		if problem := mgr.Paths.ConfProblem(*h); problem != "" {
			return "  " + styleWarn.Render(agentMark+" "+h.Agent.Alias+"（別名已停用："+oneLine(problem)+"）")
		}
	}
	if agentaccess.Relocated(*h) {
		return "  " + styleWarn.Render(agentMark+" "+h.Agent.Alias+"（位置已變更，需重新 enable）")
	}
	return "  " + styleInfo.Render(agentMark+" "+h.Agent.Alias)
}

// syncAgentAfterSave 存檔後重新產生別名設定；已開通機器改了連線欄位時提醒新位置尚未安裝公鑰。
func (m *Model) syncAgentAfterSave(orig store.Host, editing bool, id string) string {
	mgr := m.opts.Agent
	if mgr == nil || !agentaccess.HasAgents(m.file) {
		return ""
	}
	if _, _, err := mgr.Paths.SyncConf(m.file); err != nil {
		return "；更新 AI agent 別名設定失敗：" + oneLine(err.Error())
	}
	i := m.file.HostIndex(id)
	if !editing || i < 0 || m.file.Hosts[i].Agent == nil {
		return ""
	}
	cur := m.file.Hosts[i]
	if problem := mgr.Paths.ConfProblem(cur); problem != "" {
		return "；AI agent 別名 " + cur.Agent.Alias + " 已停用（" + oneLine(problem) + "），修正後會自動恢復"
	}
	if cur.Host != orig.Host || cur.Port != orig.Port || cur.User != orig.User || agentaccess.Relocated(cur) {
		return "；" + agentaccess.RelocatedNotice(cur)
	}
	return ""
}
