package zshwidget

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptPassesZshSyntaxCheck(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("系統沒有 zsh")
	}
	path := filepath.Join(t.TempDir(), "widget.zsh")
	if err := os.WriteFile(path, []byte(Script), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(zsh, "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("zsh -n 失敗：%v\n%s", err, out)
	}
	// 正對照：語法錯誤的片段必須被 zsh -n 抓到
	bad := filepath.Join(t.TempDir(), "bad.zsh")
	if err := os.WriteFile(bad, []byte("f() {\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(zsh, "-n", bad).Run(); err == nil {
		t.Fatal("zsh -n 應該要拒絕未閉合的函式")
	}
	if strings.Contains(Script, "BUFFER=") || strings.Contains(Script, "LBUFFER=") {
		t.Error("widget 不應改動提示列內容")
	}
}
