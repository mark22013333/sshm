package connect

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mark22013333/sshm/assets"
	"github.com/mark22013333/sshm/internal/store"
)

func TestEnsureLoginScript(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sshm")
	path, err := EnsureLoginScript(dir)
	if err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, assets.LoginScript) {
			t.Fatal("script content differs from embedded")
		}
		st, _ := os.Stat(path)
		if st.Mode().Perm() != 0o700 {
			t.Fatalf("perm = %o", st.Mode().Perm())
		}
	}
	check()
	// 內容被改過 → 覆寫回內嵌版本
	if err := os.WriteFile(path, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureLoginScript(dir); err != nil {
		t.Fatal(err)
	}
	check()
	// 內容相同但權限不對 → 修正權限
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureLoginScript(dir); err != nil {
		t.Fatal(err)
	}
	check()
}

// loginRun 是以假 ssh 執行中的 expect 腳本。
type loginRun struct {
	t        *testing.T
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	chunks   chan string
	buf      string // 至今所有輸出
	pos      int    // 已消化到的位置
	done     bool
	exitCode int
}

// startLogin 把 fakebinDir 前置到 PATH，實際執行內嵌的 expect 腳本。
func startLogin(t *testing.T, fakebinDir string, h store.Host, env ...string) *loginRun {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("expect 腳本只支援 unix")
	}
	if _, err := exec.LookPath("expect"); err != nil {
		t.Skip("系統沒有 expect")
	}
	fakebin, err := filepath.Abs(fakebinDir)
	if err != nil {
		t.Fatal(err)
	}
	scriptPath, err := EnsureLoginScript(filepath.Join(t.TempDir(), "sshm"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := HostPlan(h, scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(plan.Argv[0], plan.Argv[1:]...)
	cmd.Env = append(os.Environ(), "PATH="+fakebin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r := &loginRun{t: t, cmd: cmd, stdin: stdin, chunks: make(chan string, 64)}
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := stdout.Read(b)
			if n > 0 {
				r.chunks <- string(b[:n])
			}
			if err != nil {
				close(r.chunks)
				return
			}
		}
	}()
	t.Cleanup(r.finish)
	return r
}

// until 持續讀輸出直到 found 在未消化的部分找到結果（回傳新的 pos 與值）。
func (r *loginRun) until(what string, found func(rest string) (int, string, bool)) string {
	r.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		if n, v, ok := found(r.buf[r.pos:]); ok {
			r.pos += n
			return v
		}
		select {
		case c, ok := <-r.chunks:
			if !ok {
				r.t.Fatalf("expect 提早結束，等不到 %q；輸出：%q", what, r.buf)
			}
			r.buf += c
		case <-timeout:
			_ = r.cmd.Process.Kill()
			r.t.Fatalf("逾時等不到 %q；輸出：%q", what, r.buf)
		}
	}
}

// waitFor 等到含 marker 的完整一行，回傳 marker 之後到行尾的內容。
func (r *loginRun) waitFor(marker string) string {
	r.t.Helper()
	return r.until(marker, func(rest string) (int, string, bool) {
		i := strings.Index(rest, marker)
		if i < 0 {
			return 0, "", false
		}
		j := strings.IndexByte(rest[i:], '\n')
		if j < 0 {
			return 0, "", false
		}
		return i + j + 1, strings.TrimRight(rest[i+len(marker):i+j], "\r"), true
	})
}

// waitText 等到輸出出現 marker（不需換行，例如密碼提示）。
func (r *loginRun) waitText(marker string) {
	r.t.Helper()
	r.until(marker, func(rest string) (int, string, bool) {
		i := strings.Index(rest, marker)
		if i < 0 {
			return 0, "", false
		}
		return i + len(marker), "", true
	})
}

// finish 關閉 stdin 讓 interact 讀到 EOF 後結束，回傳 expect 的結束碼（逾時為 -1）。
func (r *loginRun) finish() {
	if r.done {
		return
	}
	r.done = true
	_ = r.stdin.Close()
	go func() {
		for range r.chunks {
		}
	}()
	done := make(chan struct{})
	go func() { _ = r.cmd.Wait(); close(done) }()
	select {
	case <-done:
		r.exitCode = r.cmd.ProcessState.ExitCode()
	case <-time.After(5 * time.Second):
		_ = r.cmd.Process.Kill()
		r.exitCode = -1
	}
}

func (r *loginRun) send(s string) {
	r.t.Helper()
	if _, err := io.WriteString(r.stdin, s); err != nil {
		r.t.Fatal(err)
	}
}

// 正常密碼提示：參數原樣傳遞、密碼完整送達、提示文字仍顯示給使用者。
func TestLoginScriptSendsPasswordAtPrompt(t *testing.T) {
	password := `p@ss w0rd'"$HOME[x]\`
	r := startLogin(t, "../../testdata/fakebin-target", store.Host{Name: "n", Host: "10.0.0.24", Port: 2222, User: "helpdesk",
		Auth: "password", Password: password, ExtraArgs: `-J jump -o "ServerAliveInterval=30"`})
	args := r.waitFor("ARGS: ")
	got := r.waitFor("GOT_PASSWORD=[")

	wantArgs := "-o StrictHostKeyChecking=accept-new -p 2222 -J jump -o ServerAliveInterval=30 -- helpdesk@10.0.0.24"
	if args != wantArgs {
		t.Errorf("ssh args = %q\nwant       %q", args, wantArgs)
	}
	if want := password + "]"; got != want {
		t.Errorf("password = %q, want %q", got, want)
	}
	if !strings.Contains(r.buf, "password:") {
		t.Errorf("密碼提示應顯示給使用者；輸出：%q", r.buf)
	}
}

// MOTD 中段出現 password: 不送密碼，且使用者按鍵立即轉送（不必等 timeout）。
func TestLoginScriptIgnoresPasswordInMOTD(t *testing.T) {
	r := startLogin(t, "../../testdata/fakebin-motd", store.Host{Name: "n", Host: "h", Port: 22, User: "u",
		Auth: "password", Password: "SECRET"})
	r.waitFor("Change password:")
	start := time.Now()
	if _, err := io.WriteString(r.stdin, "typed\n"); err != nil {
		t.Fatal(err)
	}
	got := r.waitFor("GOT_LINE=[")
	if got != "typed]" {
		t.Fatalf("遠端收到 %q，want %q（密碼被誤送？）", got, "typed]")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("使用者輸入延遲 %v，按鍵被吞", d)
	}
	if strings.Contains(r.buf, "SECRET") {
		t.Fatalf("密碼不應出現在輸出：%q", r.buf)
	}
}

var secretHost = store.Host{Name: "n", Host: "h", Port: 22, User: "u", Auth: "password", Password: "SECRET"}

// 密碼提示分兩次寫出（"pass" + "word: "）仍要送出。
func TestLoginScriptSplitPrompt(t *testing.T) {
	r := startLogin(t, "../../testdata/rv-split", secretHost)
	if got := r.waitFor("GOT_PASSWORD=["); got != "SECRET]" {
		t.Fatalf("got %q", got)
	}
}

// 提示後接 ANSI 色碼仍視為密碼提示。
func TestLoginScriptPromptWithANSI(t *testing.T) {
	r := startLogin(t, "../../testdata/rv-ansi", secretHost)
	if got := r.waitFor("GOT_PASSWORD=["); got != "SECRET]" {
		t.Fatalf("got %q", got)
	}
}

// MOTD 的「password:」與換行分兩次寫出時不送密碼。
func TestLoginScriptMOTDSplitWrite(t *testing.T) {
	r := startLogin(t, "../../testdata/rv-motdsplit", secretHost)
	r.waitFor("Please change password:")
	r.send("typed\n")
	if got := r.waitFor("GOT_LINE=["); got != "typed]" {
		t.Fatalf("遠端收到 %q（密碼被誤送？）", got)
	}
}

// 登入時沒有密碼提示（金鑰通過），之後 su 的 Password: 不自動填。
func TestLoginScriptNoAutofillAfterUserInput(t *testing.T) {
	r := startLogin(t, "../../testdata/rv-noprompt", secretHost)
	r.waitFor("Last login")
	r.send("cmd\n")
	r.waitFor("RAN=[")
	r.send("manual\n")
	if got := r.waitFor("SU_GOT=["); got != "manual]" {
		t.Fatalf("su 收到 %q，want manual", got)
	}
}

// 超過登入時窗才出現的密碼提示不自動填（時窗以 SSHM_LOGIN_WINDOW 縮短為 1 秒）。
func TestLoginScriptNoAutofillAfterWindow(t *testing.T) {
	r := startLogin(t, "../../testdata/rv-late", secretHost, "SSHM_LOGIN_WINDOW=1")
	r.waitFor("Last login")
	time.Sleep(3500 * time.Millisecond)
	r.send("manual\n")
	if got := r.waitFor("GOT_PASSWORD=["); got != "manual]" {
		t.Fatalf("got %q，want manual", got)
	}
}

// 密碼錯誤的第二次提示不重送，交給使用者輸入。
func TestLoginScriptWrongPasswordNotResent(t *testing.T) {
	r := startLogin(t, "../../testdata/rv-wrong", secretHost)
	if got := r.waitFor("FIRST=["); got != "SECRET]" {
		t.Fatalf("first = %q", got)
	}
	r.waitFor("Permission denied")
	r.send("again\n")
	if got := r.waitFor("SECOND=["); got != "again]" {
		t.Fatalf("second = %q", got)
	}
}

func TestLoginScriptExitCodes(t *testing.T) {
	cases := []struct {
		dir  string
		want int
	}{
		{"../../testdata/rv-exit", 7},
		{"../../testdata/rv-signal", 128 + 15},
	}
	for _, c := range cases {
		r := startLogin(t, c.dir, secretHost)
		r.waitFor("READY")
		r.finish()
		if r.exitCode != c.want {
			t.Errorf("%s: exit = %d, want %d", c.dir, r.exitCode, c.want)
		}
	}
}

// MOTD 中出現目標格式的提示、且與後文分兩次寫出：靜止確認期間有新輸出就不送。
func TestLoginScriptMOTDTargetSplitWrite(t *testing.T) {
	r := startLogin(t, "../../testdata/rv-motdtarget", secretHost)
	r.waitFor("expires in 3 days")
	r.send("typed\n")
	if got := r.waitFor("GOT_LINE=["); got != "typed]" {
		t.Fatalf("遠端收到 %q（密碼被誤送？）", got)
	}
}

var scenHost = store.Host{Name: "n", Host: "host", Port: 22, User: "user", Auth: "password", Password: "SECRET"}

// ProxyJump：跳板機的提示不送（使用者自己輸入），隨後目標機的提示自動送。
func TestLoginScriptProxyJumpPromptNotSent(t *testing.T) {
	h := scenHost
	h.Host = "target"
	r := startLogin(t, "../../testdata/rv-scen", h, "SCEN=jump")
	r.waitText("jumpuser@jumphost's password: ")
	time.Sleep(1200 * time.Millisecond) // 超過靜止確認時間，確定沒有誤送
	r.send("JUMPPW\n")
	if got := r.waitFor("JUMP1=["); got != "JUMPPW]" {
		t.Fatalf("跳板機收到 %q，want JUMPPW（目標密碼被送給跳板機？）", got)
	}
	if got := r.waitFor("TARGET=["); got != "SECRET]" {
		t.Fatalf("目標機收到 %q，want SECRET", got)
	}
}

// OpenSSH 三種提示格式都要送出。
func TestLoginScriptPromptFormats(t *testing.T) {
	for _, scen := range []string{"args", "kbd", "pwfor"} {
		t.Run(scen, func(t *testing.T) {
			r := startLogin(t, "../../testdata/rv-scen", scenHost, "SCEN="+scen)
			if got := r.waitFor("PW=["); got != "SECRET]" {
				t.Fatalf("got %q", got)
			}
		})
	}
}

// 不含 user@host 的提示，或 host 只是「看起來像」（. 不能當 regex 萬用字元）都不送。
func TestLoginScriptNonTargetPromptsNotSent(t *testing.T) {
	cases := []struct{ name, host, prompt string }{
		{"純 Password:", "host", "Password: "},
		{"host 的 . 不當萬用字元", "10.0.0.1", "user@10x0y0z1's password: "},
		{"jumpuser 不等於 user", "host", "jumpuser@host's password: "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := scenHost
			h.Host = c.host
			r := startLogin(t, "../../testdata/rv-scen", h, "SCEN=custom", "PROMPT="+c.prompt)
			r.waitText(c.prompt)
			time.Sleep(1200 * time.Millisecond)
			r.send("manual\n")
			if got := r.waitFor("PW=["); got != "manual]" {
				t.Fatalf("got %q，want manual", got)
			}
		})
	}
}

// host 含 regex 特殊字元時以字串比對，仍能正確送出。
func TestLoginScriptHostWithSpecialChars(t *testing.T) {
	h := scenHost
	h.Host = "a+b[1].example"
	r := startLogin(t, "../../testdata/rv-scen", h, "SCEN=custom", "PROMPT=user@a+b[1].example's password: ")
	if got := r.waitFor("PW=["); got != "SECRET]" {
		t.Fatalf("got %q", got)
	}
}

// 提示出現後 0.3 秒內使用者開始打字：不送密碼，使用者打的字照常送達。
func TestLoginScriptTypingDuringConfirm(t *testing.T) {
	r := startLogin(t, "../../testdata/rv-scen", scenHost, "SCEN=typing")
	r.waitText("user@host's password: ")
	time.Sleep(300 * time.Millisecond)
	r.send("MYTYPED\n")
	if got := r.waitFor("PW=["); got != "MYTYPED]" {
		t.Fatalf("PW = %q，want MYTYPED（密碼被自動送出？）", got)
	}
	r.waitText("$ ")
	r.send("ls\n")
	if got := r.waitFor("SHELLLINE=["); got != "ls]" {
		t.Fatalf("SHELLLINE = %q", got)
	}
}

// SSHM_LOGIN_WINDOW 不是整數時退回 20 秒（2 秒後出現的提示仍會送出）。
func TestLoginScriptInvalidWindowEnv(t *testing.T) {
	for _, v := range []string{"abc", ""} {
		t.Run("值="+v, func(t *testing.T) {
			r := startLogin(t, "../../testdata/rv-late", secretHost, "SSHM_LOGIN_WINDOW="+v)
			if got := r.waitFor("GOT_PASSWORD=["); got != "SECRET]" {
				t.Fatalf("got %q", got)
			}
		})
	}
}
