package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/mark22013333/sshm/internal/agentaccess"
	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
	"github.com/mattn/go-isatty"
)

const agentUsage = `用法：
  sshm agent list [--json]
                       列出已開通給 AI agent 的機器（名稱、別名、host、port、user、群組；不含密碼）
  sshm agent enable <機器名稱> [--alias <別名>] [--user <帳號>]
                       用 sshm 存的密碼登入，安裝專用金鑰並建立 ssh 別名（需在終端機確認）
  sshm agent disable <機器名稱>
                       移除遠端公鑰、別名與金鑰（需在終端機確認）
  sshm agent guide     印出給 AI agent 指引檔（CLAUDE.md／AGENTS.md）的說明

AI agent 的用法：先 sshm agent list --json 找機器，再 ssh <別名> 連線；不需要、也不可以要求密碼。
enable／disable 一定要在互動終端機按 y 確認，沒有跳過確認的選項。
`

// isTerminal 判斷 stdin 與 stdout 是否都是終端機；測試可替換。
var isTerminal = func() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

// newAgentManager 建立開通用的 Manager；測試可替換成假的遠端。
var newAgentManager = func(hostsPath string) (*agentaccess.Manager, error) {
	script, err := connect.EnsureLoginScript(filepath.Dir(hostsPath))
	if err != nil {
		return nil, fmt.Errorf("寫出 expect 登入腳本失敗：%w", err)
	}
	return agentaccess.NewManager(hostsPath, script)
}

var errNeedTTY = errors.New("enable／disable 會登入遠端並修改 ~/.ssh，必須在互動終端機執行並按 y 確認；不支援管線或排程等非互動執行，也沒有跳過確認的選項")

func runAgent(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, agentUsage)
		return nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(out, agentUsage)
		return nil
	case "guide":
		fmt.Fprint(out, agentaccess.Guide)
		return nil
	case "list":
		return runAgentList(args[1:], out)
	case "enable":
		return runAgentEnable(args[1:], in, out)
	case "disable":
		return runAgentDisable(args[1:], in, out)
	}
	return fmt.Errorf("不認得的 agent 子指令 %s\n%s", args[0], agentUsage)
}

func runAgentList(args []string, out io.Writer) error {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		default:
			return errors.New("用法：sshm agent list [--json]")
		}
	}
	path, err := store.HostsPath()
	if err != nil {
		return err
	}
	f, err := store.Open(path)
	if err != nil {
		return err
	}
	entries := agentaccess.List(f)
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	if len(entries) == 0 {
		fmt.Fprintln(out, "還沒有開通給 AI agent 的機器。用 sshm agent enable <機器名稱> 開通。")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "名稱\t別名\thost\tuser\t群組")
	for _, e := range entries {
		host := e.Host
		if e.Port != 22 {
			host = fmt.Sprintf("%s:%d", e.Host, e.Port)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", store.Printable(e.Name), e.Alias, store.Printable(host), store.Printable(e.User), store.Printable(e.Group))
	}
	return tw.Flush()
}

// agentArgs 解析 enable／disable 的機器名稱與選項（選項可放在名稱前後）。
func agentArgs(args []string, allowOpts bool) (name, alias, user string, err error) {
	var names []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		key, val, hasVal := strings.Cut(a, "=")
		switch {
		case a == "--":
			names = append(names, args[i+1:]...)
			i = len(args)
		case allowOpts && (key == "--alias" || key == "--user"):
			if !hasVal {
				if i+1 >= len(args) {
					return "", "", "", fmt.Errorf("%s 需要一個值", key)
				}
				i++
				val = args[i]
			}
			if key == "--alias" {
				alias = val
			} else {
				user = val
			}
		case strings.HasPrefix(a, "-"):
			return "", "", "", fmt.Errorf("不認得的選項 %s（enable／disable 沒有跳過確認的選項）", a)
		default:
			names = append(names, a)
		}
	}
	if len(names) != 1 {
		return "", "", "", errors.New("請指定一台機器的名稱（名稱含空白時請加引號）")
	}
	return names[0], alias, user, nil
}

// confirm 讀一行，只有 y／Y 算同意；EOF 視為取消。
func confirm(r *bufio.Reader, out io.Writer, question string) bool {
	fmt.Fprint(out, question+"輸入 y 確認，其他鍵取消：")
	line, _ := r.ReadString('\n')
	ans := strings.TrimSpace(line)
	return ans == "y" || ans == "Y"
}

func printSummary(out io.Writer, title string, lines []string) {
	fmt.Fprintln(out, title)
	for _, l := range lines {
		fmt.Fprintln(out, "  "+l)
	}
}

func loadForAgent() (string, *store.File, error) {
	path, err := store.HostsPath()
	if err != nil {
		return "", nil, err
	}
	f, err := store.Open(path)
	return path, f, err
}

func runAgentEnable(args []string, in io.Reader, out io.Writer) error {
	name, alias, user, err := agentArgs(args, true)
	if err != nil {
		return err
	}
	if !isTerminal() {
		return errNeedTTY
	}
	path, f, err := loadForAgent()
	if err != nil {
		return err
	}
	h, err := agentaccess.FindHost(f, name)
	if err != nil {
		return err
	}
	if err := agentaccess.CheckEligible(h); err != nil {
		return err
	}
	m, err := newAgentManager(path)
	if err != nil {
		return err
	}
	ctx := context.Background()
	plan, err := m.PrepareEnable(ctx, f, h.ID, alias, user)
	if err != nil {
		return err
	}
	printSummary(out, "即將開通給 AI agent：", plan.Summary())
	if !confirm(bufio.NewReader(in), out, "確定要開通嗎？") {
		m.Discard(plan)
		fmt.Fprintln(out, "已取消，未做任何變更。")
		return nil
	}
	fmt.Fprintln(out, "正在登入遠端安裝公鑰…")
	res, err := m.Enable(ctx, plan)
	if err != nil {
		return err
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(out, w)
	}
	note := "已安裝公鑰"
	if res.Remote == "unchanged" {
		note = "遠端公鑰已存在，未變更"
	}
	fmt.Fprintf(out, "完成：%s，並以別名驗證連線成功。AI agent 可用 `ssh %s` 連線。\n", note, res.Alias)
	return nil
}

func runAgentDisable(args []string, in io.Reader, out io.Writer) error {
	name, _, _, err := agentArgs(args, false)
	if err != nil {
		return err
	}
	if !isTerminal() {
		return errNeedTTY
	}
	path, f, err := loadForAgent()
	if err != nil {
		return err
	}
	h, err := agentaccess.FindHost(f, name)
	if err != nil {
		return err
	}
	m, err := newAgentManager(path)
	if err != nil {
		return err
	}
	plan, err := m.PrepareDisable(f, h.ID)
	if err != nil {
		return err
	}
	r := bufio.NewReader(in)
	printSummary(out, "即將停用 AI agent 存取：", plan.Summary(m.Paths))
	if !confirm(r, out, "確定要停用嗎？") {
		fmt.Fprintln(out, "已取消，未做任何變更。")
		return nil
	}
	fmt.Fprintln(out, "正在登入遠端移除公鑰…")
	if _, err := m.DisableRemote(context.Background(), plan); err != nil {
		fmt.Fprintf(out, "無法移除遠端公鑰：%v\n", err)
		fmt.Fprintf(out, "遠端 %s 的 ~/.ssh/authorized_keys 仍有行尾為 %s 的公鑰，之後請自行刪除該行。\n", plan.User, agentaccess.KeyComment(plan.Alias))
		if !confirm(r, out, "是否只清除本機（別名區塊、金鑰與開通狀態）？") {
			fmt.Fprintln(out, "已取消，本機與遠端都維持原狀。")
			return nil
		}
	}
	removed, err := m.DisableLocal(plan)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "已停用「%s」：別名 %s 已移除，刪除 %d 個金鑰檔。\n", store.Printable(h.Name), plan.Alias, len(removed))
	return nil
}

// syncAgentConf 在 hosts.json 被改動後重新產生別名設定（機器改了 host／user／port 時別名跟著更新）。
func syncAgentConf(path string, errOut io.Writer) {
	f, err := store.Open(path)
	if err != nil {
		return
	}
	p, err := agentaccess.DefaultPaths()
	if err == nil {
		_, _, err = p.SyncConf(f)
	}
	if err != nil {
		fmt.Fprintln(errOut, "sshm：更新 AI agent 別名設定失敗：", err)
	}
}
