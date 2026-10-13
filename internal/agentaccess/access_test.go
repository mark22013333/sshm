package agentaccess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mark22013333/sshm/internal/connect"
	"github.com/mark22013333/sshm/internal/store"
)

// fakeRemote 以本機 sh 在 remoteHome 執行遠端腳本，模擬一台 VM；fail 非 nil 時模擬連不上。
type fakeRemote struct {
	home  string
	fail  error
	calls []string // 每次呼叫的帳號
}

func (r *fakeRemote) run(_ context.Context, h store.Host, user, script string) (string, error) {
	r.calls = append(r.calls, user)
	if r.fail != nil {
		return "ssh: connect to host " + h.Host + " port 22: Connection refused", r.fail
	}
	cmd := exec.Command("/bin/sh", "-c", RemoteCommand(script))
	cmd.Env = append(os.Environ(), "HOME="+r.home)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type fixture struct {
	m      *Manager
	remote *fakeRemote
	path   string
	verify []string
}

func newFixture(t *testing.T, hosts ...store.Host) *fixture {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("沒有 ssh-keygen")
	}
	dir := t.TempDir()
	fx := &fixture{remote: &fakeRemote{home: t.TempDir()}, path: filepath.Join(dir, "sshm", "hosts.json")}
	f := store.NewFile()
	f.Hosts = hosts
	if err := store.Save(fx.path, f); err != nil {
		t.Fatal(err)
	}
	fx.m = &Manager{
		Paths:     PathsFor(filepath.Join(dir, "home"), "/nonexistent"),
		HostsPath: fx.path,
		Remote:    fx.remote.run,
		Keygen:    connect.ExecRunner,
		Verify: func(_ context.Context, alias string) error {
			fx.verify = append(fx.verify, alias)
			return nil
		},
	}
	return fx
}

func (fx *fixture) file(t *testing.T) *store.File {
	t.Helper()
	f, err := store.Open(fx.path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func pwHost() store.Host {
	return store.Host{ID: "h_1", Name: "範例客戶-PROD-VM", Group: "範例客戶", Host: "10.0.0.24", Port: 2222, User: "helpdesk", Auth: store.AuthPassword, Password: "test-pass"}
}

func TestCheckEligible(t *testing.T) {
	cases := []struct {
		name string
		mod  func(h *store.Host)
		want string
	}{
		{"可開通", func(h *store.Host) {}, ""},
		{"key 認證", func(h *store.Host) { h.Auth, h.IdentityFile, h.Password = store.AuthKey, "/k", "" }, "只支援 password"},
		{"none 認證", func(h *store.Host) { h.Auth, h.Password = store.AuthNone, "" }, "只支援 password"},
		{"沒密碼", func(h *store.Host) { h.Password = "" }, "沒有存密碼"},
		{"自訂指令", func(h *store.Host) { h.CustomCommand = "ssh x" }, "自訂指令"},
		{"額外參數", func(h *store.Host) { h.ExtraArgs = "-J jump" }, "額外 ssh 參數"},
	}
	for _, c := range cases {
		h := pwHost()
		c.mod(&h)
		err := CheckEligible(h)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestFindHost(t *testing.T) {
	f := &store.File{Hosts: []store.Host{
		{ID: "h_1", Name: "範例-A", User: "u", Host: "10.0.0.1"},
		{ID: "h_2", Name: "範例-a", User: "u", Host: "10.0.0.2"},
		{ID: "h_3", Name: "dup", User: "u", Host: "10.0.0.3"},
		{ID: "h_4", Name: "dup", User: "u", Host: "10.0.0.4"},
		{ID: "h_5", Name: "Only", User: "u", Host: "10.0.0.5"},
	}}
	if h, err := FindHost(f, "範例-a"); err != nil || h.ID != "h_2" {
		t.Fatalf("完全相同優先：%v %v", h.ID, err)
	}
	if h, err := FindHost(f, "only"); err != nil || h.ID != "h_5" {
		t.Fatalf("不分大小寫：%v %v", h.ID, err)
	}
	if _, err := FindHost(f, "dup"); err == nil || !strings.Contains(err.Error(), "h_3") {
		t.Fatalf("同名應報錯：%v", err)
	}
	if _, err := FindHost(f, "none"); err == nil {
		t.Fatal("找不到應報錯")
	}
}

func TestEnableFullFlow(t *testing.T) {
	other := store.Host{ID: "h_2", Name: "範例客戶-PROD-VM", Host: "10.0.0.25", User: "helpdesk", Auth: store.AuthPassword, Password: "test-pass",
		Agent: &store.AgentAccess{Alias: "sshm-prod-vm", KeyFile: "/x/sshm-prod-vm"}}
	fx := newFixture(t, pwHost(), other)
	ctx := context.Background()
	plan, err := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Alias != "sshm-prod-vm-2" || plan.User != "helpdesk" || plan.UserSet || !plan.Key.Created {
		t.Fatalf("plan = %+v", plan)
	}
	summary := strings.Join(plan.Summary(), "\n")
	for _, want := range []string{"範例客戶-PROD-VM", "10.0.0.24:2222", "helpdesk", plan.Key.Fingerprint, "sshm-prod-vm-2", fx.m.Paths.Conf, fx.m.Paths.Config} {
		if !strings.Contains(summary, want) {
			t.Errorf("確認內容缺 %q：\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "test-pass") {
		t.Fatal("確認內容不得含密碼")
	}
	res, err := fx.m.Enable(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if res.Remote != "changed" || len(fx.verify) != 1 || fx.verify[0] != "sshm-prod-vm-2" {
		t.Fatalf("res=%+v verify=%v", res, fx.verify)
	}
	h := fx.file(t).Hosts[0]
	if h.Agent == nil || h.Agent.Alias != "sshm-prod-vm-2" || h.Agent.User != "" || h.Agent.KeyFile != plan.Key.Private {
		t.Fatalf("hosts.json agent = %+v", h.Agent)
	}
	ak, _ := os.ReadFile(filepath.Join(fx.remote.home, ".ssh", "authorized_keys"))
	if string(ak) != AuthorizedKeysLine(plan.Key.TypeBlob, "sshm-prod-vm-2")+"\n" {
		t.Fatalf("遠端 authorized_keys = %q", ak)
	}
	conf, _ := os.ReadFile(fx.m.Paths.Conf)
	if !strings.Contains(string(conf), "Host sshm-prod-vm-2\n  HostName 10.0.0.24\n  User helpdesk\n  Port 2222\n") {
		t.Fatalf("conf = %s", conf)
	}
	cfg, _ := os.ReadFile(fx.m.Paths.Config)
	if !strings.HasPrefix(string(cfg), "Include ") {
		t.Fatalf("config = %q", cfg)
	}
	// 重複開通：沿用金鑰、遠端不變
	plan2, err := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	if err != nil || !plan2.Reinstall || plan2.Key.Created || plan2.Key.Fingerprint != plan.Key.Fingerprint {
		t.Fatalf("plan2 = %+v err=%v", plan2, err)
	}
	if res, err := fx.m.Enable(ctx, plan2); err != nil || res.Remote != "unchanged" {
		t.Fatalf("重複開通 res=%+v err=%v", res, err)
	}
	if _, err := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "sshm-other", ""); err == nil {
		t.Fatal("已開通時換別名應拒絕")
	}
}

func TestEnableWithUserAndAlias(t *testing.T) {
	fx := newFixture(t, pwHost())
	ctx := context.Background()
	if _, err := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "Bad Alias", ""); err == nil {
		t.Fatal("不合法別名應拒絕")
	}
	if _, err := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "-oProxyCommand=x"); err == nil {
		t.Fatal("不合法帳號應拒絕")
	}
	plan, err := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "my-vm", "readonly")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.UserSet || plan.User != "readonly" || !strings.Contains(strings.Join(plan.Summary(), "\n"), "密碼原本屬於 helpdesk") {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := fx.m.Enable(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if got := fx.remote.calls; len(got) != 1 || got[0] != "readonly" {
		t.Fatalf("遠端登入帳號 = %v", got)
	}
	h := fx.file(t).Hosts[0]
	if h.Agent.User != "readonly" || h.Agent.Alias != "my-vm" {
		t.Fatalf("agent = %+v", h.Agent)
	}
}

func TestPrepareRejectsAliasDefinedByUser(t *testing.T) {
	fx := newFixture(t, pwHost())
	_ = os.MkdirAll(fx.m.Paths.SSHDir, 0o700)
	_ = os.WriteFile(fx.m.Paths.Config, []byte("Host sshm-prod-vm\n  HostName 10.0.0.1\n"), 0o600)
	if _, err := fx.m.PrepareEnable(context.Background(), fx.file(t), "h_1", "", ""); err == nil || !strings.Contains(err.Error(), "--alias") {
		t.Fatalf("err = %v", err)
	}
}

func TestPrepareRejectsUnsupported(t *testing.T) {
	h := pwHost()
	h.ExtraArgs = "-J jump"
	fx := newFixture(t, h)
	if _, err := fx.m.PrepareEnable(context.Background(), fx.file(t), "h_1", "", ""); err == nil || !strings.Contains(err.Error(), "額外 ssh 參數") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(fx.m.Paths.KeyDir); !os.IsNotExist(err) {
		t.Fatal("被拒絕時不應產生金鑰")
	}
}

func TestEnableRemoteFailureLeavesNothing(t *testing.T) {
	fx := newFixture(t, pwHost())
	fx.remote.fail = errors.New("ssh 結束碼非 0")
	ctx := context.Background()
	plan, err := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fx.m.Enable(ctx, plan)
	if err == nil || !strings.Contains(err.Error(), "本機未做任何變更") || !strings.Contains(err.Error(), "Connection refused") {
		t.Fatalf("err = %v", err)
	}
	if fx.file(t).Hosts[0].Agent != nil {
		t.Fatal("失敗時不應寫入開通狀態")
	}
	for _, p := range []string{plan.Key.Private, plan.Key.Public, fx.m.Paths.Conf, fx.m.Paths.Config} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("失敗後不應留下 %s", p)
		}
	}
}

func TestEnableVerifyFailureRollsBack(t *testing.T) {
	fx := newFixture(t, pwHost())
	fx.m.Verify = func(context.Context, string) error { return errors.New("以別名連線驗證失敗") }
	ctx := context.Background()
	plan, _ := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	_, err := fx.m.Enable(ctx, plan)
	if err == nil || !strings.Contains(err.Error(), "已復原為未開通") {
		t.Fatalf("err = %v", err)
	}
	if fx.file(t).Hosts[0].Agent != nil {
		t.Fatal("驗證失敗應清除開通狀態")
	}
	ak, _ := os.ReadFile(filepath.Join(fx.remote.home, ".ssh", "authorized_keys"))
	if strings.Contains(string(ak), "sshm-agent:") {
		t.Fatalf("遠端公鑰應移除：%q", ak)
	}
	conf, _ := os.ReadFile(fx.m.Paths.Conf)
	if strings.Contains(string(conf), "Host sshm-") {
		t.Fatalf("conf 應移除區塊：%s", conf)
	}
	if _, err := os.Stat(plan.Key.Private); !os.IsNotExist(err) {
		t.Fatal("新產生的金鑰應刪除")
	}
}

func TestEnableWarnsMisplacedInclude(t *testing.T) {
	fx := newFixture(t, pwHost())
	_ = os.MkdirAll(fx.m.Paths.SSHDir, 0o700)
	userCfg := "Host *\n  User me\nInclude " + fx.m.Paths.IncludeArg + "\n"
	_ = os.WriteFile(fx.m.Paths.Config, []byte(userCfg), 0o600)
	ctx := context.Background()
	plan, _ := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	res, err := fx.m.Enable(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "移到檔案最上方") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
	if got, _ := os.ReadFile(fx.m.Paths.Config); string(got) != userCfg {
		t.Fatalf("不應改動使用者設定：%q", got)
	}
}

func TestDisable(t *testing.T) {
	fx := newFixture(t, pwHost())
	ctx := context.Background()
	_ = os.MkdirAll(filepath.Join(fx.remote.home, ".ssh"), 0o700)
	userKey := "ssh-rsa AAAAB3Nza-user-key user@laptop\n"
	_ = os.WriteFile(filepath.Join(fx.remote.home, ".ssh", "authorized_keys"), []byte(userKey), 0o600)
	plan, _ := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	if _, err := fx.m.Enable(ctx, plan); err != nil {
		t.Fatal(err)
	}
	dp, err := fx.m.PrepareDisable(fx.file(t), "h_1")
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.Join(dp.Summary(fx.m.Paths), "\n"); !strings.Contains(s, "sshm-prod-vm") || !strings.Contains(s, "helpdesk") {
		t.Fatalf("summary = %s", s)
	}
	if st, err := fx.m.DisableRemote(ctx, dp); err != nil || st != "changed" {
		t.Fatalf("remote = %q %v", st, err)
	}
	if ak, _ := os.ReadFile(filepath.Join(fx.remote.home, ".ssh", "authorized_keys")); string(ak) != userKey {
		t.Fatalf("只應刪自己那行：%q", ak)
	}
	removed, err := fx.m.DisableLocal(dp)
	if err != nil || len(removed) != 2 {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	if fx.file(t).Hosts[0].Agent != nil {
		t.Fatal("開通狀態應清除")
	}
	conf, _ := os.ReadFile(fx.m.Paths.Conf)
	if strings.Contains(string(conf), "Host ") {
		t.Fatalf("conf = %s", conf)
	}
	if _, err := fx.m.PrepareDisable(fx.file(t), "h_1"); err == nil {
		t.Fatal("未開通時停用應報錯")
	}
}

func TestDisableRemoteUnreachable(t *testing.T) {
	fx := newFixture(t, pwHost())
	ctx := context.Background()
	plan, _ := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	if _, err := fx.m.Enable(ctx, plan); err != nil {
		t.Fatal(err)
	}
	fx.remote.fail = errors.New("逾時")
	dp, _ := fx.m.PrepareDisable(fx.file(t), "h_1")
	_, err := fx.m.DisableRemote(ctx, dp)
	var re *RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("應回傳 RemoteError：%v", err)
	}
	if fx.file(t).Hosts[0].Agent == nil {
		t.Fatal("遠端失敗時本機狀態不應自動清除（由使用者決定）")
	}
}

func TestListHasNoPasswords(t *testing.T) {
	p := testPaths(t)
	f := agentFile(p)
	entries := p.List(f)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if e := entries[1]; e.Alias != "sshm-a-uat" || e.User != "readonly" || e.Port != 22 {
		t.Fatalf("entry = %+v", e)
	}
	if empty := p.List(&store.File{}); empty == nil || len(empty) != 0 {
		t.Fatal("沒有開通機器時應回傳空陣列（JSON 為 []）")
	}
}

// TestExpectRunnerWithFakeSSH 實際跑內嵌的 expect 腳本與假 ssh：確認密碼自動送出、RemoteCommand 原樣傳到遠端 shell。
func TestExpectRunnerWithFakeSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("expect 只支援 unix")
	}
	if _, err := exec.LookPath("expect"); err != nil {
		t.Skip("沒有 expect")
	}
	fakebin, _ := filepath.Abs("testdata/fakessh")
	remoteHome := t.TempDir()
	t.Setenv("PATH", fakebin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", remoteHome)
	t.Setenv("FAKE_SSH_PASSWORD", "test-pass")
	script, err := connect.EnsureLoginScript(filepath.Join(t.TempDir(), "sshm"))
	if err != nil {
		t.Fatal(err)
	}
	run := ExpectRunner(script)
	h := pwHost()
	status, err := runRemote(context.Background(), run, h, "helpdesk", InstallScript("sshm-a", blobA))
	if err != nil || status != "changed" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	ak, _ := os.ReadFile(filepath.Join(remoteHome, ".ssh", "authorized_keys"))
	if string(ak) != AuthorizedKeysLine(blobA, "sshm-a")+"\n" {
		t.Fatalf("authorized_keys = %q", ak)
	}
	out, err := run(context.Background(), h, "helpdesk", "echo hi")
	if err != nil || !strings.Contains(out, "OPT PubkeyAuthentication=no") || !strings.Contains(out, "LOGGED_IN helpdesk@10.0.0.24") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	// 密碼錯誤：失敗且輸出不含密碼
	h.Password = "wrong-pass"
	_, err = runRemote(context.Background(), run, h, "helpdesk", RemoveScript(blobA))
	if err == nil || strings.Contains(err.Error(), "wrong-pass") {
		t.Fatalf("err = %v", err)
	}
}

// 公鑰檔遺失時無從辨識遠端那一行：不得猜測（例如改用註解比對），回報錯誤讓使用者決定只清本機。
func TestDisableRemoteNeedsPublicKey(t *testing.T) {
	fx := newFixture(t, pwHost())
	ctx := context.Background()
	plan, _ := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	if _, err := fx.m.Enable(ctx, plan); err != nil {
		t.Fatal(err)
	}
	ak := filepath.Join(fx.remote.home, ".ssh", "authorized_keys")
	before, _ := os.ReadFile(ak)
	if err := os.Rename(plan.Key.Public, plan.Key.Public+".moved"); err != nil {
		t.Fatal(err)
	}
	dp, _ := fx.m.PrepareDisable(fx.file(t), "h_1")
	if _, err := fx.m.DisableRemote(ctx, dp); err == nil || !strings.Contains(err.Error(), ".pub") {
		t.Fatalf("err = %v", err)
	}
	if after, _ := os.ReadFile(ak); string(after) != string(before) {
		t.Fatal("無法辨識時不應改動遠端")
	}
}

// 審查 #4：ExpectRunner 啟動的行程 argv 不含密碼，密碼只經環境變數交給 expect。
func TestExpectRunnerArgvHasNoPassword(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("只支援 unix")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	fake := filepath.Join(dir, "fake-login.exp")
	body := "#!/bin/sh\nprintf 'ARG[%s]\\n' \"$@\" > \"" + rec + "\"\nprintf 'ENV[%s]\\n' \"$" + connect.PasswordEnv + "\" >> \"" + rec + "\"\necho SSHM_AGENT_OK unchanged\n"
	if err := os.WriteFile(fake, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	h := pwHost()
	h.Password = "test-pass-Argv"
	if _, err := runRemote(context.Background(), ExpectRunner(fake), h, "helpdesk", RemoveScript(blobA)); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(rec)
	if strings.Contains(strings.Split(string(got), "ENV[")[0], h.Password) {
		t.Fatalf("argv 含密碼：\n%s", got)
	}
	if !strings.Contains(string(got), "ENV["+h.Password+"]") {
		t.Fatalf("expect 應從環境變數拿到密碼：\n%s", got)
	}
}

func TestPrepareRejectsUnsafeUserFlag(t *testing.T) {
	fx := newFixture(t, pwHost())
	for _, u := range unsafeConfValues {
		if _, err := fx.m.PrepareEnable(context.Background(), fx.file(t), "h_1", "", u); err == nil {
			t.Errorf("--user %q 應拒絕", u)
		}
	}
}

// locRemote 依「帳號@主機:埠」各給一個遠端家目錄，模擬機器搬家前後的兩個位置。
type locRemote struct {
	t     *testing.T
	homes map[string]string
	fail  map[string]bool
	calls []string
}

func (r *locRemote) home(loc string) string {
	if r.homes[loc] == "" {
		r.homes[loc] = r.t.TempDir()
	}
	return r.homes[loc]
}

func (r *locRemote) run(_ context.Context, h store.Host, user, script string) (string, error) {
	loc := fmt.Sprintf("%s@%s:%d", user, h.Host, effectivePort(h.Port))
	r.calls = append(r.calls, loc)
	if r.fail[loc] {
		return "ssh: connect to host " + h.Host + ": Connection timed out", errors.New("逾時")
	}
	cmd := exec.Command("/bin/sh", "-c", RemoteCommand(script))
	cmd.Env = append(os.Environ(), "HOME="+r.home(loc))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (r *locRemote) ak(loc string) string {
	data, _ := os.ReadFile(filepath.Join(r.home(loc), ".ssh", "authorized_keys"))
	return string(data)
}

func moveHost(t *testing.T, fx *fixture, host string, port int, user string) {
	t.Helper()
	if _, err := store.Update(fx.path, func(f *store.File) error {
		f.Hosts[0].Host, f.Hosts[0].Port, f.Hosts[0].User = host, port, user
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// 審查 #5：開通後改了 host／user，disable 要同時清掉舊位置與新位置的公鑰。
func TestDisableCleansOldAndNewLocations(t *testing.T) {
	fx := newFixture(t, pwHost())
	r := &locRemote{t: t, homes: map[string]string{}, fail: map[string]bool{}}
	fx.m.Remote = r.run
	ctx := context.Background()
	plan, _ := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	if _, err := fx.m.Enable(ctx, plan); err != nil {
		t.Fatal(err)
	}
	oldLoc, newLoc := "helpdesk@10.0.0.24:2222", "ops@10.0.0.99:22"
	if !strings.Contains(r.ak(oldLoc), plan.Key.TypeBlob) {
		t.Fatal("舊位置應已安裝公鑰")
	}
	moveHost(t, fx, "10.0.0.99", 22, "ops")
	// 新位置重新開通
	plan2, err := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.m.Enable(ctx, plan2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.ak(newLoc), plan.Key.TypeBlob) {
		t.Fatal("新位置應已安裝公鑰")
	}
	dp, err := fx.m.PrepareDisable(fx.file(t), "h_1")
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.Join(dp.Summary(fx.m.Paths), "\n"); !strings.Contains(s, oldLoc) || !strings.Contains(s, newLoc) {
		t.Fatalf("停用確認應列出兩個位置：\n%s", s)
	}
	if _, err := fx.m.DisableRemote(ctx, dp); err != nil {
		t.Fatal(err)
	}
	for _, loc := range []string{oldLoc, newLoc} {
		if strings.Contains(r.ak(loc), plan.Key.TypeBlob) {
			t.Errorf("%s 的公鑰應已清除：%q", loc, r.ak(loc))
		}
	}
}

// 改了位置但沒重新開通就停用：舊位置照樣清除，新位置沒裝過也不算失敗。
func TestDisableAfterMoveWithoutReenable(t *testing.T) {
	fx := newFixture(t, pwHost())
	r := &locRemote{t: t, homes: map[string]string{}, fail: map[string]bool{}}
	fx.m.Remote = r.run
	ctx := context.Background()
	plan, _ := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	if _, err := fx.m.Enable(ctx, plan); err != nil {
		t.Fatal(err)
	}
	moveHost(t, fx, "10.0.0.99", 2222, "helpdesk")
	dp, _ := fx.m.PrepareDisable(fx.file(t), "h_1")
	if _, err := fx.m.DisableRemote(ctx, dp); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.ak("helpdesk@10.0.0.24:2222"), plan.Key.TypeBlob) {
		t.Fatal("舊位置的公鑰應已清除")
	}
	// 舊位置連不到：回報哪個位置失敗，交給使用者決定是否只清本機
	fx2 := newFixture(t, pwHost())
	r2 := &locRemote{t: t, homes: map[string]string{}, fail: map[string]bool{}}
	fx2.m.Remote = r2.run
	plan, _ = fx2.m.PrepareEnable(ctx, fx2.file(t), "h_1", "", "")
	if _, err := fx2.m.Enable(ctx, plan); err != nil {
		t.Fatal(err)
	}
	moveHost(t, fx2, "10.0.0.99", 2222, "helpdesk")
	r2.fail["helpdesk@10.0.0.24:2222"] = true
	dp, _ = fx2.m.PrepareDisable(fx2.file(t), "h_1")
	_, err := fx2.m.DisableRemote(ctx, dp)
	var re *RemoteError
	if !errors.As(err, &re) || !strings.Contains(err.Error(), "10.0.0.24") {
		t.Fatalf("應回報舊位置失敗：%v", err)
	}
}

func TestEnableRecordsInstallLocation(t *testing.T) {
	fx := newFixture(t, pwHost())
	r := &locRemote{t: t, homes: map[string]string{}, fail: map[string]bool{}}
	fx.m.Remote = r.run
	ctx := context.Background()
	plan, _ := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	if _, err := fx.m.Enable(ctx, plan); err != nil {
		t.Fatal(err)
	}
	want := store.AgentInstall{Host: "10.0.0.24", Port: 2222, User: "helpdesk", Fingerprint: plan.Key.Fingerprint}
	if got := fx.file(t).Hosts[0].Agent.Installed; len(got) != 1 || got[0] != want {
		t.Fatalf("installed = %+v", got)
	}
	if Relocated(fx.file(t).Hosts[0]) {
		t.Fatal("未搬家不應判定為位置變更")
	}
	moveHost(t, fx, "10.0.0.99", 2222, "helpdesk")
	if !Relocated(fx.file(t).Hosts[0]) {
		t.Fatal("改了 host 應判定為位置變更")
	}
	// 重新開通到新位置：兩個位置都記錄
	plan2, _ := fx.m.PrepareEnable(ctx, fx.file(t), "h_1", "", "")
	if _, err := fx.m.Enable(ctx, plan2); err != nil {
		t.Fatal(err)
	}
	if got := fx.file(t).Hosts[0].Agent.Installed; len(got) != 2 || got[0].Host != "10.0.0.24" || got[1].Host != "10.0.0.99" {
		t.Fatalf("installed = %+v", got)
	}
}
