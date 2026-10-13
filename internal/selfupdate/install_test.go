package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// leftovers 在 dir 放一組「上次更新中斷」與不該被碰的檔案，回傳應被清掉的路徑與應保留的路徑。
func leftovers(t *testing.T, dir string) (stale []string, keep []string) {
	t.Helper()
	old := time.Now().Add(-2 * time.Hour)
	write := func(name string, mtime time.Time) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("殘留"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stale = append(stale, write(".sshm-update-1234567890", old), write(".sshm-update-42", old))
	keep = append(keep,
		write(".sshm-update-55555", time.Now()), // 太新：可能是另一個 sshm 正在寫
		write(".sshm-update-notes.txt", old),    // 不是 CreateTemp 產生的名字
		write(".sshm-update-", old),
		write("範例筆記.txt", old),
	)
	// symlink：連結本身與它指向的檔案都不能動
	outside := filepath.Join(t.TempDir(), "外部檔案")
	if err := os.WriteFile(outside, []byte("不可刪"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(outside, old, old); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".sshm-update-777")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	// 目錄同名也不動
	sub := filepath.Join(dir, ".sshm-update-888")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sub, old, old); err != nil {
		t.Fatal(err)
	}
	keep = append(keep, outside, link, sub)
	return stale, keep
}

func assertCleaned(t *testing.T, stale, keep []string) {
	t.Helper()
	for _, p := range stale {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("舊暫存檔應被清掉：%s（%v）", p, err)
		}
	}
	for _, p := range keep {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("不該被動到：%s（%v）", p, err)
		}
	}
}

func TestApplyCleansStaleUpdateTemps(t *testing.T) {
	newBin := []byte("新版")
	fr := newFakeRelease(t, "v0.4.0", newBin)
	exe := installFake(t, 0o755)
	stale, keep := leftovers(t, filepath.Dir(exe))
	u := fr.updater(t, "0.3.0", exe)
	u.CachePath = "" // 只看 Apply 本身
	if _, err := u.Apply(context.Background()); err != nil {
		t.Fatalf("Apply：%v", err)
	}
	if got := snap(t, exe); !bytes.Equal(got.data, newBin) {
		t.Fatalf("內容 = %q", got.data)
	}
	assertCleaned(t, stale, keep)
}

func TestCheckLatestCleansStaleUpdateTemps(t *testing.T) {
	fr := newFakeRelease(t, "v0.4.0", []byte("新版"))
	exe := installFake(t, 0o755)
	u := fr.updater(t, "0.3.0", exe)
	staleExe, keepExe := leftovers(t, filepath.Dir(exe))
	staleCache, keepCache := leftovers(t, filepath.Dir(u.CachePath))
	if _, err := u.CheckLatest(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCleaned(t, append(staleExe, staleCache...), append(keepExe, keepCache...))
}

func TestWriteFileAtomicSyncsDirAfterRename(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sshm")
	if err := os.WriteFile(target, []byte("舊"), 0o755); err != nil {
		t.Fatal(err)
	}
	var synced []string
	orig := syncDir
	t.Cleanup(func() { syncDir = orig })
	syncDir = func(d string) error {
		b, _ := os.ReadFile(target)
		if string(b) != "新" {
			t.Errorf("目錄 fsync 應在 rename 之後，當下內容 = %q", b)
		}
		synced = append(synced, d)
		return errors.New("模擬：此檔案系統不支援目錄 fsync")
	}
	if err := writeFileAtomic(target, []byte("新"), 0); err != nil {
		t.Fatalf("目錄 fsync 失敗不應讓已完成的替換回報失敗：%v", err)
	}
	if len(synced) != 1 || synced[0] != dir {
		t.Fatalf("應對 %s fsync 一次，實際 %v", dir, synced)
	}
}

func TestSyncDirRealDirectory(t *testing.T) {
	if err := syncDir(t.TempDir()); err != nil {
		t.Skipf("此平台不支援目錄 fsync：%v", err)
	}
}
