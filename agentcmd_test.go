package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark22013333/sshm/internal/agentaccess"
	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
)

// agentEnv 建立臨時 HOME／XDG_CONFIG_HOME 與 hosts.json；遠端以本機 sh 在 remoteHome 模擬。
type agentEnv struct {
	home, remoteHome, hostsPath string
	remoteFail                  error
	tty                         bool
}

func setupAgentEnv(t *testing.T, hosts ...store.Host) *agentEnv {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("沒有 ssh-keygen")
	}
	e := &agentEnv{home: t.TempDir(), remoteHome: t.TempDir(), tty: true}
	t.Setenv("HOME", e.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(e.home, ".config"))
	path, err := store.HostsPath()
	if err != nil {
		t.Fatal(err)
	}
	e.hostsPath = path
	f := store.NewFile()
	f.Hosts = hosts
	if err := store.Save(path, f); err != nil {
		t.Fatal(err)
	}
	origTTY, origMgr := isTerminal, newAgentManager
	t.Cleanup(func() { isTerminal, newAgentManager = origTTY, origMgr })
	isTerminal = func() bool { return e.tty }
	newAgentManager = func(hostsPath string) (*agentaccess.Manager, error) {
		return &agentaccess.Manager{
			Paths:     agentaccess.PathsFor(e.home, "/nonexistent"),
			HostsPath: hostsPath,
			Keygen:    connect.ExecRunner,
			Remote: func(_ context.Context, h store.Host, user, script string) (string, error) {
				if e.remoteFail != nil {
					return "Connection timed out", e.remoteFail
				}
				cmd := exec.Command("/bin/sh", "-c", agentaccess.RemoteCommand(script))
				cmd.Env = append(os.Environ(), "HOME="+e.remoteHome)
				out, err := cmd.CombinedOutput()
				return string(out), err
			},
			Verify: func(context.Context, string) error { return nil },
		}, nil
	}
	return e
}

func demoHosts() []store.Host {
	return []store.Host{
		{ID: "h_1", Name: "範例客戶-PROD-VM", Group: "範例客戶", Host: "10.0.0.24", Port: 22, User: "helpdesk", Auth: store.AuthPassword, Password: "test-pass"},
		{ID: "h_2", Name: "範例客戶-KEY-VM", Host: "10.0.0.25", Port: 22, User: "ops", Auth: store.AuthKey, IdentityFile: "/k"},
		{ID: "h_3", Name: "範例客戶-JUMP-VM", Host: "10.0.0.26", Port: 22, User: "ops", Auth: store.AuthPassword, Password: "test-pass", ExtraArgs: "-J jump"},
		{ID: "h_4", Name: "範例客戶-CUSTOM", Host: "10.0.0.27", Port: 22, User: "ops", Auth: store.AuthPassword, Password: "test-pass", CustomCommand: "ssh x"},
	}
}

func runAgentCLI(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runAgent(args, strings.NewReader(input), &out)
	return out.String(), err
}

func TestParseArgsAgent(t *testing.T) {
	got := parseArgs([]string{"agent", "list", "--json"})
	if got.name != "agent" || strings.Join(got.args, " ") != "list --json" {
		t.Fatalf("parseArgs = %+v", got)
	}
}

func TestAgentArgs(t *testing.T) {
	name, alias, user, err := agentArgs([]string{"--alias=my-vm", "範例 VM", "--user", "readonly"}, true)
	if err != nil || name != "範例 VM" || alias != "my-vm" || user != "readonly" {
		t.Fatalf("got %q %q %q %v", name, alias, user, err)
	}
	for _, bad := range [][]string{{"--yes", "x"}, {"-y", "x"}, {"a", "b"}, {}, {"x", "--alias"}} {
		if _, _, _, err := agentArgs(bad, true); err == nil {
			t.Errorf("agentArgs(%q) 應報錯", bad)
		}
	}
	if _, _, _, err := agentArgs([]string{"x", "--alias", "a"}, false); err == nil {
		t.Error("disable 不接受 --alias")
	}
}

func TestAgentRefusesWithoutTTY(t *testing.T) {
	e := setupAgentEnv(t, demoHosts()...)
	e.tty = false
	for _, sub := range []string{"enable", "disable"} {
		out, err := runAgentCLI(t, "y\n", sub, "範例客戶-PROD-VM")
		if !errors.Is(err, errNeedTTY) {
			t.Fatalf("%s 非 TTY 應拒絕：%v", sub, err)
		}
		if out != "" {
			t.Fatalf("%s 被拒時不應有任何動作輸出：%q", sub, out)
		}
	}
	if _, err := os.Stat(filepath.Join(e.home, ".ssh")); !os.IsNotExist(err) {
		t.Fatal("被拒時不應建立 ~/.ssh")
	}
	// list 唯讀，不需 TTY
	if _, err := runAgentCLI(t, "", "list"); err != nil {
		t.Fatal(err)
	}
}

func TestAgentEnableRejectsUnsupported(t *testing.T) {
	setupAgentEnv(t, demoHosts()...)
	cases := map[string]string{
		"範例客戶-KEY-VM":  "只支援 password",
		"範例客戶-JUMP-VM": "額外 ssh 參數",
		"範例客戶-CUSTOM":  "自訂指令",
		"不存在":          "找不到",
	}
	for name, want := range cases {
		_, err := runAgentCLI(t, "y\n", "enable", name)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", name, err, want)
		}
	}
}

func TestAgentEnableCancelAndConfirm(t *testing.T) {
	e := setupAgentEnv(t, demoHosts()...)
	out, err := runAgentCLI(t, "n\n", "enable", "範例客戶-PROD-VM")
	if err != nil || !strings.Contains(out, "已取消") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	for _, want := range []string{"範例客戶-PROD-VM", "helpdesk", "SHA256:", "sshm-prod-vm", "sshm_agents.conf", "authorized_keys"} {
		if !strings.Contains(out, want) {
			t.Errorf("確認畫面缺 %q：\n%s", want, out)
		}
	}
	if strings.Contains(out, "test-pass") {
		t.Fatal("確認畫面不得顯示密碼")
	}
	if entries, _ := os.ReadDir(filepath.Join(e.home, ".ssh", "sshm")); len(entries) != 0 {
		t.Fatalf("取消後不應留下金鑰：%v", entries)
	}
	// EOF 也算取消
	if out, _ := runAgentCLI(t, "", "enable", "範例客戶-PROD-VM"); !strings.Contains(out, "已取消") {
		t.Fatalf("EOF out=%q", out)
	}
	out, err = runAgentCLI(t, "y\n", "enable", "範例客戶-PROD-VM")
	if err != nil || !strings.Contains(out, "ssh sshm-prod-vm") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	f, _ := store.Open(e.hostsPath)
	if f.Hosts[0].Agent == nil || f.Hosts[0].Agent.Alias != "sshm-prod-vm" {
		t.Fatalf("agent = %+v", f.Hosts[0].Agent)
	}
}

func TestAgentListNoPasswords(t *testing.T) {
	setupAgentEnv(t, demoHosts()...)
	out, _ := runAgentCLI(t, "", "list")
	if !strings.Contains(out, "還沒有開通") {
		t.Fatalf("空清單 out=%q", out)
	}
	out, _ = runAgentCLI(t, "", "list", "--json")
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("空 JSON = %q", out)
	}
	if _, err := runAgentCLI(t, "y\n", "enable", "範例客戶-PROD-VM", "--user", "readonly"); err != nil {
		t.Fatal(err)
	}
	out, _ = runAgentCLI(t, "", "list")
	for _, want := range []string{"名稱", "別名", "範例客戶-PROD-VM", "sshm-prod-vm", "10.0.0.24", "readonly", "範例客戶"} {
		if !strings.Contains(out, want) {
			t.Errorf("list 缺 %q：\n%s", want, out)
		}
	}
	jsonOut, _ := runAgentCLI(t, "", "list", "--json")
	for _, o := range []string{out, jsonOut} {
		if strings.Contains(o, "test-pass") || strings.Contains(strings.ToLower(o), "password") {
			t.Fatalf("list 不得含密碼：%s", o)
		}
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0]["alias"] != "sshm-prod-vm" || entries[0]["user"] != "readonly" || entries[0]["group"] != "範例客戶" ||
		entries[0]["host"] != "10.0.0.24" || entries[0]["name"] != "範例客戶-PROD-VM" {
		t.Fatalf("json = %v", entries)
	}
	if _, err := runAgentCLI(t, "", "list", "--yaml"); err == nil {
		t.Fatal("不認得的選項應報錯")
	}
}

func TestAgentDisableRemoteUnreachable(t *testing.T) {
	e := setupAgentEnv(t, demoHosts()...)
	if _, err := runAgentCLI(t, "y\n", "enable", "範例客戶-PROD-VM"); err != nil {
		t.Fatal(err)
	}
	e.remoteFail = errors.New("逾時")
	// 第一個 y 確認停用、第二個回答 n：不清本機
	out, err := runAgentCLI(t, "y\nn\n", "disable", "範例客戶-PROD-VM")
	if err != nil || !strings.Contains(out, "仍有行尾為 sshm-agent:sshm-prod-vm 的公鑰") || !strings.Contains(out, "維持原狀") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	f, _ := store.Open(e.hostsPath)
	if f.Hosts[0].Agent == nil {
		t.Fatal("回答 n 時不應清本機")
	}
	out, err = runAgentCLI(t, "y\ny\n", "disable", "範例客戶-PROD-VM")
	if err != nil || !strings.Contains(out, "已停用") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	f, _ = store.Open(e.hostsPath)
	if f.Hosts[0].Agent != nil {
		t.Fatal("應清除本機開通狀態")
	}
	conf, _ := os.ReadFile(filepath.Join(e.home, ".ssh", "sshm_agents.conf"))
	if strings.Contains(string(conf), "Host ") {
		t.Fatalf("conf = %s", conf)
	}
}

func TestAgentGuideAndHelp(t *testing.T) {
	out, _ := runAgentCLI(t, "", "guide")
	for _, want := range []string{"sshm agent list --json", "ssh <alias>", "不要要求", "密碼"} {
		if !strings.Contains(out, want) {
			t.Errorf("guide 缺 %q", want)
		}
	}
	out, _ = runAgentCLI(t, "", "--help")
	if !strings.Contains(out, "沒有跳過確認的選項") || !strings.Contains(out, "ssh <別名>") {
		t.Errorf("help = %s", out)
	}
	var buf bytes.Buffer
	run([]string{"--help"}, &buf, &buf)
	if !strings.Contains(buf.String(), "sshm agent") {
		t.Error("主說明應列出 agent 子指令")
	}
}

func TestImportResyncsAgentConf(t *testing.T) {
	e := setupAgentEnv(t, demoHosts()...)
	if _, err := runAgentCLI(t, "y\n", "enable", "範例客戶-PROD-VM"); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(e.home, "in.json")
	data := `{"hosts":[{"id":"h_1","name":"範例客戶-PROD-VM","group":"範例客戶","host":"10.0.0.88","port":2200,"user":"helpdesk","auth":"password","password":"test-pass"}]}`
	_ = os.WriteFile(src, []byte(data), 0o600)
	var out, errOut bytes.Buffer
	if err := runImport([]string{src}, strings.NewReader("y\n"), &out, &errOut); err != nil {
		t.Fatal(err, errOut.String())
	}
	conf, _ := os.ReadFile(filepath.Join(e.home, ".ssh", "sshm_agents.conf"))
	if !strings.Contains(string(conf), "HostName 10.0.0.88") || !strings.Contains(string(conf), "Port 2200") {
		t.Fatalf("匯入改了 host 後 conf 應更新：%s", conf)
	}
	if msg := errOut.String(); !strings.Contains(msg, "需重新 enable") || !strings.Contains(msg, "舊位置的公鑰會在 disable 時清除") {
		t.Fatalf("匯入改了位置應提示重新 enable：%q", msg)
	}
}

// 審查 #2 的端對端情境：已開通後 import 把 user 改成 o'brien。別名停用、整份 ssh 設定仍可解析，且停用狀態看得到。
func TestImportUnsafeUserDisablesAliasOnly(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("沒有 ssh")
	}
	e := setupAgentEnv(t, demoHosts()...)
	if _, err := runAgentCLI(t, "y\n", "enable", "範例客戶-PROD-VM"); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(e.home, "in.json")
	data := `{"hosts":[{"id":"h_1","name":"範例客戶-PROD-VM","group":"範例客戶","host":"10.0.0.24","port":22,"user":"o'brien","auth":"password","password":"test-pass"}]}`
	_ = os.WriteFile(src, []byte(data), 0o600)
	var out, errOut bytes.Buffer
	if err := runImport([]string{src}, strings.NewReader("y\n"), &out, &errOut); err != nil {
		t.Fatal(err, errOut.String())
	}
	if !strings.Contains(errOut.String(), "別名已停用") || !strings.Contains(errOut.String(), "o'brien") {
		t.Fatalf("應提示別名已停用：%s", errOut.String())
	}
	conf, _ := os.ReadFile(filepath.Join(e.home, ".ssh", "sshm_agents.conf"))
	if strings.Contains(string(conf), "o'brien") || strings.Contains(string(conf), "Host sshm-") {
		t.Fatalf("不合法的機器不應寫入：%s", conf)
	}
	cfg := filepath.Join(e.home, ".ssh", "config")
	if out, err := exec.Command("ssh", "-G", "-F", cfg, "github.com").CombinedOutput(); err != nil {
		t.Fatalf("整份 ssh 設定應仍可解析：%v\n%s", err, out)
	}
	listOut, err := runAgentCLI(t, "", "list", "--json")
	if err != nil || !strings.Contains(listOut, `"active": false`) || !strings.Contains(listOut, `"problem"`) {
		t.Fatalf("list 應標出停用：%v\n%s", err, listOut)
	}
}

// 審查 #1：說明文字誠實：確認關卡是防誤開通，不是防惡意繞過。
func TestAgentUsageStatesBoundary(t *testing.T) {
	out, err := runAgentCLI(t, "", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"防止誤開通", "不是防止惡意繞過", "同一個 OS 帳號"} {
		if !strings.Contains(out, want) {
			t.Errorf("說明缺 %q：\n%s", want, out)
		}
	}
}
