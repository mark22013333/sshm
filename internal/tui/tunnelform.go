package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/catalog"
	"github.com/mark22013333/sshm/internal/store"
)

var ruleTypes = []string{"L", "R", "D"}

type ruleRow struct {
	typ        int
	bindAddr   textinput.Model
	bindPort   textinput.Model
	targetHost textinput.Model
	targetPort textinput.Model
	extra      store.Extra
}

func (r *ruleRow) kind() string { return ruleTypes[r.typ] }

type tfField int

const (
	tfName tfField = iota
	tfHost
	tfRuleType
	tfBindAddr
	tfBindPort
	tfTargetHost
	tfTargetPort
)

// focusItem 是表單中可取得焦點的一格；rule 為規則索引（非規則欄位為 -1）。
type focusItem struct {
	field tfField
	rule  int
}

// tunnelForm 是新增／編輯 tunnel 的表單。
type tunnelForm struct {
	editing  bool
	original store.Tunnel
	hosts    []store.Host

	name      textinput.Model
	hostQuery textinput.Model
	hostIdx   int
	hostID    string
	rules     []*ruleRow
	focus     int
	err       string
}

func newRuleRow(r store.Rule) *ruleRow {
	row := &ruleRow{extra: r.Extra}
	for i, t := range ruleTypes {
		if t == r.Type {
			row.typ = i
		}
	}
	port := func(p int) string {
		if p <= 0 {
			return ""
		}
		return strconv.Itoa(p)
	}
	row.bindAddr = newTextInput("選填，例 127.0.0.1")
	row.bindAddr.SetValue(r.BindAddress)
	row.bindPort = newTextInput("例 13306")
	row.bindPort.SetValue(port(r.BindPort))
	row.targetHost = newTextInput("例 127.0.0.1")
	row.targetHost.SetValue(r.TargetHost)
	row.targetPort = newTextInput("例 3306")
	row.targetPort.SetValue(port(r.TargetPort))
	for _, ti := range []*textinput.Model{&row.bindAddr, &row.bindPort, &row.targetHost, &row.targetPort} {
		ti.Width = 20
	}
	return row
}

func newTunnelForm(t store.Tunnel, editing bool, hosts []store.Host) *tunnelForm {
	f := &tunnelForm{editing: editing, original: t, hosts: hosts, hostID: t.HostID}
	f.name = newTextInput("例：範例PROD DB")
	f.name.SetValue(t.Name)
	f.hostQuery = newTextInput("輸入名稱、host、user 搜尋")
	for _, r := range t.Rules {
		f.rules = append(f.rules, newRuleRow(r))
	}
	if len(f.rules) == 0 {
		f.rules = append(f.rules, newRuleRow(store.Rule{Type: "L"}))
	}
	f.syncHostIdx()
	f.setFocus(0)
	return f
}

func (f *tunnelForm) matches() []store.Host {
	var out []store.Host
	for _, h := range f.hosts {
		if catalog.Match(h, f.hostQuery.Value()) {
			out = append(out, h)
		}
	}
	return out
}

// syncHostIdx 讓清單游標停在目前選定的機器上。
func (f *tunnelForm) syncHostIdx() {
	for i, h := range f.matches() {
		if h.ID == f.hostID {
			f.hostIdx = i
			return
		}
	}
	f.hostIdx = 0
}

func (f *tunnelForm) selectedHost() (store.Host, bool) {
	for _, h := range f.hosts {
		if h.ID == f.hostID {
			return h, true
		}
	}
	return store.Host{}, false
}

func (f *tunnelForm) items() []focusItem {
	items := []focusItem{{tfName, -1}, {tfHost, -1}}
	for i, r := range f.rules {
		items = append(items, focusItem{tfRuleType, i}, focusItem{tfBindAddr, i}, focusItem{tfBindPort, i})
		if r.kind() != "D" {
			items = append(items, focusItem{tfTargetHost, i}, focusItem{tfTargetPort, i})
		}
	}
	return items
}

func (f *tunnelForm) current() focusItem {
	items := f.items()
	if f.focus >= len(items) {
		f.focus = len(items) - 1
	}
	return items[f.focus]
}

func (f *tunnelForm) input(it focusItem) *textinput.Model {
	switch it.field {
	case tfName:
		return &f.name
	case tfHost:
		return &f.hostQuery
	case tfRuleType:
		return nil
	}
	r := f.rules[it.rule]
	switch it.field {
	case tfBindAddr:
		return &r.bindAddr
	case tfBindPort:
		return &r.bindPort
	case tfTargetHost:
		return &r.targetHost
	default:
		return &r.targetPort
	}
}

func (f *tunnelForm) blurAll() {
	f.name.Blur()
	f.hostQuery.Blur()
	for _, r := range f.rules {
		r.bindAddr.Blur()
		r.bindPort.Blur()
		r.targetHost.Blur()
		r.targetPort.Blur()
	}
}

func (f *tunnelForm) setFocus(i int) tea.Cmd {
	items := f.items()
	f.focus = min(max(i, 0), len(items)-1)
	f.blurAll()
	if ti := f.input(items[f.focus]); ti != nil {
		return ti.Focus()
	}
	return nil
}

func (f *tunnelForm) move(delta int) tea.Cmd {
	n := len(f.items())
	return f.setFocus(((f.focus+delta)%n + n) % n)
}

func (f *tunnelForm) update(msg tea.KeyMsg) (formResult, tea.Cmd) {
	it := f.current()
	switch msg.String() {
	case "esc":
		return formCancel, nil
	case "ctrl+s":
		return f.trySubmit()
	case "tab":
		return formContinue, f.move(1)
	case "shift+tab":
		return formContinue, f.move(-1)
	case "ctrl+a":
		f.rules = append(f.rules, newRuleRow(store.Rule{Type: "L"}))
		items := f.items()
		for i, x := range items {
			if x.rule == len(f.rules)-1 && x.field == tfRuleType {
				return formContinue, f.setFocus(i)
			}
		}
		return formContinue, nil
	case "ctrl+d":
		if it.rule >= 0 {
			f.rules = append(f.rules[:it.rule], f.rules[it.rule+1:]...)
			return formContinue, f.setFocus(min(f.focus, len(f.items())-1))
		}
		return formContinue, nil
	}
	if it.field == tfHost {
		ms := f.matches()
		switch msg.String() {
		case "up":
			if f.hostIdx > 0 {
				f.hostIdx--
			}
			if len(ms) > 0 {
				f.hostID = ms[f.hostIdx].ID
			}
			return formContinue, nil
		case "down":
			if f.hostIdx < len(ms)-1 {
				f.hostIdx++
			}
			if len(ms) > 0 {
				f.hostID = ms[f.hostIdx].ID
			}
			return formContinue, nil
		case "enter":
			if len(ms) > 0 {
				f.hostID = ms[min(f.hostIdx, len(ms)-1)].ID
			}
			return formContinue, f.move(1)
		}
	}
	switch msg.String() {
	case "up":
		return formContinue, f.move(-1)
	case "down", "enter":
		return formContinue, f.move(1)
	}
	if it.field == tfRuleType {
		r := f.rules[it.rule]
		switch msg.String() {
		case "left":
			r.typ = cycle(r.typ, len(ruleTypes), -1)
		case "right", " ":
			r.typ = cycle(r.typ, len(ruleTypes), 1)
		}
		return formContinue, nil
	}
	ti := f.input(it)
	before := ti.Value()
	var cmd tea.Cmd
	*ti, cmd = ti.Update(msg)
	f.err = ""
	if it.field == tfHost && ti.Value() != before {
		f.hostIdx = 0
		if ms := f.matches(); len(ms) > 0 {
			f.hostID = ms[0].ID
		}
	}
	return formContinue, cmd
}

func (f *tunnelForm) trySubmit() (formResult, tea.Cmd) {
	if _, err := f.tunnel(); err != nil {
		f.err = err.Error()
		return formContinue, nil
	}
	return formSubmit, nil
}

func parsePort(label string, n int, s string) (int, error) {
	s = strings.TrimSpace(s)
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("第 %d 條規則的 %s 必須是 1–65535 的數字", n, label)
	}
	return p, nil
}

// tunnel 組出 tunnel 並驗證；編輯時保留 id 與未知欄位。
func (f *tunnelForm) tunnel() (store.Tunnel, error) {
	t := f.original
	t.Name = strings.TrimSpace(f.name.Value())
	t.HostID = f.hostID
	if t.Name == "" {
		return t, errors.New("請填名稱")
	}
	if _, ok := f.selectedHost(); !ok {
		return t, errors.New("請選擇機器")
	}
	t.Rules = nil
	for i, r := range f.rules {
		n := i + 1
		rule := store.Rule{Type: r.kind(), BindAddress: strings.TrimSpace(r.bindAddr.Value()), Extra: r.extra}
		var err error
		if rule.BindPort, err = parsePort("bindPort", n, r.bindPort.Value()); err != nil {
			return t, err
		}
		if rule.Type != "D" {
			rule.TargetHost = strings.TrimSpace(r.targetHost.Value())
			if rule.TargetPort, err = parsePort("targetPort", n, r.targetPort.Value()); err != nil {
				return t, err
			}
		}
		t.Rules = append(t.Rules, rule)
	}
	if err := store.ValidateTunnel(t); err != nil {
		return t, err
	}
	return t, nil
}

func (f *tunnelForm) view() string {
	var b strings.Builder
	title := "新增 tunnel"
	if f.editing {
		title = "編輯 tunnel"
	}
	b.WriteString(styleGroup.Render(title) + "\n\n")
	cur := f.current()
	label := func(it focusItem, text string) string {
		if it == cur {
			return styleLabelFocus.Render(text)
		}
		return styleLabel.Render(text)
	}
	b.WriteString(label(focusItem{tfName, -1}, "名稱 *") + f.name.View() + "\n")
	b.WriteString(label(focusItem{tfHost, -1}, "機器 *") + f.hostQuery.View() + "\n")
	if h, ok := f.selectedHost(); ok {
		b.WriteString(styleLabel.Render("") + styleInfo.Render(fmt.Sprintf("已選：%s（%s@%s）", store.Printable(h.Name), store.Printable(h.User), store.Printable(h.Host))) + "\n")
	} else {
		b.WriteString(styleLabel.Render("") + styleWarn.Render("尚未選擇機器") + "\n")
	}
	if cur.field == tfHost {
		ms := f.matches()
		for i, h := range ms {
			if i >= 6 {
				b.WriteString(styleLabel.Render("") + styleDim.Render(fmt.Sprintf("…還有 %d 台，輸入文字縮小範圍", len(ms)-6)) + "\n")
				break
			}
			marker := "  "
			if i == f.hostIdx {
				marker = styleSelected.Render("› ")
			}
			b.WriteString(styleLabel.Render("") + marker + store.Printable(h.Name) + styleDim.Render("  "+store.Printable(h.User)+"@"+store.Printable(h.Host)) + "\n")
		}
		if len(ms) == 0 {
			b.WriteString(styleLabel.Render("") + styleDim.Render("沒有符合的機器") + "\n")
		}
	}
	b.WriteString("\n")
	for i, r := range f.rules {
		b.WriteString(styleGroup.Render(fmt.Sprintf("規則 %d", i+1)) + "\n")
		b.WriteString(label(focusItem{tfRuleType, i}, "  type") + "‹ " + r.kind() + " ›" + styleDim.Render("  L＝本機轉遠端、R＝遠端轉本機、D＝SOCKS") + "\n")
		b.WriteString(label(focusItem{tfBindAddr, i}, "  bindAddress") + r.bindAddr.View() + "\n")
		b.WriteString(label(focusItem{tfBindPort, i}, "  bindPort *") + r.bindPort.View() + "\n")
		if r.kind() != "D" {
			b.WriteString(label(focusItem{tfTargetHost, i}, "  targetHost *") + r.targetHost.View() + "\n")
			b.WriteString(label(focusItem{tfTargetPort, i}, "  targetPort *") + r.targetPort.View() + "\n")
		}
	}
	b.WriteString("\n")
	if f.err != "" {
		b.WriteString(styleError.Render(f.err) + "\n")
	}
	help := "Tab／↑↓ 切換欄位 · ←→ 切換 type · Ctrl+A 新增規則 · Ctrl+D 刪除目前規則 · Ctrl+S 儲存 · Esc 取消"
	if cur.field == tfHost {
		help = "輸入文字搜尋、↑↓ 選擇機器、Enter 確定 · " + help
	}
	b.WriteString(styleHelp.Render(help))
	return b.String()
}
