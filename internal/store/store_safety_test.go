package store

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustUpsert(t *testing.T, f *File, h Host) string {
	t.Helper()
	id, err := f.UpsertHost(h)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func entryNames(entries []os.DirEntry) []string {
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestValidateHost(t *testing.T) {
	ok := Host{Name: "n", Host: "10.0.0.1", User: "u", Port: 22, Auth: AuthPassword}
	with := func(mod func(*Host)) Host { h := ok; mod(&h); return h }
	cases := []struct {
		name    string
		host    Host
		wantErr string
	}{
		{"合法", ok, ""},
		{"user 以 - 開頭", with(func(h *Host) { h.User = "-oProxyCommand=evil" }), "user 不可以 - 開頭"},
		{"host 以 - 開頭", with(func(h *Host) { h.Host = "-J" }), "host 不可以 - 開頭"},
		{"缺必填", with(func(h *Host) { h.Name = ""; h.User = " " }), "名稱、user"},
		{"key 缺金鑰檔", with(func(h *Host) { h.Auth = AuthKey }), "金鑰檔"},
		{"key 有金鑰檔", with(func(h *Host) { h.Auth = AuthKey; h.IdentityFile = "~/.ssh/id" }), ""},
		{"未知 auth", with(func(h *Host) { h.Auth = "otp" }), "不支援"},
	}
	for _, c := range cases {
		err := ValidateHost(c.host)
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected err %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
		}
	}
	f := NewFile()
	if _, err := f.UpsertHost(with(func(h *Host) { h.User = "-x" })); err == nil || len(f.Hosts) != 0 {
		t.Fatal("UpsertHost 應拒絕 - 開頭的 user")
	}
}

func TestOpenTightensExistingPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sshm")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hosts.json")
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err != nil {
		t.Fatal(err)
	}
	if p := perm(t, path); p != 0o600 {
		t.Fatalf("file perm = %o, want 600", p)
	}
	if p := perm(t, dir); p != 0o700 {
		t.Fatalf("dir perm = %o, want 700", p)
	}
}

// 兩個實例各自從舊的快照新增一台，鎖內重讀後兩台都要留下。
func TestConcurrentUpdatesKeepBothHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	if _, err := Open(path); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, name := range []string{"A", "B"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, err := Update(path, func(f *File) error {
				_, err := f.UpsertHost(Host{Name: name, Host: "h", User: "u", Port: 22})
				return err
			})
			errs <- err
		}(name)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Hosts) != 2 {
		t.Fatalf("hosts = %+v, want A 與 B", f.Hosts)
	}
}

// 大量並行（同一行程內各自 open 鎖檔），確認沒有任何一筆遺失。
func TestManyConcurrentUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	const n = 30
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := Update(path, func(f *File) error {
				_, err := f.UpsertHost(Host{Name: fmt.Sprint(i), Host: "h", User: "u"})
				return err
			}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Hosts) != n {
		t.Fatalf("hosts = %d, want %d", len(f.Hosts), n)
	}
}

func TestUpdateMutateErrorDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	if _, err := Open(path); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if _, err := Update(path, func(f *File) error {
		_, err := f.UpsertHost(Host{Name: "x", Host: "-bad", User: "u"})
		return err
	}); err == nil {
		t.Fatal("expected validation error")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("file changed after failed mutate")
	}
}

func TestSaveFollowsSymlink(t *testing.T) {
	root := t.TempDir()
	dotfiles := filepath.Join(root, "dotfiles")
	if err := os.Mkdir(dotfiles, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dotfiles, "hosts.json")
	if err := os.WriteFile(target, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "sshm")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "hosts.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(link, func(f *File) error {
		_, err := f.UpsertHost(Host{Name: "s", Host: "h", User: "u"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink 被取代成一般檔")
	}
	data, _ := os.ReadFile(target)
	if !strings.Contains(string(data), `"name": "s"`) {
		t.Fatalf("目標檔未更新：%s", data)
	}
	if p := perm(t, dotfiles); p != 0o755 {
		t.Fatalf("symlink 目標目錄權限不應被改動，got %o", p)
	}
}

// encoding/json 比對欄位不分大小寫；"NAME" 已被吃進 Name，不應再存進 Extra 而在寫回後蓋掉新值。
func TestUnknownFieldCaseCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	f, err := Parse([]byte(`{"version":1,"hosts":[{"id":"h_1","name":"a","NAME":"shadow","host":"h","user":"u","Other":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	f.Hosts[0].Name = "new"
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	reread, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reread.Hosts[0].Name; got != "new" {
		t.Fatalf("name = %q, want new", got)
	}
	if _, ok := reread.Hosts[0].Extra["Other"]; !ok {
		t.Fatal("真正的未知欄位 Other 應保留")
	}
}

func TestApplyHostEdit(t *testing.T) {
	base := Host{ID: "h_1", Name: "a", Host: "h", User: "u", Port: 22, Auth: AuthPassword, Password: "old"}
	f := &File{Hosts: []Host{base}}
	// 其他實例先改了密碼
	f.Hosts[0].Password = "NEW"
	edited := base
	edited.Name = "renamed"
	if err := f.ApplyHostEdit(base, edited); err != nil {
		t.Fatal(err)
	}
	if h := f.Hosts[0]; h.Name != "renamed" || h.Password != "NEW" {
		t.Fatalf("merge = %+v", h)
	}
	// 改成 key 認證時清掉密碼
	toKey := f.Hosts[0]
	toKey.Auth, toKey.IdentityFile, toKey.Password = AuthKey, "/k", ""
	if err := f.ApplyHostEdit(f.Hosts[0], toKey); err != nil {
		t.Fatal(err)
	}
	if h := f.Hosts[0]; h.Auth != AuthKey || h.Password != "" {
		t.Fatalf("auth change = %+v", h)
	}
	// 已被刪除
	gone := &File{}
	if err := gone.ApplyHostEdit(base, edited); err != ErrHostGone || len(gone.Hosts) != 0 {
		t.Fatalf("err = %v hosts=%v", err, gone.Hosts)
	}
}

func TestValidateRejectsControlChars(t *testing.T) {
	ok := Host{Name: "n", Host: "h", User: "u", Auth: AuthPassword}
	mods := map[string]func(*Host){
		"名稱":        func(h *Host) { h.Name = "a\nb" },
		"群組":        func(h *Host) { h.Group = "g\x1b" },
		"host":      func(h *Host) { h.Host = "h\t" },
		"user":      func(h *Host) { h.User = "u\r" },
		"密碼":        func(h *Host) { h.Password = "p\nwhoami" },
		"金鑰檔":       func(h *Host) { h.IdentityFile = "/k\x00" },
		"額外 ssh 參數": func(h *Host) { h.ExtraArgs = "-v\n-v" },
		"自訂指令":      func(h *Host) { h.CustomCommand = "ls\x7f" },
	}
	for label, mod := range mods {
		h := ok
		mod(&h)
		err := ValidateHost(h)
		if err == nil || !strings.Contains(err.Error(), label) || !strings.Contains(err.Error(), "控制字元") {
			t.Errorf("%s: err = %v", label, err)
		}
	}
	h := ok
	h.Password = "中文 空白 'q' $x"
	if err := ValidateHost(h); err != nil {
		t.Errorf("一般字元不應被擋：%v", err)
	}
}

func TestDanglingSymlinkGivesClearError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sshm")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "hosts.json")
	if err := os.Symlink(filepath.Join(dir, "missing", "hosts.json"), link); err != nil {
		t.Fatal(err)
	}
	_, err := Open(link)
	if err == nil || !strings.Contains(err.Error(), "symlink") || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("err = %v", err)
	}
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("懸空 symlink 不應被取代")
	}
}

func TestLockFileBesideTargetAndTightened(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "dotfiles")
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDir, "hosts.json")
	if err := os.WriteFile(target, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target+".lock", nil, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target+".lock", 0o666); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "sshm")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "hosts.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); err != nil {
		t.Fatal(err)
	}
	if p := perm(t, target+".lock"); p != 0o600 {
		t.Fatalf("lock perm = %o, want 600", p)
	}
	if _, err := os.Stat(link + ".lock"); err == nil {
		t.Fatal("鎖檔應放在 symlink 目標旁，不是 symlink 旁")
	}
}

func TestLockTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshm", "hosts.json")
	if _, err := Open(path); err != nil {
		t.Fatal(err)
	}
	old := lockTimeout
	lockTimeout = 200 * time.Millisecond
	defer func() { lockTimeout = old }()
	unlock, err := lockFile(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	start := time.Now()
	_, err = Update(path, func(*File) error { return nil })
	if err != ErrLockTimeout || !strings.Contains(err.Error(), "另一個 sshm 正在存檔") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("等待過久：%v", d)
	}
}

func TestApplyHostEditConflicts(t *testing.T) {
	base := Host{ID: "h_1", Name: "a", Host: "10.0.0.1", User: "u", Port: 22, Auth: AuthPassword, Password: "old"}
	cases := []struct {
		name  string
		other func(*Host) // 另一個實例先存的變更
		mine  func(*Host) // 本表單的變更
	}{
		{"他人改 host、我改密碼", func(h *Host) { h.Host = "10.0.0.99" }, func(h *Host) { h.Password = "new-for-10.0.0.1" }},
		{"他人改 auth 為 key、我改密碼", func(h *Host) { h.Auth = AuthKey; h.IdentityFile = "/k"; h.Password = "" }, func(h *Host) { h.Password = "new" }},
		{"他人改密碼、我改 user", func(h *Host) { h.Password = "theirs" }, func(h *Host) { h.User = "root" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cur := base
			c.other(&cur)
			f := &File{Hosts: []Host{cur}}
			edited := base
			c.mine(&edited)
			if err := f.ApplyHostEdit(base, edited); err != ErrEditConflict {
				t.Fatalf("err = %v, want ErrEditConflict", err)
			}
			if !reflect.DeepEqual(f.Hosts[0], cur) {
				t.Fatalf("衝突時不得改動資料：%+v", f.Hosts[0])
			}
		})
	}
	// 不衝突：他人改 host、我只改名稱
	cur := base
	cur.Host = "10.0.0.99"
	f := &File{Hosts: []Host{cur}}
	edited := base
	edited.Name = "b"
	if err := f.ApplyHostEdit(base, edited); err != nil || f.Hosts[0].Host != "10.0.0.99" || f.Hosts[0].Name != "b" {
		t.Fatalf("err=%v host=%+v", err, f.Hosts[0])
	}
}
