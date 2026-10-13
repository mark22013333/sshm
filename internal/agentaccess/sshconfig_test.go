package agentaccess

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark22013333/sshm/internal/store"
)

// testPaths 在臨時目錄組出路徑；passwd 家目錄給不同值，Include 一律寫絕對路徑。
func testPaths(t *testing.T) Paths {
	t.Helper()
	home := t.TempDir()
	return PathsFor(home, "/nonexistent-passwd-home")
}

func agentFile() *store.File {
	return &store.File{Hosts: []store.Host{
		{ID: "h_1", Name: "範例客戶-PROD-VM", Host: "10.0.0.24", Port: 2222, User: "helpdesk", Auth: store.AuthPassword, Password: "test-pass",
			Agent: &store.AgentAccess{Alias: "sshm-prod-vm", KeyFile: "/tmp/k dir/sshm-prod-vm"}},
		{ID: "h_2", Name: "範例客戶-UAT", Host: "10.0.0.25", User: "helpdesk", Auth: store.AuthPassword, Password: "test-pass",
			Agent: &store.AgentAccess{Alias: "sshm-a-uat", User: "readonly", KeyFile: "/tmp/k/sshm-a-uat"}},
		{ID: "h_3", Name: "未開通", Host: "10.0.0.26", User: "u", Auth: store.AuthPassword},
	}}
}

func TestPathsForIncludeArg(t *testing.T) {
	p := PathsFor("/Users/example", "/Users/example")
	if p.IncludeArg != "~/.ssh/sshm_agents.conf" || p.Conf != "/Users/example/.ssh/sshm_agents.conf" || p.KeyDir != "/Users/example/.ssh/sshm" {
		t.Fatalf("同家目錄 = %+v", p)
	}
	p = PathsFor("/tmp/x y", "/Users/example")
	if p.IncludeArg != `"/tmp/x y/.ssh/sshm_agents.conf"` {
		t.Fatalf("不同家目錄 = %q", p.IncludeArg)
	}
}

func TestRenderConf(t *testing.T) {
	f := agentFile()
	// 手改 hosts.json 帶進 % 的機器要略過
	f.Hosts = append(f.Hosts, store.Host{ID: "h_4", Name: "bad", Host: "10.0.0.%h", User: "u", Auth: store.AuthPassword,
		Agent: &store.AgentAccess{Alias: "sshm-bad", KeyFile: "/k"}})
	data, skipped := RenderConf(f)
	got := string(data)
	want := confHeader + `
Host sshm-a-uat
  HostName 10.0.0.25
  User readonly
  Port 22
  IdentityFile /tmp/k/sshm-a-uat
  IdentitiesOnly yes
  IdentityAgent none

Host sshm-prod-vm
  HostName 10.0.0.24
  User helpdesk
  Port 2222
  IdentityFile "/tmp/k dir/sshm-prod-vm"
  IdentitiesOnly yes
  IdentityAgent none
`
	if got != want {
		t.Fatalf("RenderConf =\n%s\nwant\n%s", got, want)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "bad") {
		t.Fatalf("skipped = %v", skipped)
	}
	if strings.Contains(got, "test-pass") {
		t.Fatal("conf 不得含密碼")
	}
}

func TestSyncConfUpdatesAfterHostChange(t *testing.T) {
	p := testPaths(t)
	empty := &store.File{}
	if changed, _, err := p.SyncConf(empty); err != nil || changed {
		t.Fatalf("沒有開通機器時不應建立 conf：%v %v", changed, err)
	}
	if _, err := os.Stat(p.Conf); !os.IsNotExist(err) {
		t.Fatal("conf 不應存在")
	}
	f := agentFile()
	if changed, _, err := p.SyncConf(f); err != nil || !changed {
		t.Fatalf("首次寫入：%v %v", changed, err)
	}
	st, _ := os.Stat(p.Conf)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("conf perm = %o", st.Mode().Perm())
	}
	if changed, _, _ := p.SyncConf(f); changed {
		t.Fatal("內容相同不應重寫")
	}
	// 機器改了 host／user／port → conf 跟著更新
	f.Hosts[0].Host, f.Hosts[0].Port, f.Hosts[0].User = "10.0.0.99", 22, "ops"
	if changed, _, err := p.SyncConf(f); err != nil || !changed {
		t.Fatalf("改 host 後應重寫：%v %v", changed, err)
	}
	data, _ := os.ReadFile(p.Conf)
	block := string(data)[strings.Index(string(data), "Host sshm-prod-vm"):]
	for _, want := range []string{"HostName 10.0.0.99", "User ops", "Port 22"} {
		if !strings.Contains(block, want) {
			t.Errorf("conf 缺 %q：\n%s", want, block)
		}
	}
	// 有指定帳號的機器，改機器 user 不影響別名帳號
	f.Hosts[1].User = "someone"
	_, _, _ = p.SyncConf(f)
	data, _ = os.ReadFile(p.Conf)
	if !strings.Contains(string(data), "User readonly") {
		t.Fatal("指定的登入帳號應保留")
	}
	// 全部停用 → 只剩檔頭
	for i := range f.Hosts {
		f.Hosts[i].Agent = nil
	}
	_, _, _ = p.SyncConf(f)
	data, _ = os.ReadFile(p.Conf)
	if string(data) != confHeader {
		t.Fatalf("停用後 conf = %q", data)
	}
}

func TestSyncConfFollowsSymlink(t *testing.T) {
	p := testPaths(t)
	if err := os.MkdirAll(p.SSHDir, 0o700); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(t.TempDir(), "real.conf")
	if err := os.WriteFile(real, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, p.Conf); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.SyncConf(agentFile()); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(p.Conf); st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink 被換成一般檔")
	}
	data, _ := os.ReadFile(real)
	if !strings.Contains(string(data), "Host sshm-prod-vm") {
		t.Fatal("應寫到 symlink 目標")
	}
}

func TestEnsureIncludeCreatesConfig(t *testing.T) {
	p := testPaths(t)
	added, st, err := p.EnsureInclude()
	if err != nil || !added || st.Misplaced {
		t.Fatalf("added=%v st=%+v err=%v", added, st, err)
	}
	data, _ := os.ReadFile(p.Config)
	if string(data) != "Include "+p.IncludeArg+"\n" {
		t.Fatalf("config = %q", data)
	}
	for path, want := range map[string]os.FileMode{p.Config: 0o600, p.SSHDir: 0o700} {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != want {
			t.Errorf("%s perm = %o, want %o", path, st.Mode().Perm(), want)
		}
	}
	// 再跑一次不重複加
	added, _, _ = p.EnsureInclude()
	data2, _ := os.ReadFile(p.Config)
	if added || string(data2) != string(data) {
		t.Fatalf("重複加入 Include：%q", data2)
	}
}

func TestEnsureIncludeInsertsAtTopKeepingContent(t *testing.T) {
	p := testPaths(t)
	if err := os.MkdirAll(p.SSHDir, 0o700); err != nil {
		t.Fatal(err)
	}
	orig := "# 我的設定\nHost work\n  HostName 10.0.0.1\n\nHost *\n  ServerAliveInterval 30\n"
	if err := os.WriteFile(p.Config, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	added, _, err := p.EnsureInclude()
	if err != nil || !added {
		t.Fatal(added, err)
	}
	data, _ := os.ReadFile(p.Config)
	if string(data) != "Include "+p.IncludeArg+"\n"+orig {
		t.Fatalf("config = %q", data)
	}
	if st, _ := os.Stat(p.Config); st.Mode().Perm() != 0o644 {
		t.Fatalf("既有檔權限不應被改：%o", st.Mode().Perm())
	}
}

func TestCheckIncludeDetectsMisplaced(t *testing.T) {
	cases := []struct {
		name, config    string
		found, misplace bool
	}{
		{"最上方", "Include ~/.ssh/sshm_agents.conf\nHost a\n", true, false},
		{"註解後最上方", "# x\n\nInclude=sshm_agents.conf\n", true, false},
		{"Host * 之後", "Host *\n  User x\nInclude ~/.ssh/sshm_agents.conf\n", true, true},
		{"Match 之後", "Match host a\nInclude \"~/.ssh/sshm_agents.conf\"\n", true, true},
		{"縮排在區塊內", "Host a\n  Include ~/.ssh/sshm_agents.conf\n", true, true},
		{"別的 Include", "Include ~/.ssh/other.conf\n", false, false},
	}
	for _, c := range cases {
		p := testPaths(t)
		if err := os.MkdirAll(p.SSHDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.Config, []byte(c.config), 0o600); err != nil {
			t.Fatal(err)
		}
		st, err := p.CheckInclude()
		if err != nil || st.Found != c.found || st.Misplaced != c.misplace {
			t.Errorf("%s: st=%+v err=%v", c.name, st, err)
			continue
		}
		if c.misplace {
			// 位置不對時不自動搬動，也不再加第二行
			added, _, _ := p.EnsureInclude()
			data, _ := os.ReadFile(p.Config)
			if added || string(data) != c.config {
				t.Errorf("%s: 不應改動使用者設定：%q", c.name, data)
			}
			if w := p.IncludeWarning(st); !strings.Contains(w, "最上方") || !strings.Contains(w, "Host／Match") {
				t.Errorf("%s: 警告文字 = %s", c.name, w)
			}
		}
	}
}

func TestEnsureIncludeFollowsSymlink(t *testing.T) {
	p := testPaths(t)
	if err := os.MkdirAll(p.SSHDir, 0o700); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(t.TempDir(), "dotfiles-ssh-config")
	if err := os.WriteFile(real, []byte("Host a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, p.Config); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.EnsureInclude(); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(p.Config); st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink 被換成一般檔")
	}
	data, _ := os.ReadFile(real)
	if !strings.HasPrefix(string(data), "Include ") {
		t.Fatalf("目標內容 = %q", data)
	}
}

func TestConfigDefinesHost(t *testing.T) {
	p := testPaths(t)
	if ok, err := p.ConfigDefinesHost("sshm-a"); ok || err != nil {
		t.Fatal("沒有 config 時應為 false")
	}
	_ = os.MkdirAll(p.SSHDir, 0o700)
	_ = os.WriteFile(p.Config, []byte("Host work sshm-a\n  HostName x\nHost sshm-*\n"), 0o600)
	if ok, _ := p.ConfigDefinesHost("sshm-a"); !ok {
		t.Fatal("應偵測到同名 Host")
	}
	if ok, _ := p.ConfigDefinesHost("sshm-b"); ok {
		t.Fatal("萬用字元不算同名")
	}
}

// TestConfParsedBySSH 以真正的 ssh -G（不連線）確認產生的 conf 與 Include 解析結果正確，
// 且 Include 之後使用者自己的 Host * 仍然生效。
func TestConfParsedBySSH(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("沒有 ssh")
	}
	p := testPaths(t)
	f := agentFile()
	f.Hosts[0].Agent.KeyFile = filepath.Join(p.KeyDir, "sshm-prod-vm")
	if _, _, err := p.SyncConf(f); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Config, []byte("Host *\n  ServerAliveInterval 42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.EnsureInclude(); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ssh", "-G", "-F", p.Config, "sshm-prod-vm").Output()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"hostname 10.0.0.24\n", "user helpdesk\n", "port 2222\n", "identitiesonly yes\n",
		"identityagent none\n", "identityfile " + f.Hosts[0].Agent.KeyFile + "\n", "serveraliveinterval 42\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("ssh -G 缺 %q", want)
		}
	}
}
