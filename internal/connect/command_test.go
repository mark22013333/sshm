package connect

import (
	"context"
	"errors"
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
	tun := store.Tunnel{Rules: []store.Rule{
		{Type: "L", BindPort: 13306, TargetHost: "127.0.0.1", TargetPort: 3306},
		{Type: "R", BindAddress: "0.0.0.0", BindPort: 8080, TargetHost: "localhost", TargetPort: 80},
		{Type: "D", BindPort: 1080},
	}}
	h := store.Host{Name: "n", Host: "h", User: "u", Port: 22, Auth: "none", ExtraArgs: "-J j"}
	p, err := TunnelPlan(h, tun, script)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ssh", "-o", "StrictHostKeyChecking=accept-new", "-p", "22",
		"-N", "-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3", "-o", "ExitOnForwardFailure=yes",
		"-L", "13306:127.0.0.1:3306", "-R", "0.0.0.0:8080:localhost:80", "-D", "1080", "-J", "j", "--", "u@h"}
	if !reflect.DeepEqual(p.Argv, want) {
		t.Fatalf("argv = %q", p.Argv)
	}
	if _, err := TunnelPlan(h, store.Tunnel{Rules: []store.Rule{{Type: "L", BindPort: 1}}}, script); err == nil {
		t.Fatal("L without target should fail")
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
