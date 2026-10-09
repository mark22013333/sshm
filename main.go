// sshm 是 macOS 上的 SSH 機器管理 TUI。
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
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
  sshm import-iterm [--plist <路徑>]
                       從 iTerm2 匯入以 login.exp 登入的 profile（先預覽再寫入）
  sshm export [--with-passwords] [--force] <檔案>
                       匯出 JSON（預設不含密碼；目的檔已存在時需 --force）
  sshm import <檔案>   匯入 JSON（同 id 且內容不同的項目先列出差異再確認）
  sshm --version       顯示版本
`

// version 由 GoReleaser 以 -ldflags "-X main.version=…" 寫入；go install 時改讀模組版本。
var version = ""

func versionString() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// command 是解析命令列後的結果。
type command struct {
	name  string // tui、add、help、zsh-widget、import-iterm、export、import
	query string
	args  []string // 子指令後的參數
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
	case "-v", "--version", "version":
		return command{name: "version"}
	case "zsh-widget", "add", "import-iterm", "export", "import":
		return command{name: args[0], args: args[1:]}
	}
	return command{name: "tui", query: strings.Join(args, " ")}
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := parseArgs(args)
	switch cmd.name {
	case "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version":
		fmt.Fprintln(stdout, "sshm", versionString())
		return 0
	case "zsh-widget":
		fmt.Fprint(stdout, zshwidget.Script)
		return 0
	case "import-iterm":
		return report(stderr, runImportITerm(cmd.args, stdout))
	case "export":
		return report(stderr, runExport(cmd.args, stdout))
	case "import":
		return report(stderr, runImport(cmd.args, os.Stdin, stdout))
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
		StatePath: filepath.Join(filepath.Dir(path), "state.json"),
	})
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		return err
	}
	if argv := m.Result().ExecArgv; len(argv) > 0 {
		return launch.Exec(argv)
	}
	return nil
}
