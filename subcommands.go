package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/itermimport"
	"github.com/mark22013333/sshm/internal/store"
	"github.com/mark22013333/sshm/internal/transfer"
	"github.com/mark22013333/sshm/internal/tui"
)

func report(stderr io.Writer, err error) int {
	if err != nil {
		fmt.Fprintln(stderr, "sshm：", err)
		return 1
	}
	return 0
}

// exportArgs 解析 export 的旗標與目的檔（旗標可放在檔名前後）。
func exportArgs(args []string) (string, transfer.ExportOptions, error) {
	var opt transfer.ExportOptions
	var files []string
loop:
	for i, a := range args {
		switch {
		case a == "--":
			files = append(files, args[i+1:]...)
			break loop
		case a == "--with-passwords":
			opt.WithPasswords = true
		case a == "--force":
			opt.Force = true
		case strings.HasPrefix(a, "-") && a != "-":
			return "", opt, fmt.Errorf("不認得的選項 %s", a)
		default:
			files = append(files, a)
		}
	}
	if len(files) != 1 {
		return "", opt, errors.New("用法：sshm export [--with-passwords] [--force] <檔案>")
	}
	return files[0], opt, nil
}

func runExport(args []string, out io.Writer) error {
	dest, opt, err := exportArgs(args)
	if err != nil {
		return err
	}
	path, err := store.HostsPath()
	if err != nil {
		return err
	}
	res, err := transfer.Export(path, dest, opt)
	if err != nil {
		return err
	}
	note := "（不含密碼）"
	if opt.WithPasswords {
		note = "（含密碼，請妥善保管）"
	}
	fmt.Fprintf(out, "已匯出 %d 台機器到 %s%s\n", res.Hosts, dest, note)
	if len(res.SkippedTunnels) > 0 {
		fmt.Fprintf(out, "以下 %d 條 tunnel 指向已不存在的機器或設定不合法，未匯出：%s\n",
			len(res.SkippedTunnels), strings.Join(res.SkippedTunnels, "、"))
	}
	return nil
}

func runImport(args []string, in io.Reader, out io.Writer) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return errors.New("用法：sshm import <檔案>")
	}
	path, err := store.HostsPath()
	if err != nil {
		return err
	}
	return transfer.RunImport(path, args[0], in, out)
}

// plistArg 解析 import-iterm 的 --plist；預設為 ~/Library/Preferences/com.googlecode.iterm2.plist。
func plistArg(args []string) (string, error) {
	switch {
	case len(args) == 0:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, itermimport.DefaultPlistPath), nil
	case len(args) == 2 && args[0] == "--plist":
		return args[1], nil
	case len(args) == 1 && strings.HasPrefix(args[0], "--plist="):
		return strings.TrimPrefix(args[0], "--plist="), nil
	}
	return "", errors.New("用法：sshm import-iterm [--plist <路徑>]")
}

func runImportITerm(args []string, out io.Writer) error {
	plist, err := plistArg(args)
	if err != nil {
		return err
	}
	path, err := store.HostsPath()
	if err != nil {
		return err
	}
	cur, err := store.Open(path)
	if err != nil {
		return err
	}
	bookmarks, err := itermimport.ReadBookmarks(connect.ExecRunner, plist)
	if err != nil {
		return err
	}
	preview := tui.NewImportPreview(itermimport.Build(bookmarks, cur))
	if _, err := tea.NewProgram(preview, tea.WithAltScreen()).Run(); err != nil {
		return err
	}
	if !preview.Confirmed() {
		fmt.Fprintln(out, "已取消，未匯入任何機器。")
		return nil
	}
	return applyITerm(path, preview.Candidates(), out)
}

// applyITerm 在 store 的鎖內寫入勾選的候選；一台都沒勾時不寫檔。
func applyITerm(path string, cands []itermimport.Candidate, out io.Writer) error {
	if itermimport.CountSelected(cands) == 0 {
		fmt.Fprintln(out, "未選任何機器，未寫入任何資料。")
		return nil
	}
	var res itermimport.Result
	if _, err := store.Update(path, func(f *store.File) error {
		var err error
		res, err = itermimport.Apply(f, cands)
		return err
	}); err != nil {
		return err
	}
	fmt.Fprintf(out, "已匯入 %d 台，略過 %d 台。\n", res.Added, res.Skipped)
	return nil
}
