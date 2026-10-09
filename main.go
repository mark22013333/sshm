// sshm 是 macOS 上的 SSH 機器管理 TUI。
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/launch"
	"github.com/mark22013333/sshm/internal/store"
	"github.com/mark22013333/sshm/internal/tui"
	"github.com/mark22013333/sshm/internal/zshwidget"
)

const usage = `用法：
  sshm                 開啟機器清單
  sshm <搜尋字>        開啟機器清單並預填搜尋字
  sshm -- <搜尋字>     搜尋字與子指令同名時使用（例：sshm -- add）
  sshm add             直接開「新增機器」表單
  sshm zsh-widget      印出 Ctrl+O 的 zsh widget 設定（請自行貼進 ~/.zshrc）
  sshm import-iterm    從 iTerm2 匯入（尚未實作）
  sshm export [--with-passwords] <檔案>   匯出 JSON（尚未實作）
  sshm import <檔案>   匯入 JSON（尚未實作）
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// command 是解析命令列後的結果。
type command struct {
	name  string // tui、add、help、zsh-widget，或尚未實作的子指令名稱
	query string
}

func parseArgs(args []string) command {
	if len(args) == 0 {
		return command{name: "tui"}
	}
	switch args[0] {
	case "--":
		// sshm -- add：-- 之後一律當搜尋字
		return command{name: "tui", query: strings.Join(args[1:], " ")}
	case "-h", "--help", "help":
		return command{name: "help"}
	case "zsh-widget", "add", "import-iterm", "export", "import":
		return command{name: args[0]}
	}
	return command{name: "tui", query: strings.Join(args, " ")}
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := parseArgs(args)
	switch cmd.name {
	case "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "zsh-widget":
		fmt.Fprint(stdout, zshwidget.Script)
		return 0
	case "import-iterm", "export", "import":
		fmt.Fprintf(stderr, "sshm %s：尚未實作\n", cmd.name)
		return 1
	}
	if err := runTUI(cmd.query, cmd.name == "add"); err != nil {
		fmt.Fprintln(stderr, "sshm：", err)
		return 1
	}
	return 0
}

func runTUI(query string, startAdd bool) error {
	path, err := store.HostsPath()
	if err != nil {
		return err
	}
	file, err := store.Open(path)
	if err != nil {
		return err
	}
	script, err := connect.EnsureLoginScript(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("寫出 expect 登入腳本失敗：%w", err)
	}
	orca, inOrca := connect.DetectOrca(os.Getenv)
	m := tui.New(tui.Options{
		Path: path, File: file, ScriptPath: script,
		Query: query, StartAdd: startAdd,
		Orca: orca, InOrca: inOrca,
	})
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		return err
	}
	if argv := m.Result().ExecArgv; len(argv) > 0 {
		return launch.Exec(argv)
	}
	return nil
}
