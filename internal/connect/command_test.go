package connect

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mark22013333/sshm/internal/store"
)

const script = "/cfg/sshm/sshm-login.exp"

func TestQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "''"},
		{"helpdesk", "helpdesk"},
		{"10.0.0.24", "10.0.0.24"},
		{"ServerAliveInterval=30", "ServerAliveInterval=30"},
		{"=cmd", "'=cmd'"},
		{"a b", "'a b'"},
		{"it's", `'it'\''s'`},
		{`p$ss"w`, `'p$ss"w'`},
		{"範例客戶", "'範例客戶'"},
		{"a*b?", "'a*b?'"},
		{"~/.ssh/id", "'~/.ssh/id'"},
		{"!x", "'!x'"},
	}
	for _, c := range cases {
		if got := Quote(c.in); got != c.want {
			t.Errorf("Quote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHostPlan(t *testing.T) {
	base := store.Host{Name: "n", Host: "10.0.0.24", Port: 2222, User: "helpdesk"}
	with := func(mod func(*store.Host)) store.Host {
		h := base
		mod(&h)
		return h
	}
	cases := []struct {
		name     string
		host     store.Host
		wantArgv []string
		wantText string
		wantErr  string
	}{
		{
			name:     "password",
			host:     with(func(h *store.Host) { h.Auth = "password"; h.Password = "secret" }),
			wantArgv: []string{script, "2222", "helpdesk", "10.0.0.24", "secret"},
			wantText: " /cfg/sshm/sshm-login.exp 2222 helpdesk 10.0.0.24 secret",
		},
		{
			name: "password 含空白引號與 $ 並帶 extraArgs",
			host: with(func(h *store.Host) {
				h.Auth = "password"
				h.Password = `a b'c"$HOME`
				h.ExtraArgs = `-J jump -o "ProxyCommand=nc %h %p"`
			}),
			wantArgv: []string{script, "2222", "helpdesk", "10.0.0.24", `a b'c"$HOME`, "-J", "jump", "-o", "ProxyCommand=nc %h %p"},
			wantText: ` /cfg/sshm/sshm-login.exp 2222 helpdesk 10.0.0.24 'a b'\''c"$HOME' -J jump -o 'ProxyCommand=nc %h %p'`,
		},
		{
			name:     "key",
			host:     with(func(h *store.Host) { h.Auth = "key"; h.IdentityFile = "/k/my key" }),
			wantArgv: []string{"ssh", "-o", "StrictHostKeyChecking=accept-new", "-p", "2222", "-i", "/k/my key", "--", "helpdesk@10.0.0.24"},
			wantText: " ssh -o StrictHostKeyChecking=accept-new -p 2222 -i '/k/my key' -- helpdesk@10.0.0.24",
		},
		{
			name: "key 帶 extraArgs",
			host: with(func(h *store.Host) {
				h.Auth = "key"
				h.IdentityFile = "/k/id"
				h.ExtraArgs = "-J jump -o ServerAliveInterval=30"
			}),
			wantArgv: []string{"ssh", "-o", "StrictHostKeyChecking=accept-new", "-p", "2222", "-i", "/k/id", "-J", "jump", "-o", "ServerAliveInterval=30", "--", "helpdesk@10.0.0.24"},
		},
		{
			name:     "none 預設 port 22",
			host:     with(func(h *store.Host) { h.Auth = "none"; h.Port = 0 }),
			wantArgv: []string{"ssh", "-o", "StrictHostKeyChecking=accept-new", "-p", "22", "--", "helpdesk@10.0.0.24"},
		},
		{
			name:     "空 auth 視同 none",
			host:     base,
			wantArgv: []string{"ssh", "-o", "StrictHostKeyChecking=accept-new", "-p", "2222", "--", "helpdesk@10.0.0.24"},
		},
		{
			name:     "名稱含空白與引號（quoting）",
			host:     with(func(h *store.Host) { h.Auth = "none"; h.Name = "a b"; h.User = "o'neil" }),
			wantText: ` ssh -o StrictHostKeyChecking=accept-new -p 2222 -- 'o'\''neil@10.0.0.24'`,
		},
		{name: "host 含 @（實際會連到 @ 後的主機）", host: with(func(h *store.Host) { h.Auth = "password"; h.Host = "victim@attacker" }), wantErr: "host 不可包含 @"},
		{name: "user 含 @", host: with(func(h *store.Host) { h.User = "root@evil" }), wantErr: "user 不可包含 @"},
		{name: "user 含空白", host: with(func(h *store.Host) { h.User = "a b" }), wantErr: "user 不可包含"},
		{
			name: "customCommand 整個取代",
			host: with(func(h *store.Host) {
				h.Auth = "password"
				h.Password = "x"
				h.ExtraArgs = "-v"
				h.CustomCommand = "  kubectl exec -it pod -- bash "
			}),
			wantText: " kubectl exec -it pod -- bash",
		},
		{name: "key 未設金鑰", host: with(func(h *store.Host) { h.Auth = "key" }), wantErr: "金鑰檔"},
		{name: "未知 auth", host: with(func(h *store.Host) { h.Auth = "otp" }), wantErr: "不支援"},
		{name: "extraArgs 引號未閉合", host: with(func(h *store.Host) { h.ExtraArgs = `-o "x` }), wantErr: "無法解析"},
		{name: "缺 host", host: with(func(h *store.Host) { h.Host = "" }), wantErr: "host"},
		{name: "手改檔帶進控制字元", host: with(func(h *store.Host) { h.Auth = "password"; h.Password = "x\x15rm -rf" }), wantErr: "控制字元"},
		{name: "customCommand 含換行", host: with(func(h *store.Host) { h.CustomCommand = "ls\nwhoami" }), wantErr: "控制字元"},
		{name: "user 以 - 開頭", host: with(func(h *store.Host) { h.User = "-oProxyCommand=evil" }), wantErr: "- 開頭"},
		{name: "host 以 - 開頭（password）", host: with(func(h *store.Host) { h.Auth = "password"; h.Host = "-oX=1" }), wantErr: "- 開頭"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := HostPlan(c.host, script)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want contains %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.wantArgv != nil && !reflect.DeepEqual(p.Argv, c.wantArgv) {
				t.Errorf("argv = %q\nwant  %q", p.Argv, c.wantArgv)
			}
			if c.wantText != "" && p.Text != c.wantText {
				t.Errorf("text = %q\nwant  %q", p.Text, c.wantText)
			}
			if !strings.HasPrefix(p.Text, " ") {
				t.Errorf("text should start with a space: %q", p.Text)
			}
		})
	}
}

func TestCustomCommandExecUsesShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	p, err := HostPlan(store.Host{Name: "n", Host: "h", User: "u", CustomCommand: "echo hi"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Argv, []string{"/bin/zsh", "-c", "echo hi"}) {
		t.Fatalf("argv = %q", p.Argv)
	}
}

func TestTunnelPlan(t *testing.T) {
	tun := store.Tunnel{Name: "db", HostID: "h_1", Rules: []store.Rule{
		{Type: "L", BindPort: 13306, TargetHost: "127.0.0.1", TargetPort: 3306},
		{Type: "R", BindAddress: "0.0.0.0", BindPort: 8080, TargetHost: "localhost", TargetPort: 80},
		{Type: "D", BindPort: 1080},
	}}
	h := store.Host{ID: "h_1", Name: "n", Host: "h", User: "u", Port: 22, Auth: "none", ExtraArgs: "-J j"}
	p, err := TunnelPlan(h, tun, script)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ssh", "-o", "StrictHostKeyChecking=accept-new", "-p", "22",
		"-N", "-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3", "-o", "ExitOnForwardFailure=yes",
		"-o", "ControlPath=none", "-o", "ControlMaster=no",
		"-L", "13306:127.0.0.1:3306", "-R", "0.0.0.0:8080:localhost:80", "-D", "1080", "-J", "j", "--", "u@h"}
	if !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("argv = %q", p.Argv)
	}
	if _, err := TunnelPlan(h, store.Tunnel{Name: "x", HostID: "h_1", Rules: []store.Rule{{Type: "L", BindPort: 1}}}, script); err == nil {
		t.Fatal("L without target should fail")
	}
}

// IPv6 位址在規則字串中要加中括號。
func TestTunnelIPv6Brackets(t *testing.T) {
	h := store.Host{ID: "h_1", Name: "n", Host: "h", User: "u", Port: 22, Auth: "none"}
	tun := store.Tunnel{Name: "v6", HostID: "h_1", Rules: []store.Rule{
		{Type: "L", BindPort: 13306, TargetHost: "::1", TargetPort: 3306},
		{Type: "L", BindAddress: "::1", BindPort: 13306, TargetHost: "h", TargetPort: 22},
		{Type: "D", BindAddress: "fe80::1", BindPort: 1080},
		{Type: "R", BindPort: 8080, TargetHost: "[2001:db8::1]", TargetPort: 80},
	}}
	p, err := TunnelPlan(h, tun, script)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(p.Argv, " ")
	for _, want := range []string{"-L 13306:[::1]:3306", "-L [::1]:13306:h:22", "-D [fe80::1]:1080", "-R 8080:[2001:db8::1]:80"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv 缺 %q：%s", want, got)
		}
	}
}

// TunnelPlan 套用與匯入相同的驗證。
func TestTunnelPlanValidates(t *testing.T) {
	h := store.Host{ID: "h_1", Name: "n", Host: "h", User: "u", Port: 22, Auth: "none"}
	cases := []struct {
		name string
		tun  store.Tunnel
		want string
	}{
		{"type 不合法", store.Tunnel{Name: "x", HostID: "h_1", Rules: []store.Rule{{Type: "Z", BindPort: 1}}}, "type 只能是"},
		{"小寫 type", store.Tunnel{Name: "x", HostID: "h_1", Rules: []store.Rule{{Type: "l", BindPort: 1, TargetHost: "a", TargetPort: 2}}}, "type 只能是"},
		{"port 超出範圍", store.Tunnel{Name: "x", HostID: "h_1", Rules: []store.Rule{{Type: "D", BindPort: 70000}}}, "bindPort"},
		{"targetPort 超出範圍", store.Tunnel{Name: "x", HostID: "h_1", Rules: []store.Rule{{Type: "L", BindPort: 1, TargetHost: "a", TargetPort: 0}}}, "targetPort"},
		{"hostId 不符", store.Tunnel{Name: "x", HostID: "h_2", Rules: []store.Rule{{Type: "D", BindPort: 1}}}, "hostId"},
		{"缺名稱", store.Tunnel{HostID: "h_1", Rules: []store.Rule{{Type: "D", BindPort: 1}}}, "名稱"},
		{"targetHost 以 - 開頭", store.Tunnel{Name: "x", HostID: "h_1", Rules: []store.Rule{{Type: "L", BindPort: 1, TargetHost: "-oX", TargetPort: 2}}}, "位址"},
	}
	for _, c := range cases {
		if _, err := TunnelPlan(h, c.tun, script); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want 含 %q", c.name, err, c.want)
		}
	}
}

func TestDetectOrca(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if _, ok := DetectOrca(env(map[string]string{"TERM_PROGRAM": "Orca"})); ok {
		t.Error("missing bin dir should not be Orca")
	}
	if _, ok := DetectOrca(env(map[string]string{"TERM_PROGRAM": "iTerm.app", "ORCA_CLI_BIN_DIR": "/x"})); ok {
		t.Error("iTerm should not be Orca")
	}
	o, ok := DetectOrca(env(map[string]string{"TERM_PROGRAM": "Orca", "ORCA_CLI_BIN_DIR": "/Applications/Orca.app/Contents/Resources/bin"}))
	if !ok || o.BinDir != "/Applications/Orca.app/Contents/Resources/bin" {
		t.Errorf("detect = %+v %v", o, ok)
	}
}

func TestOrcaCreateArgv(t *testing.T) {
	o := OrcaEnv{BinDir: "/opt/orca bin"}
	h := store.Host{Name: `範例 "PROD"`, Host: "10.0.0.24", Port: 22, User: "helpdesk", Auth: "password", Password: "p w"}
	p, err := HostPlan(h, script)
	if err != nil {
		t.Fatal(err)
	}
	got := o.CreateArgv(Title("🔴", h.Name), p.Text)
	want := []string{
		"/opt/orca bin/orca", "terminal", "create",
		"--worktree", "active",
		`--title=🔴 範例 "PROD"`,
		"--command= /cfg/sshm/sshm-login.exp 22 helpdesk 10.0.0.24 'p w'",
		"--focus", "--json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q\nwant  %q", got, want)
	}
	if Title("", "n") != "n" {
		t.Error("title without emoji")
	}
}

func TestOpenTab(t *testing.T) {
	o := OrcaEnv{BinDir: "/b"}
	cases := []struct {
		name       string
		out        string
		stderr     string
		err        error
		wantHandle string
		wantErr    string
	}{
		{name: "成功", out: `{"id":"1","ok":true,"result":{"terminal":{"handle":"term_abc"}}}`, wantHandle: "term_abc"},
		{name: "ok false", out: `{"ok":false,"error":{"code":"x","message":"no active worktree"}}`, err: errors.New("exit status 1"), wantErr: "no active worktree"},
		{name: "ok false 但結束碼 0", out: `{"ok":false,"error":{"code":"bad"}}`, wantErr: "bad"},
		{name: "非 JSON 失敗", out: "boom", err: errors.New("exit status 2"), wantErr: "boom"},
		{name: "非 JSON 成功", out: "done"},
		{name: "stderr 警告不影響 JSON 解析", out: `{"ok":true,"result":{"terminal":{"handle":"h2"}}}`, stderr: "warning: deprecated", wantHandle: "h2"},
		{name: "失敗訊息帶 stderr", out: "", stderr: "orca: runtime not running", err: errors.New("exit status 1"), wantErr: "runtime not running"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var gotArgv []string
			run := func(_ context.Context, argv []string) ([]byte, []byte, error) {
				gotArgv = argv
				return []byte(c.out), []byte(c.stderr), c.err
			}
			h, err := o.OpenTab(run, "t", " ssh x")
			if gotArgv[0] != "/b/orca" {
				t.Errorf("argv0 = %q", gotArgv[0])
			}
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil || h != c.wantHandle {
				t.Fatalf("handle=%q err=%v", h, err)
			}
		})
	}
}

// 名稱以 -- 開頭時，--title=… 形式讓 orca CLI 不會把值當成旗標。
func TestOrcaCreateArgvDashValue(t *testing.T) {
	got := OrcaEnv{BinDir: "/b"}.CreateArgv("--help", " --x")
	if got[5] != "--title=--help" || got[6] != "--command= --x" {
		t.Fatalf("argv = %q", got)
	}
}

// tunnel 的分頁指令：ssh 結束後印出結束碼、等待後 exit；用 zsh -f 與 bash 實際解析執行。
func TestTunnelEpilogueRunsInShells(t *testing.T) {
	old := TunnelCloseDelay
	TunnelCloseDelay = 0
	defer func() { TunnelCloseDelay = old }()
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake login.exp")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nfor a in \"$@\"; do printf 'ARG[%s]\\n' \"$a\"; done\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	h := store.Host{ID: "h_1", Name: "n", Host: "10.0.0.1", Port: 22, User: "root", Auth: "password", Password: `p w'$x*`}
	tun := store.Tunnel{Name: "db", HostID: "h_1", Rules: []store.Rule{{Type: "D", BindPort: 1080}}}
	p, err := TunnelPlan(h, tun, fake)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Text, " ") {
		t.Fatalf("開頭應有空白：%q", p.Text)
	}
	var want strings.Builder
	for _, a := range p.Argv[1:] {
		want.WriteString("ARG[" + a + "]\n")
	}
	for _, shell := range [][]string{{"zsh", "-f", "-c"}, {"bash", "-c"}} {
		bin, err := exec.LookPath(shell[0])
		if err != nil {
			t.Logf("略過 %s：%v", shell[0], err)
			continue
		}
		cmd := exec.Command(bin, append(shell[1:], p.Text)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 7 {
			t.Errorf("%s: 結束碼應為 7，err=%v\n%s", shell[0], err, out)
		}
		if !strings.HasPrefix(string(out), want.String()) {
			t.Errorf("%s: argv 還原不一致：\n%s", shell[0], out)
		}
		if !strings.Contains(string(out), "Tunnel 已結束（代碼 7），0 秒後關閉此分頁") {
			t.Errorf("%s: 缺結束訊息：\n%s", shell[0], out)
		}
	}
}
