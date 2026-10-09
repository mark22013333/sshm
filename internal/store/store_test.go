package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func perm(t *testing.T, p string) os.FileMode {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestDirHonorsXDG(t *testing.T) {
	x := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	got, err := HostsPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(x, "sshm", "hosts.json"); got != want {
		t.Fatalf("HostsPath = %q, want %q", got, want)
	}
	// 相對路徑依 XDG 規範忽略
	t.Setenv("XDG_CONFIG_HOME", "relative/dir")
	t.Setenv("HOME", x)
	got, _ = Dir()
	if want := filepath.Join(x, ".config", "sshm"); got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
}

func TestOpenCreatesFileWithPermissions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := HostsPath()
	if err != nil {
		t.Fatal(err)
	}
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != 1 || len(f.Hosts) != 0 {
		t.Fatalf("unexpected new file: %+v", f)
	}
	if p := perm(t, path); p != 0o600 {
		t.Fatalf("file perm = %o, want 600", p)
	}
	if p := perm(t, filepath.Dir(path)); p != 0o700 {
		t.Fatalf("dir perm = %o, want 700", p)
	}
	data, _ := os.ReadFile(path)
	for _, key := range []string{`"groups": []`, `"hosts": []`, `"tunnels": []`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("new file missing %s:\n%s", key, data)
		}
	}
}

func TestOpenTightensLooseDirPermission(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sshm")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(dir, "hosts.json")); err != nil {
		t.Fatal(err)
	}
	if p := perm(t, dir); p != 0o700 {
		t.Fatalf("dir perm = %o, want 700", p)
	}
}

const sample = `{
  "version": 1,
  "futureTop": {"a": [1, 2]},
  "groups": [{"name": "G", "color": "blue", "icon": "x"}],
  "hosts": [{
    "id": "h_1", "name": "n", "group": "G", "host": "10.0.0.1", "port": 22, "user": "u",
    "auth": "password", "password": "p", "identityFile": "", "color": "", "extraArgs": "",
    "customCommand": "", "pluginData": {"k": "v"}, "zz": null
  }],
  "tunnels": [{
    "id": "t_1", "name": "db", "hostId": "h_1", "autostart": true,
    "rules": [
      {"type": "L", "bindPort": 13306, "targetHost": "127.0.0.1", "targetPort": 3306, "note": "mysql"},
      {"type": "D", "bindPort": 1080}
    ]
  }]
}`

func TestUnknownFieldsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	f.Hosts[0].Name = "改名"
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(sample), &want); err != nil {
		t.Fatal(err)
	}
	want["hosts"].([]any)[0].(map[string]any)["name"] = "改名"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch\n got: %s", data)
	}
	// 已知欄位順序保持在前，未知欄位接在後
	if strings.Index(string(data), `"version"`) > strings.Index(string(data), `"futureTop"`) {
		t.Errorf("known fields should come first:\n%s", data)
	}
}

func TestSaveIsAtomicAndLeavesNoTemp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sshm")
	path := filepath.Join(dir, "hosts.json")
	f := NewFile()
	mustUpsert(t, f, Host{Name: "a", Host: "h", User: "u", Port: 22, Auth: AuthNone})
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, f, Host{Name: "b", Host: "h2", User: "u", Port: 22, Auth: AuthNone})
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if names := entryNames(entries); strings.Join(names, ",") != "hosts.json,hosts.json.lock" {
		t.Fatalf("dir should only contain hosts.json and its lock, got %v", names)
	}
	if p := perm(t, path); p != 0o600 {
		t.Fatalf("perm = %o", p)
	}
	reread, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reread.Hosts) != 2 {
		t.Fatalf("hosts = %d", len(reread.Hosts))
	}
}

func TestSaveFailureKeepsOriginal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sshm")
	path := filepath.Join(dir, "hosts.json")
	if err := Save(path, NewFile()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	f := NewFile()
	f.Extra = Extra{"bad": []byte("{not json")}
	if err := Save(path, f); err == nil {
		t.Fatal("expected error for invalid extra")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("original file changed after failed save")
	}
	entries, _ := os.ReadDir(dir)
	if names := entryNames(entries); len(names) != 2 {
		t.Fatalf("temp file left behind: %v", names)
	}
}

func TestColors(t *testing.T) {
	f := &File{Groups: []Group{{Name: "G", Color: "blue"}, {Name: "N"}}}
	cases := []struct {
		host Host
		want string
	}{
		{Host{Color: "red", Group: "G"}, "red"},
		{Host{Group: "G"}, "blue"},
		{Host{Group: "N"}, ""},
		{Host{Group: "missing"}, ""},
		{Host{Color: "pink", Group: "G"}, "blue"},
		{Host{Color: " Purple "}, "purple"},
	}
	for _, c := range cases {
		if got := f.EffectiveColor(c.host); got != c.want {
			t.Errorf("EffectiveColor(%+v) = %q, want %q", c.host, got, c.want)
		}
	}
	if ColorEmoji("white") != "⚪" || ColorEmoji("") != "" {
		t.Error("ColorEmoji mismatch")
	}
}

func TestHostOps(t *testing.T) {
	f := NewFile()
	id := mustUpsert(t, f, Host{Name: "a", Group: "新群組", Host: "h", User: "u"})
	if !strings.HasPrefix(id, "h_") || !f.HasGroup("新群組") {
		t.Fatalf("upsert: id=%q groups=%v", id, f.Groups)
	}
	mustUpsert(t, f, Host{Name: "b", Host: "h", User: "u"})
	dup, err := f.DuplicateHost(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.Hosts[1].ID != dup || f.Hosts[1].Name != "a (複本)" || dup == id {
		t.Fatalf("duplicate placed wrong: %+v", f.Hosts)
	}
	if err := f.DeleteHost(id); err != nil {
		t.Fatal(err)
	}
	if len(f.Hosts) != 2 || f.HostIndex(id) != -1 {
		t.Fatalf("delete failed: %+v", f.Hosts)
	}
}
