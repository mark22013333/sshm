package agentaccess

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark22013333/sshm/internal/connect"
)

const (
	blobA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	blobB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOtherKeyBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

// runLocal 以臨時 HOME 在本機執行遠端腳本（經過與 RemoteCommand 相同的 sh -c 包裝）。
func runLocal(t *testing.T, home, script string) string {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", RemoteCommand(script))
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("腳本失敗：%v\n%s", err, out)
	}
	return string(out)
}

func readAK(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestScriptsAreSafeToWrap(t *testing.T) {
	for _, s := range []string{InstallScript("sshm-a", blobA), RemoveScript(blobA)} {
		if strings.ContainsAny(s, "'%\n") || strings.Contains(s, "${") {
			t.Fatalf("腳本含不可包進 RemoteCommand 的字元：%s", s)
		}
	}
}

func TestInstallCreatesWithPermissions(t *testing.T) {
	home := t.TempDir()
	out := runLocal(t, home, InstallScript("sshm-a", blobA))
	if !strings.Contains(out, okMarker+" changed") {
		t.Fatalf("out = %q", out)
	}
	if got := readAK(t, home); got != AuthorizedKeysLine(blobA, "sshm-a")+"\n" {
		t.Fatalf("authorized_keys = %q", got)
	}
	if !strings.HasPrefix(readAK(t, home), "no-agent-forwarding,no-X11-forwarding ssh-ed25519 ") ||
		!strings.HasSuffix(readAK(t, home), " sshm-agent:sshm-a\n") {
		t.Fatal("行首限制或行尾註解不對")
	}
	for path, want := range map[string]os.FileMode{filepath.Join(home, ".ssh"): 0o700, filepath.Join(home, ".ssh", "authorized_keys"): 0o600} {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != want {
			t.Errorf("%s perm = %o, want %o", path, st.Mode().Perm(), want)
		}
	}
}

func TestInstallIsIdempotentAndKeepsOtherLines(t *testing.T) {
	home := t.TempDir()
	ssh := filepath.Join(home, ".ssh")
	_ = os.MkdirAll(ssh, 0o755)
	other := "ssh-rsa AAAAB3Nza-user-key user@laptop\n" +
		"# 註解行\n" +
		"ssh-ed25519 AAAAOTHER sshm-agent:sshm-ab\n" + // 別名相近的其他 sshm 行
		"ssh-ed25519 AAAAX not-sshm-agent:sshm-a extra\n"
	ak := filepath.Join(ssh, "authorized_keys")
	if err := os.WriteFile(ak, []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	runLocal(t, home, InstallScript("sshm-a", blobA))
	want := other + AuthorizedKeysLine(blobA, "sshm-a") + "\n"
	if got := readAK(t, home); got != want {
		t.Fatalf("第一次安裝 = %q", got)
	}
	st, _ := os.Stat(ssh)
	st2, _ := os.Stat(ak)
	if st.Mode().Perm() != 0o700 || st2.Mode().Perm() != 0o600 {
		t.Fatalf("權限未收緊：%o %o", st.Mode().Perm(), st2.Mode().Perm())
	}
	// 重複開通：內容不變、不產生重複行
	out := runLocal(t, home, InstallScript("sshm-a", blobA))
	if !strings.Contains(out, okMarker+" unchanged") || readAK(t, home) != want {
		t.Fatalf("重複安裝 out=%q ak=%q", out, readAK(t, home))
	}
	// 換了金鑰：以公鑰本體判斷，舊公鑰那行可能屬於別台電腦，保留不動
	runLocal(t, home, InstallScript("sshm-a", blobB))
	want2 := want + AuthorizedKeysLine(blobB, "sshm-a") + "\n"
	if got := readAK(t, home); got != want2 {
		t.Fatalf("換金鑰 = %q", got)
	}
}

func TestInstallCollapsesDuplicatesInPlace(t *testing.T) {
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	mine := AuthorizedKeysLine(blobA, "sshm-a")
	content := "first\n" + mine + "\nmiddle\n" + mine + "\nlast\n"
	_ = os.WriteFile(filepath.Join(home, ".ssh", "authorized_keys"), []byte(content), 0o600)
	runLocal(t, home, InstallScript("sshm-a", blobA))
	if got := readAK(t, home); got != "first\n"+mine+"\nmiddle\nlast\n" {
		t.Fatalf("got %q", got)
	}
}

func TestRemoveOnlyOwnLine(t *testing.T) {
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	keep := "ssh-rsa AAAAB3Nza-user-key user@laptop\nssh-ed25519 AAAAOTHER sshm-agent:sshm-ab\n"
	content := "ssh-rsa AAAAB3Nza-user-key user@laptop\n" + AuthorizedKeysLine(blobA, "sshm-a") + "\nssh-ed25519 AAAAOTHER sshm-agent:sshm-ab\n"
	ak := filepath.Join(home, ".ssh", "authorized_keys")
	_ = os.WriteFile(ak, []byte(content), 0o600)
	out := runLocal(t, home, RemoveScript(blobA))
	if !strings.Contains(out, okMarker+" changed") || readAK(t, home) != keep {
		t.Fatalf("移除 out=%q ak=%q", out, readAK(t, home))
	}
	out = runLocal(t, home, RemoveScript(blobA))
	if !strings.Contains(out, okMarker+" unchanged") || readAK(t, home) != keep {
		t.Fatalf("重複移除 out=%q", out)
	}
	// 沒有 authorized_keys 也算成功，且不建立檔案
	empty := t.TempDir()
	out = runLocal(t, empty, RemoveScript(blobA))
	if !strings.Contains(out, okMarker+" absent") {
		t.Fatalf("out = %q", out)
	}
	if _, err := os.Stat(filepath.Join(empty, ".ssh")); !os.IsNotExist(err) {
		t.Fatal("移除時不應建立 ~/.ssh")
	}
}

func TestEnsureKeyGeneratesEd25519(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("沒有 ssh-keygen")
	}
	p := testPaths(t)
	k, err := EnsureKey(context.Background(), connect.ExecRunner, p, "sshm-a")
	if err != nil {
		t.Fatal(err)
	}
	if !k.Created || k.Private != filepath.Join(p.KeyDir, "sshm-a") || !strings.HasPrefix(k.TypeBlob, "ssh-ed25519 ") {
		t.Fatalf("key = %+v", k)
	}
	for path, want := range map[string]os.FileMode{p.KeyDir: 0o700, k.Private: 0o600} {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != want {
			t.Errorf("%s perm = %o, want %o", path, st.Mode().Perm(), want)
		}
	}
	// 指紋與 ssh-keygen -l 一致；無 passphrase（-y 不需要輸入即可讀出公鑰）
	out, err := exec.Command("ssh-keygen", "-l", "-f", k.Public).Output()
	if err != nil || !strings.Contains(string(out), k.Fingerprint) || !strings.Contains(string(out), "sshm-agent:sshm-a") {
		t.Fatalf("ssh-keygen -l = %q, fp=%s err=%v", out, k.Fingerprint, err)
	}
	if out, err := exec.Command("ssh-keygen", "-y", "-P", "", "-f", k.Private).Output(); err != nil || !strings.HasPrefix(string(out), k.TypeBlob) {
		t.Fatalf("私鑰應無 passphrase：%v %s", err, out)
	}
	// 第二次沿用
	k2, err := EnsureKey(context.Background(), connect.ExecRunner, p, "sshm-a")
	if err != nil || k2.Created || k2.Fingerprint != k.Fingerprint {
		t.Fatalf("應沿用既有金鑰：%+v %v", k2, err)
	}
	// 只刪 ~/.ssh/sshm 底下的檔
	if _, err := RemoveKey(p, "/etc/hosts"); err == nil {
		t.Fatal("不在 KeyDir 底下的路徑不應刪")
	}
	removed, err := RemoveKey(p, k.Private)
	if err != nil || len(removed) != 2 {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
}

// 審查 #3：同別名但不同公鑰的他人行（另一台筆電以相同別名開通、手寫的備份行）必須保留。
func TestInstallAndRemoveMatchByKeyBlobNotComment(t *testing.T) {
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	colleague := AuthorizedKeysLine(blobB, "sshm-a")
	backup := "ssh-ed25519 AAAAYbackup backup key sshm-agent:sshm-a"
	crlf := "ssh-ed25519 AAAAZcrlf sshm-agent:sshm-a\r"
	base := "ssh-rsa AAAAB3Nza-user-key user@laptop\n" + colleague + "\n" + backup + "\n" + crlf + "\n"
	_ = os.WriteFile(filepath.Join(home, ".ssh", "authorized_keys"), []byte(base), 0o600)
	runLocal(t, home, InstallScript("sshm-a", blobA))
	if got, want := readAK(t, home), base+AuthorizedKeysLine(blobA, "sshm-a")+"\n"; got != want {
		t.Fatalf("安裝不得動同別名的他人行：\ngot  %q\nwant %q", got, want)
	}
	out := runLocal(t, home, RemoveScript(blobA))
	if got := readAK(t, home); !strings.Contains(out, okMarker+" changed") || got != base {
		t.Fatalf("移除只刪本機公鑰那行：out=%q\ngot  %q\nwant %q", out, got, base)
	}
}

// 審查低項：改寫 authorized_keys 用同目錄暫存檔 rename（原子），中途斷線不會留下截斷的檔案；symlink 則寫到目標、保留連結。
func TestRemoteWriteIsAtomicRename(t *testing.T) {
	home := t.TempDir()
	ssh := filepath.Join(home, ".ssh")
	_ = os.MkdirAll(ssh, 0o700)
	ak := filepath.Join(ssh, "authorized_keys")
	_ = os.WriteFile(ak, []byte("ssh-rsa AAAAB3Nza-user-key user@laptop\n"), 0o600)
	before, _ := os.Stat(ak)
	runLocal(t, home, InstallScript("sshm-a", blobA))
	after, _ := os.Stat(ak)
	if os.SameFile(before, after) {
		t.Fatal("一般檔應以 rename 原子取代（inode 會改變）")
	}
	if after.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o", after.Mode().Perm())
	}
	// symlink：寫到目標，連結保留
	home2 := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home2, ".ssh"), 0o700)
	target := filepath.Join(t.TempDir(), "real_keys")
	_ = os.WriteFile(target, []byte("ssh-rsa AAAAB3Nza-user-key user@laptop\n"), 0o600)
	link := filepath.Join(home2, ".ssh", "authorized_keys")
	_ = os.Symlink(target, link)
	runLocal(t, home2, InstallScript("sshm-a", blobA))
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink 被換成一般檔")
	}
	if data, _ := os.ReadFile(target); !strings.Contains(string(data), blobA) {
		t.Fatalf("目標檔應寫入公鑰：%q", data)
	}
	entries, _ := os.ReadDir(filepath.Join(home, ".ssh"))
	if len(entries) != 1 {
		t.Fatalf("不應留下暫存檔：%v", entries)
	}
}
