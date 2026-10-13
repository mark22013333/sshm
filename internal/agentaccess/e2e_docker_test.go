package agentaccess

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
)

// TestE2EDockerSSHD 對真正的 sshd（通常是暫時的 Docker 容器）跑完整流程：
// enable → ssh 別名成功 → disable → 舊金鑰被拒、別名消失。
// 只在設定 SSHM_E2E_PORT（127.0.0.1 上的埠）時執行；帳號 tester、密碼 test-pass。
// 所有 ssh 設定與 known_hosts 都在臨時目錄，不碰使用者的 ~/.ssh。
func TestE2EDockerSSHD(t *testing.T) {
	portStr := os.Getenv("SSHM_E2E_PORT")
	if portStr == "" {
		t.Skip("未設定 SSHM_E2E_PORT，略過端對端測試")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p := PathsFor(filepath.Join(dir, "home"), "/nonexistent")
	if err := os.MkdirAll(p.SSHDir, 0o700); err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(dir, "known_hosts")
	// 使用者自己的設定：known_hosts 指到臨時檔；Include 之後的 Host * 必須仍生效
	userCfg := "Host *\n  UserKnownHostsFile " + knownHosts + "\n  StrictHostKeyChecking accept-new\n"
	if err := os.WriteFile(p.Config, []byte(userCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	hostsPath := filepath.Join(dir, "sshm", "hosts.json")
	f := store.NewFile()
	f.Hosts = []store.Host{{ID: "h_e2e", Name: "範例客戶-E2E-VM", Group: "範例客戶", Host: "127.0.0.1", Port: port, User: "tester", Auth: store.AuthPassword, Password: "test-pass"}}
	if err := store.Save(hostsPath, f); err != nil {
		t.Fatal(err)
	}
	script, err := connect.EnsureLoginScript(filepath.Join(dir, "sshm"))
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{Paths: p, HostsPath: hostsPath, Remote: ExpectRunner(script, "-F", p.Config), Keygen: connect.ExecRunner}
	ctx := context.Background()
	sshAlias := func(args ...string) (string, error) {
		full := append([]string{"-F", p.Config, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}, args...)
		out, err := exec.Command("ssh", full...).CombinedOutput()
		return string(out), err
	}

	cur, _ := store.Open(hostsPath)
	plan, err := m.PrepareEnable(ctx, cur, "h_e2e", "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("確認畫面：\n  %s", strings.Join(plan.Summary(), "\n  "))
	res, err := m.Enable(ctx, plan)
	if err != nil {
		t.Fatalf("enable 失敗：%v", err)
	}
	t.Logf("enable：別名 %s、遠端 %s、警告 %v", res.Alias, res.Remote, res.Warnings)
	if res.Alias != "sshm-e2e-vm" {
		t.Fatalf("別名 = %s", res.Alias)
	}
	out, err := sshAlias(res.Alias, "id -un; stat -c %a ~/.ssh ~/.ssh/authorized_keys; cat ~/.ssh/authorized_keys")
	t.Logf("ssh %s →\n%s", res.Alias, out)
	if err != nil || !strings.Contains(out, "tester\n700\n600\n") || !strings.Contains(out, "no-agent-forwarding,no-X11-forwarding ssh-ed25519 ") ||
		!strings.Contains(out, " sshm-agent:sshm-e2e-vm") {
		t.Fatalf("以別名連線失敗或遠端內容不符：%v", err)
	}

	// 重複開通不產生重複行
	cur, _ = store.Open(hostsPath)
	plan2, err := m.PrepareEnable(ctx, cur, "h_e2e", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if res2, err := m.Enable(ctx, plan2); err != nil || res2.Remote != "unchanged" {
		t.Fatalf("重複開通 res=%+v err=%v", res2, err)
	}
	out, _ = sshAlias(res.Alias, "grep -c sshm-agent ~/.ssh/authorized_keys")
	if strings.TrimSpace(out) != "1" {
		t.Fatalf("authorized_keys 標記行數 = %q", out)
	}

	// 停用前留一份私鑰副本，用來證明遠端公鑰真的被移除
	keyCopy := filepath.Join(dir, "key-copy")
	data, _ := os.ReadFile(plan.Key.Private)
	if err := os.WriteFile(keyCopy, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cur, _ = store.Open(hostsPath)
	dp, err := m.PrepareDisable(cur, "h_e2e")
	if err != nil {
		t.Fatal(err)
	}
	st, err := m.DisableRemote(ctx, dp)
	if err != nil || st != "changed" {
		t.Fatalf("disable 遠端 st=%q err=%v", st, err)
	}
	removed, err := m.DisableLocal(dp)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("disable：遠端 %s、刪除 %v", st, removed)

	out, err = sshAlias(res.Alias, "true")
	t.Logf("停用後 ssh %s →（err=%v）\n%s", res.Alias, err, out)
	if err == nil {
		t.Fatal("停用後別名仍可連線")
	}
	out, err = sshAlias("-i", keyCopy, "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-p", portStr, "tester@127.0.0.1", "true")
	t.Logf("停用後以舊私鑰直連 →（err=%v）\n%s", err, out)
	if err == nil || !strings.Contains(out, "Permission denied") {
		t.Fatal("停用後舊金鑰仍被接受")
	}
	conf, _ := os.ReadFile(p.Conf)
	if strings.Contains(string(conf), "Host ") {
		t.Fatalf("conf 仍有區塊：%s", conf)
	}
}
