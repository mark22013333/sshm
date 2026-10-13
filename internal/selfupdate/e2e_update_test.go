package selfupdate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildSSHM 以 GoReleaser 相同的 ldflags 建出指定版本的 sshm。
func buildSSHM(t *testing.T, version, out string) []byte {
	t.Helper()
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+version, "-o", out, ".")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("建置 sshm %s 失敗：%v\n%s", version, err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func runVersion(t *testing.T, exe string) string {
	t.Helper()
	b, err := exec.Command(exe, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("%s --version：%v\n%s", exe, err, b)
	}
	return strings.TrimSpace(string(b))
}

// TestE2EUpdateRealBinary 用真的 sshm 執行檔走完整更新：httptest 模擬 GitHub release，
// 對臨時目錄中經 symlink 呼叫的舊版副本執行 Apply，再執行替換後的檔案確認版本。
// 只在設定 SSHM_E2E_UPDATE=1 時執行；不連真實 GitHub、不碰使用者安裝的 sshm。
func TestE2EUpdateRealBinary(t *testing.T) {
	if os.Getenv("SSHM_E2E_UPDATE") != "1" {
		t.Skip("未設定 SSHM_E2E_UPDATE=1，略過端對端更新測試")
	}
	work := t.TempDir()
	oldBin := buildSSHM(t, "0.3.0", filepath.Join(work, "build-old"))
	newBin := buildSSHM(t, "0.4.0", filepath.Join(work, "build-new"))

	installDir := filepath.Join(work, "opt", "bin")
	linkDir := filepath.Join(work, "local", "bin")
	for _, d := range []string{installDir, linkDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	real := filepath.Join(installDir, "sshm")
	if err := os.WriteFile(real, oldBin, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "sshm")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Logf("安裝位置：%s（symlink %s）", real, link)
	t.Logf("更新前：%s，sha256=%s", runVersion(t, link), sha(oldBin))

	fr := newFakeRelease(t, "v0.4.0", newBin)
	t.Logf("模擬 release：%s/repos/%s/releases/latest", fr.srv.URL, Repo)
	exe, err := ResolveExecutable(func() (string, error) { return link, nil })
	if err != nil {
		t.Fatal(err)
	}
	u := fr.updater(t, "0.3.0", exe)
	t.Logf("ResolveExecutable：%s", exe)

	latest, err := u.CheckLatest(context.Background())
	if err != nil || latest != "v0.4.0" {
		t.Fatalf("CheckLatest = %q, %v", latest, err)
	}
	t.Logf("CheckLatest：%s（IsNewer=%v）", latest, IsNewer(latest, u.Version))
	e := u.Eligibility()
	if !e.OK {
		t.Fatalf("Eligibility = %+v", e)
	}
	got, err := u.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply：%v", err)
	}
	t.Logf("Apply 回傳：%s", got)

	after := runVersion(t, link)
	t.Logf("更新後：%s", after)
	if after != "sshm 0.4.0" {
		t.Fatalf("更新後版本 = %q", after)
	}
	b, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(real)
	lst, _ := os.Lstat(link)
	t.Logf("實體檔 sha256=%s（新版建置 %s）權限=%v；symlink 保留=%v", sha(b), sha(newBin), st.Mode().Perm(), lst.Mode()&os.ModeSymlink != 0)
	if sha(b) != sha(newBin) || st.Mode().Perm() != 0o755 || lst.Mode()&os.ModeSymlink == 0 {
		t.Fatal("替換結果不符")
	}
	entries, _ := os.ReadDir(installDir)
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	t.Logf("安裝目錄內容：%v", names)
	if len(names) != 1 {
		t.Fatal("安裝目錄不應殘留暫存檔")
	}
}
