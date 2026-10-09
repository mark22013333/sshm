package connect

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func fakeRun(out, stderr string, err error, gotArgv *[]string) Runner {
	return func(_ context.Context, argv []string) ([]byte, []byte, error) {
		if gotArgv != nil {
			*gotArgv = argv
		}
		return []byte(out), []byte(stderr), err
	}
}

func TestTunnelOrcaArgv(t *testing.T) {
	o := OrcaEnv{BinDir: "/b"}
	if got := o.TunnelCreateArgv(TunnelTitle("--db"), " ssh -N x"); !reflect.DeepEqual(got, []string{
		"/b/orca", "terminal", "create", "--worktree", "active", "--title=⇄ --db", "--command= ssh -N x", "--json"}) {
		t.Errorf("create = %q", got)
	}
	if got := o.ListArgv(); !reflect.DeepEqual(got, []string{"/b/orca", "terminal", "list", "--limit=1000", "--json"}) {
		t.Errorf("list = %q", got)
	}
	if got := o.CloseArgv("term_1"); !reflect.DeepEqual(got, []string{"/b/orca", "terminal", "close", "--terminal=term_1", "--json"}) {
		t.Errorf("close = %q", got)
	}
}

func TestOpenTunnelTabRequiresHandle(t *testing.T) {
	o := OrcaEnv{BinDir: "/b"}
	var argv []string
	h, err := o.OpenTunnelTab(fakeRun(`{"ok":true,"result":{"terminal":{"handle":"term_9"}}}`, "", nil, &argv), "⇄ db", " ssh")
	if err != nil || h != "term_9" || strings.Contains(strings.Join(argv, " "), "--focus") {
		t.Fatalf("h=%q err=%v argv=%q", h, err, argv)
	}
	if _, err := o.OpenTunnelTab(fakeRun(`{"ok":true,"result":{}}`, "", nil, nil), "t", " ssh"); err == nil || !strings.Contains(err.Error(), "handle") {
		t.Fatalf("沒有 handle 應回錯：%v", err)
	}
}

func TestListTerminals(t *testing.T) {
	o := OrcaEnv{BinDir: "/b"}
	set, err := o.ListTerminals(fakeRun(`{"ok":true,"result":{"terminals":[{"handle":"a","title":"zsh","connected":true},{"handle":"b"}],"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}}}`, "warn", nil, nil))
	if err != nil || !set.Complete || !set.Handles["a"] || !set.Handles["b"] || len(set.Handles) != 2 {
		t.Fatalf("set=%+v err=%v", set, err)
	}
	set, _ = o.ListTerminals(fakeRun(`{"ok":true,"result":{"terminals":[],"truncated":true}}`, "", nil, nil))
	if set.Complete {
		t.Fatal("truncated 應標為不完整")
	}
	if _, err := o.ListTerminals(fakeRun(`{"ok":false,"error":{"message":"runtime down"}}`, "", errors.New("exit status 1"), nil)); err == nil || !strings.Contains(err.Error(), "runtime down") {
		t.Fatalf("err = %v", err)
	}
	if _, err := o.ListTerminals(fakeRun(`not json`, "", nil, nil)); err == nil {
		t.Fatal("無法解析應回錯")
	}
}

func TestCloseTerminal(t *testing.T) {
	o := OrcaEnv{BinDir: "/b"}
	if err := o.CloseTerminal(fakeRun(`{"ok":true,"result":{}}`, "", nil, nil), "a"); err != nil {
		t.Fatal(err)
	}
	if err := o.CloseTerminal(fakeRun(`{"ok":false,"error":{"message":"terminal not found"}}`, "", errors.New("exit status 1"), nil), "a"); err == nil || !strings.Contains(err.Error(), "terminal not found") {
		t.Fatalf("err = %v", err)
	}
}

// 清單完整性照 Orca 的 hostScopeCensusIsComplete 與重連中 PTY 不列出的行為判斷。
func TestListTerminalsCompleteness(t *testing.T) {
	o := OrcaEnv{BinDir: "/b"}
	cases := []struct {
		name     string
		result   string
		complete bool
	}{
		{"完整", `"terminals":[],"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}`, true},
		{"只略過 runtime: 主機", `"terminals":[],"truncated":false,"hostScope":{"hostIds":["local","ssh:box"],"omittedHostIds":["runtime:env1"]}`, true},
		{"缺 hostScope（舊版 Orca）", `"terminals":[],"truncated":false`, false},
		{"略過 ssh 主機", `"terminals":[],"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":["ssh:box"]}`, false},
		{"略過無法解析的主機", `"terminals":[],"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":["weird"]}`, false},
		{"hostIds 都無法解析", `"terminals":[],"truncated":false,"hostScope":{"hostIds":["ssh:"],"omittedHostIds":[]}`, false},
		{"truncated", `"terminals":[],"truncated":true,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}`, false},
		{"有 PTY 重連中", `"terminals":[{"handle":"x","connected":false}],"truncated":false,"hostScope":{"hostIds":["local"],"omittedHostIds":[]}`, false},
	}
	for _, c := range cases {
		set, err := o.ListTerminals(fakeRun(`{"ok":true,"result":{`+c.result+`}}`, "", nil, nil))
		if err != nil || set.Complete != c.complete {
			t.Errorf("%s: complete=%v err=%v, want %v", c.name, set.Complete, err, c.complete)
		}
	}
	set, _ := o.ListTerminals(fakeRun(`{"ok":true,"result":{"terminals":[{"handle":"x","connected":false}]}}`, "", nil, nil))
	if v, ok := set.Handles["x"]; !ok || v {
		t.Fatalf("重連中的 handle 應存在但標為未連線：%v", set.Handles)
	}
}

func TestSwitchTerminal(t *testing.T) {
	o := OrcaEnv{BinDir: "/b"}
	var argv []string
	if err := o.SwitchTerminal(fakeRun(`{"ok":true,"result":{}}`, "warn", nil, &argv), "term_1"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(argv, []string{"/b/orca", "terminal", "switch", "--terminal=term_1", "--json"}) {
		t.Fatalf("argv = %q", argv)
	}
	if err := o.SwitchTerminal(fakeRun("", "no such terminal", errors.New("exit status 1"), nil), "x"); err == nil || !strings.Contains(err.Error(), "no such terminal") {
		t.Fatalf("err = %v", err)
	}
}
