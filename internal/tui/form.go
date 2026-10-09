package tui

import (
	"errors"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
)

type fieldID int

const (
	fieldName fieldID = iota
	fieldGroup
	fieldHost
	fieldPort
	fieldUser
	fieldAuth
	fieldSecret
	fieldColor
	fieldExtraArgs
	fieldCustom
	fieldCount
)

var authChoices = []string{store.AuthPassword, store.AuthKey, store.AuthNone}

var authLabels = map[string]string{
	store.AuthPassword: "password（expect 自動輸入密碼）",
	store.AuthKey:      "key（指定金鑰檔）",
	store.AuthNone:     "none（交給 ssh-agent／~/.ssh/config）",
}

// colorChoices 第一個空字串代表「不指定（沿用群組色）」。
var colorChoices = append([]string{""}, store.Colors...)

// hostForm 是新增／編輯機器的表單。
type hostForm struct {
	editing  bool
	original store.Host
	groups   []string

	inputs   map[fieldID]*textinput.Model
	password textinput.Model
	identity textinput.Model
	authIdx  int
	colorIdx int
	focus    fieldID
	groupIdx int
	err      string
}

func newTextInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	ti.CharLimit = 1024
	ti.Width = 48
	return ti
}

func newHostForm(h store.Host, editing bool, groups []string) *hostForm {
	f := &hostForm{editing: editing, original: h, groups: groups, groupIdx: -1}
	mk := func(id fieldID, placeholder, value string) {
		ti := newTextInput(placeholder)
		ti.SetValue(value)
		f.inputs[id] = &ti
	}
	f.inputs = map[fieldID]*textinput.Model{}
	port := ""
	if h.Port > 0 {
		port = strconv.Itoa(h.Port)
	}
	mk(fieldName, "例：範例客戶-PROD-VM", h.Name)
	mk(fieldGroup, "可留空；Ctrl+G 選既有群組", h.Group)
	mk(fieldHost, "例：10.0.0.24", h.Host)
	mk(fieldPort, "22", port)
	mk(fieldUser, "例：helpdesk", h.User)
	mk(fieldExtraArgs, "例：-J jump -o ServerAliveInterval=30", h.ExtraArgs)
	mk(fieldCustom, "非空時整個取代連線指令", h.CustomCommand)

	f.password = newTextInput("密碼")
	f.password.EchoMode = textinput.EchoPassword
	f.password.SetValue(h.Password)
	f.identity = newTextInput("例：~/.ssh/id_ed25519")
	f.identity.SetValue(h.IdentityFile)

	f.authIdx = 0
	for i, a := range authChoices {
		if a == h.Auth {
			f.authIdx = i
		}
	}
	if h.Auth == "" && editing {
		f.authIdx = 2
	}
	c := store.NormalizeColor(h.Color)
	for i, v := range colorChoices {
		if v == c {
			f.colorIdx = i
		}
	}
	f.setFocus(fieldName)
	return f
}

func (f *hostForm) auth() string { return authChoices[f.authIdx] }

func (f *hostForm) secretInput() *textinput.Model {
	if f.auth() == store.AuthKey {
		return &f.identity
	}
	return &f.password
}

func (f *hostForm) input(id fieldID) *textinput.Model {
	if id == fieldSecret {
		if f.auth() == store.AuthNone {
			return nil
		}
		return f.secretInput()
	}
	return f.inputs[id]
}

func (f *hostForm) skippable(id fieldID) bool {
	return id == fieldSecret && f.auth() == store.AuthNone
}

func (f *hostForm) setFocus(id fieldID) tea.Cmd {
	for _, ti := range f.inputs {
		ti.Blur()
	}
	f.password.Blur()
	f.identity.Blur()
	f.focus = id
	if ti := f.input(id); ti != nil {
		return ti.Focus()
	}
	return nil
}

func (f *hostForm) move(delta int) tea.Cmd {
	id := f.focus
	for {
		id = fieldID((int(id) + delta + int(fieldCount)) % int(fieldCount))
		if !f.skippable(id) {
			return f.setFocus(id)
		}
	}
}

func cycle(i, n, delta int) int { return ((i+delta)%n + n) % n }

// formResult 是表單處理按鍵後的結果。
type formResult int

const (
	formContinue formResult = iota
	formSubmit
	formCancel
)

func (f *hostForm) update(msg tea.KeyMsg) (formResult, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return formCancel, nil
	case "ctrl+s":
		return f.trySubmit()
	case "tab", "down":
		return formContinue, f.move(1)
	case "shift+tab", "up":
		return formContinue, f.move(-1)
	case "enter":
		if f.focus == fieldCustom {
			return f.trySubmit()
		}
		return formContinue, f.move(1)
	}
	switch f.focus {
	case fieldAuth:
		switch msg.String() {
		case "left":
			f.authIdx = cycle(f.authIdx, len(authChoices), -1)
		case "right", " ":
			f.authIdx = cycle(f.authIdx, len(authChoices), 1)
		}
		return formContinue, nil
	case fieldColor:
		switch msg.String() {
		case "left":
			f.colorIdx = cycle(f.colorIdx, len(colorChoices), -1)
		case "right", " ":
			f.colorIdx = cycle(f.colorIdx, len(colorChoices), 1)
		}
		return formContinue, nil
	case fieldGroup:
		if msg.String() == "ctrl+g" && len(f.groups) > 0 {
			f.groupIdx = cycle(f.groupIdx, len(f.groups), 1)
			f.inputs[fieldGroup].SetValue(f.groups[f.groupIdx])
			f.inputs[fieldGroup].CursorEnd()
			return formContinue, nil
		}
	}
	if ti := f.input(f.focus); ti != nil {
		var cmd tea.Cmd
		*ti, cmd = ti.Update(msg)
		f.err = ""
		return formContinue, cmd
	}
	return formContinue, nil
}

func (f *hostForm) trySubmit() (formResult, tea.Cmd) {
	if _, err := f.host(); err != nil {
		f.err = err.Error()
		return formContinue, nil
	}
	return formSubmit, nil
}

// host 驗證欄位並組出機器資料；編輯時保留 id 與未知欄位。
func (f *hostForm) host() (store.Host, error) {
	val := func(id fieldID) string { return strings.TrimSpace(f.inputs[id].Value()) }
	h := f.original
	h.Name = val(fieldName)
	h.Group = val(fieldGroup)
	h.Host = val(fieldHost)
	h.User = val(fieldUser)
	h.Auth = f.auth()
	h.Password = f.password.Value()
	h.IdentityFile = strings.TrimSpace(f.identity.Value())
	h.Color = colorChoices[f.colorIdx]
	h.ExtraArgs = val(fieldExtraArgs)
	h.CustomCommand = val(fieldCustom)
	// 改用其他認證方式時不留下明碼密碼
	if h.Auth != store.AuthPassword {
		h.Password = ""
	}

	h.Port = 22
	if p := val(fieldPort); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return h, errors.New("port 必須是 1–65535 的數字")
		}
		h.Port = n
	}
	if err := store.ValidateHost(h); err != nil {
		return h, err
	}
	if _, err := connect.SplitArgs(h.ExtraArgs); err != nil {
		return h, err
	}
	return h, nil
}

var fieldLabels = map[fieldID]string{
	fieldName:      "名稱 *",
	fieldGroup:     "群組",
	fieldHost:      "host *",
	fieldPort:      "port",
	fieldUser:      "user *",
	fieldAuth:      "認證方式",
	fieldColor:     "顏色",
	fieldExtraArgs: "額外 ssh 參數",
	fieldCustom:    "自訂指令",
}

func (f *hostForm) label(id fieldID) string {
	if id == fieldSecret {
		if f.auth() == store.AuthKey {
			return "金鑰檔"
		}
		return "密碼"
	}
	return fieldLabels[id]
}

func (f *hostForm) view() string {
	var b strings.Builder
	title := "新增機器"
	if f.editing {
		title = "編輯機器"
	}
	b.WriteString(styleGroup.Render(title) + "\n\n")
	for id := fieldID(0); id < fieldCount; id++ {
		lbl := styleLabel.Render(f.label(id))
		if id == f.focus {
			lbl = styleLabelFocus.Render(f.label(id))
		}
		var value string
		switch id {
		case fieldAuth:
			value = "‹ " + authLabels[f.auth()] + " ›"
		case fieldColor:
			c := colorChoices[f.colorIdx]
			if c == "" {
				value = "‹ 不指定（沿用群組色） ›"
			} else {
				value = "‹ " + store.ColorEmoji(c) + " " + c + " ›"
			}
		case fieldSecret:
			if f.auth() == store.AuthNone {
				value = styleDim.Render("（不需要）")
			} else {
				value = f.secretInput().View()
			}
		default:
			value = f.inputs[id].View()
		}
		b.WriteString(lbl + value + "\n")
		if id == fieldGroup && f.focus == fieldGroup && len(f.groups) > 0 {
			b.WriteString(styleLabel.Render("") + styleDim.Render("既有："+strings.Join(f.groups, "、")) + "\n")
		}
	}
	b.WriteString("\n")
	if f.err != "" {
		b.WriteString(styleError.Render(f.err) + "\n")
	}
	help := "Tab/↑↓ 切換欄位 · ←→ 切換選項 · Ctrl+S 儲存 · Esc 取消"
	if f.focus == fieldGroup {
		help = "Ctrl+G 循環選既有群組 · " + help
	}
	b.WriteString(styleHelp.Render(help))
	return b.String()
}
