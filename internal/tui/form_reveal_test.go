package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/store"
)

func TestFormCtrlRTogglesPasswordReveal(t *testing.T) {
	f := newHostForm(store.Host{Name: "範例", Host: "10.0.0.24", User: "helpdesk", Auth: store.AuthPassword, Password: "s3cret-pw"}, true, nil)
	ctrlR := tea.KeyMsg{Type: tea.KeyCtrlR}

	if strings.Contains(f.view(), "s3cret-pw") {
		t.Fatal("密碼預設應遮罩")
	}
	if !strings.Contains(f.view(), "Ctrl+R 顯示密碼") {
		t.Fatal("password 認證時說明列應提示 Ctrl+R")
	}

	f.update(ctrlR)
	if !strings.Contains(f.view(), "s3cret-pw") {
		t.Fatal("按 Ctrl+R 後應顯示明碼")
	}
	if !strings.Contains(f.view(), "Ctrl+R 隱藏密碼") {
		t.Fatal("顯示中時說明列應改為隱藏")
	}

	f.update(ctrlR)
	if strings.Contains(f.view(), "s3cret-pw") {
		t.Fatal("再按一次 Ctrl+R 應恢復遮罩")
	}

	// 熱鍵不得把字元打進目前聚焦的欄位
	if got := f.inputs[fieldName].Value(); got != "範例" {
		t.Fatalf("名稱欄被改動：%q", got)
	}
}

func TestFormCtrlRIgnoredWithoutPasswordAuth(t *testing.T) {
	f := newHostForm(store.Host{Name: "範例", Host: "10.0.0.24", User: "helpdesk", Auth: store.AuthKey, IdentityFile: "~/.ssh/id_ed25519"}, true, nil)
	f.update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if strings.Contains(f.view(), "Ctrl+R") {
		t.Fatal("非 password 認證時不應出現 Ctrl+R 提示")
	}
	if f.password.EchoMode != textinput.EchoPassword {
		t.Fatal("非 password 認證時不應切換顯示狀態")
	}
}
