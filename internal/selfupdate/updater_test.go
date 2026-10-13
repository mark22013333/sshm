package selfupdate

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckLatestCachedOncePerDay(t *testing.T) {
	fr := newFakeRelease(t, "v0.4.0", []byte("新版"))
	u := fr.updater(t, "0.3.0", installFake(t, 0o755))
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	u.Now = func() time.Time { return now }
	ctx := context.Background()

	got, err := u.CheckLatest(ctx)
	if err != nil || got != "v0.4.0" {
		t.Fatalf("第一次查詢 = %q, %v", got, err)
	}
	now = now.Add(23 * time.Hour)
	fr.tag = "v0.5.0"
	got, err = u.CheckLatest(ctx)
	if err != nil || got != "v0.4.0" {
		t.Fatalf("一天內應沿用快取，得到 %q, %v", got, err)
	}
	if n := fr.apiCalls.Load(); n != 1 {
		t.Fatalf("一天內查了 %d 次 API，應只查 1 次", n)
	}
	now = now.Add(2 * time.Hour)
	got, err = u.CheckLatest(ctx)
	if err != nil || got != "v0.5.0" {
		t.Fatalf("超過一天應重查，得到 %q, %v", got, err)
	}
	if n := fr.apiCalls.Load(); n != 2 {
		t.Fatalf("API 次數 = %d，want 2", n)
	}
	st, err := os.Stat(u.CachePath)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("快取檔權限 = %v, %v", st, err)
	}
}

func TestCheckLatestFailureAlsoCountsAsDailyCheck(t *testing.T) {
	fr := newFakeRelease(t, "v0.4.0", []byte("新版"))
	u := fr.updater(t, "0.3.0", installFake(t, 0o755))
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	u.Now = func() time.Time { return now }
	ctx := context.Background()

	if _, err := u.CheckLatest(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(25 * time.Hour)
	fr.apiStatus = http.StatusInternalServerError
	got, err := u.CheckLatest(ctx)
	if err == nil {
		t.Fatal("API 失敗應回報錯誤（由呼叫端決定靜默）")
	}
	if got != "v0.4.0" {
		t.Fatalf("失敗時應回傳上次已知的最新版，得到 %q", got)
	}
	now = now.Add(time.Hour)
	if _, err := u.CheckLatest(ctx); err != nil {
		t.Fatalf("失敗後一天內應沿用快取不再查：%v", err)
	}
	if n := fr.apiCalls.Load(); n != 2 {
		t.Fatalf("API 次數 = %d，want 2（失敗也算當天查過）", n)
	}
}

func TestApplySuccessReplacesAtomicallyAndKeepsPerm(t *testing.T) {
	newBin := []byte("新版 sshm v0.4.0")
	fr := newFakeRelease(t, "v0.4.0", newBin)
	exe := installFake(t, 0o750)
	u := fr.updater(t, "0.3.0", exe)

	got, err := u.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply：%v", err)
	}
	if got != "v0.4.0" {
		t.Fatalf("回傳版本 = %q", got)
	}
	after := snap(t, exe)
	if !bytes.Equal(after.data, newBin) {
		t.Fatalf("內容 = %q", after.data)
	}
	if after.mode.Perm() != 0o750 {
		t.Fatalf("權限 = %v，want 0750", after.mode.Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Fatalf("目錄應只剩 sshm，實際 %d 個項目", len(entries))
	}
}

func TestApplyThroughSymlinkReplacesTarget(t *testing.T) {
	newBin := []byte("新版")
	fr := newFakeRelease(t, "v0.4.0", newBin)
	real := installFake(t, 0o755)
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "sshm")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	exe, err := ResolveExecutable(func() (string, error) { return link, nil })
	if err != nil {
		t.Fatal(err)
	}
	if exe != mustEval(t, real) {
		t.Fatalf("ResolveExecutable = %q，want %q", exe, real)
	}
	u := fr.updater(t, "0.3.0", exe)
	if _, err := u.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink 應保留：%v %v", fi, err)
	}
	if b, _ := os.ReadFile(real); !bytes.Equal(b, newBin) {
		t.Fatalf("目標檔內容 = %q", b)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestApplyChecksumMismatchKeepsOriginal(t *testing.T) {
	fr := newFakeRelease(t, "v0.4.0", []byte("新版"))
	fr.checksums = sha([]byte("被竄改的內容")) + "  " + testAsset + "\n"
	exe := installFake(t, 0o755)
	before := snap(t, exe)
	u := fr.updater(t, "0.3.0", exe)

	_, err := u.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("checksum 不符應拒絕，得到 %v", err)
	}
	assertUnchanged(t, exe, before)
}

func TestApplyMissingChecksumEntryKeepsOriginal(t *testing.T) {
	fr := newFakeRelease(t, "v0.4.0", []byte("新版"))
	fr.checksums = sha([]byte("x")) + "  sshm_darwin_amd64.tar.gz\n"
	exe := installFake(t, 0o755)
	before := snap(t, exe)
	if _, err := fr.updater(t, "0.3.0", exe).Apply(context.Background()); err == nil {
		t.Fatal("checksums.txt 沒有本架構的項目應拒絕")
	}
	assertUnchanged(t, exe, before)
}

func TestApplyDownloadInterruptedKeepsOriginal(t *testing.T) {
	fr := newFakeRelease(t, "v0.4.0", []byte(strings.Repeat("新版", 50000)))
	fr.archiveHandler = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write(fr.archive[:len(fr.archive)/2])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // 中途斷線
	}
	exe := installFake(t, 0o755)
	before := snap(t, exe)
	if _, err := fr.updater(t, "0.3.0", exe).Apply(context.Background()); err == nil {
		t.Fatal("下載中斷應回報錯誤")
	}
	assertUnchanged(t, exe, before)
}

func TestApplyRejectsRedirectToForeignHost(t *testing.T) {
	other := newFakeRelease(t, "v0.4.0", []byte("來路不明"))
	fr := newFakeRelease(t, "v0.4.0", []byte("新版"))
	fr.redirectArchiveTo = other.srv.URL + "/download/v0.4.0/" + testAsset
	exe := installFake(t, 0o755)
	before := snap(t, exe)
	u := fr.updater(t, "0.3.0", exe)
	u.Client = other.srv.Client() // 兩個 server 共用 httptest 憑證
	_, err := u.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "GitHub") {
		t.Fatalf("redirect 到非允許網域應拒絕，得到 %v", err)
	}
	assertUnchanged(t, exe, before)
}

func TestApplyRejectsOversizedArchive(t *testing.T) {
	// 隨機內容壓不小，壓縮檔一定超過 512 bytes
	noise := make([]byte, 4096)
	if _, err := rand.Read(noise); err != nil {
		t.Fatal(err)
	}
	fr := newFakeRelease(t, "v0.4.0", noise)
	exe := installFake(t, 0o755)
	before := snap(t, exe)
	u := fr.updater(t, "0.3.0", exe)
	u.MaxArchive = 512
	if _, err := u.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超過大小上限應拒絕，得到 %v", err)
	}
	assertUnchanged(t, exe, before)
}

func TestExtractBinaryRejectsTraversalAndOddEntries(t *testing.T) {
	bad := map[string][]tarEntry{
		"路徑穿越":    {{name: "../sshm", body: []byte("x")}},
		"子目錄":     {{name: "bin/sshm", body: []byte("x")}},
		"絕對路徑":    {{name: "/sshm", body: []byte("x")}},
		"symlink": {{name: "sshm", typeflag: '2', linkname: "/etc/passwd"}},
		"重複":      {{name: "sshm", body: []byte("a")}, {name: "./sshm", body: []byte("b")}},
		"沒有 sshm": {{name: "README.md", body: []byte("x")}},
		"空檔":      {{name: "sshm", body: nil}},
	}
	for name, entries := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := extractBinary(makeTarGz(t, entries...), 1<<20); err == nil {
				t.Fatal("應拒絕")
			}
		})
	}
	got, err := extractBinary(makeTarGz(t, tarEntry{name: "README.md", body: []byte("r")}, tarEntry{name: "sshm", body: []byte("ok")}), 1<<20)
	if err != nil || string(got) != "ok" {
		t.Fatalf("正常壓縮檔 = %q, %v", got, err)
	}
}

func TestApplyWhenAlreadyLatest(t *testing.T) {
	fr := newFakeRelease(t, "v0.3.0", []byte("同版"))
	exe := installFake(t, 0o755)
	before := snap(t, exe)
	_, err := fr.updater(t, "0.3.0", exe).Apply(context.Background())
	if !errors.Is(err, ErrUpToDate) {
		t.Fatalf("已是最新版應回 ErrUpToDate，得到 %v", err)
	}
	assertUnchanged(t, exe, before)
}

func TestEligibility(t *testing.T) {
	exe := installFake(t, 0o755)
	base := func() *Updater {
		return &Updater{Version: "0.3.0", GOOS: "darwin", GOARCH: "arm64", ExePath: exe}
	}
	if e := base().Eligibility(); !e.OK {
		t.Fatalf("正式版、darwin/arm64、可寫目錄應可更新：%+v", e)
	}

	dev := base()
	dev.Version = "(devel)"
	if e := dev.Eligibility(); e.OK || !strings.Contains(e.Reason, "開發版") || !strings.Contains(e.Manual, "go install github.com/mark22013333/sshm@latest") {
		t.Fatalf("開發版 = %+v", e)
	}

	other := base()
	other.GOOS = "linux"
	if e := other.Eligibility(); e.OK || !strings.Contains(e.Reason, "macOS") {
		t.Fatalf("非 darwin = %+v", e)
	}
	arch := base()
	arch.GOARCH = "386"
	if e := arch.Eligibility(); e.OK {
		t.Fatalf("不支援的架構 = %+v", e)
	}

	noExe := base()
	noExe.ExePath = ""
	if e := noExe.Eligibility(); e.OK {
		t.Fatalf("取不到執行檔路徑 = %+v", e)
	}
}

func TestEligibilityAndApplyWhenDirNotWritable(t *testing.T) {
	fr := newFakeRelease(t, "v0.4.0", []byte("新版"))
	exe := installFake(t, 0o755)
	dir := filepath.Dir(exe)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	before := snap(t, exe)
	u := fr.updater(t, "0.3.0", exe)

	e := u.Eligibility()
	if e.OK || !strings.Contains(e.Reason, "無法寫入") || !strings.Contains(e.Manual, "curl -fsSL") || !strings.Contains(e.Manual, dir) {
		t.Fatalf("不可寫目錄 = %+v", e)
	}
	if strings.Contains(e.Manual, "sudo") {
		t.Fatalf("不得建議 sudo：%q", e.Manual)
	}
	if _, err := u.Apply(context.Background()); err == nil {
		t.Fatal("不可寫目錄時 Apply 應拒絕")
	}
	assertUnchanged(t, exe, before)
	if n := fr.apiCalls.Load(); n != 0 {
		t.Fatalf("不能更新時不該連網，實際查了 %d 次", n)
	}
}

func TestDefaultAllowURL(t *testing.T) {
	ok := []string{
		"https://api.github.com/repos/mark22013333/sshm/releases/latest",
		"https://github.com/mark22013333/sshm/releases/download/v0.3.0/checksums.txt",
		"https://objects.githubusercontent.com/github-production-release-asset/x",
		"https://release-assets.githubusercontent.com/github-production-release-asset/x",
	}
	bad := []string{
		"http://github.com/mark22013333/sshm", // 非 https
		"https://github.com.evil.example/x",
		"https://evil.example/github.com",
		"https://raw.githubusercontent.com.evil.example/x",
		"https://user@evil.example/x",
	}
	for _, s := range ok {
		if !DefaultAllowURL(mustURL(t, s)) {
			t.Errorf("應允許 %s", s)
		}
	}
	for _, s := range bad {
		if DefaultAllowURL(mustURL(t, s)) {
			t.Errorf("不應允許 %s", s)
		}
	}
}
