package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// extractBinary 從 tar.gz 取出根目錄下名為 sshm 的一般檔案；其他項目一律忽略，
// 不寫任何檔案到磁碟，所以壓縮檔裡的路徑穿越不會有作用。
func extractBinary(archive []byte, limit int64) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("壓縮檔格式不正確：%w", err)
	}
	defer gz.Close()
	// 防 gzip 炸彈：解壓總量也設上限
	tr := tar.NewReader(io.LimitReader(gz, 2*limit+(1<<20)))
	var bin []byte
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("壓縮檔格式不正確：%w", err)
		}
		if path.Clean(hdr.Name) != "sshm" {
			continue
		}
		if found {
			return nil, errors.New("壓縮檔裡有多個 sshm，已中止")
		}
		found = true
		if hdr.Typeflag != tar.TypeReg {
			return nil, errors.New("壓縮檔裡的 sshm 不是一般檔案，已中止")
		}
		if hdr.Size > limit {
			return nil, fmt.Errorf("壓縮檔裡的 sshm 超過大小上限（%d bytes）", limit)
		}
		if bin, err = io.ReadAll(io.LimitReader(tr, limit+1)); err != nil {
			return nil, fmt.Errorf("解壓 sshm 失敗：%w", err)
		}
		if int64(len(bin)) > limit {
			return nil, fmt.Errorf("壓縮檔裡的 sshm 超過大小上限（%d bytes）", limit)
		}
	}
	if !found {
		return nil, errors.New("壓縮檔裡沒有 sshm")
	}
	if len(bin) == 0 {
		return nil, errors.New("壓縮檔裡的 sshm 是空檔")
	}
	return bin, nil
}

// tempPrefix 是 writeFileAtomic 暫存檔的前綴；CreateTemp 會在後面接一串數字。
const tempPrefix = ".sshm-update-"

// staleTempAge 之內的暫存檔可能是另一個 sshm 正在寫，不清。
const staleTempAge = 10 * time.Minute

// removeStaleTemps 清掉 dir 裡先前更新中斷留下的暫存檔：只認「前綴＋純數字」的一般檔案，
// 不跟隨 symlink、不碰目錄與其他檔案。盡力而為，失敗一律略過。
func removeStaleTemps(dir string, now time.Time) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		suffix, ok := strings.CutPrefix(e.Name(), tempPrefix)
		if !ok || suffix == "" || strings.Trim(suffix, "0123456789") != "" || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info() // lstat，不跟隨 symlink
		if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) < staleTempAge {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// syncDir 讓 rename 本身寫入磁碟；可替換以便測試。
var syncDir = func(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// writeFileAtomic 寫到同目錄的暫存檔、fsync、設權限後 rename 取代 target，再 fsync 目錄；
// perm 為 0 時沿用 target 現有的權限。失敗時移除暫存檔，target 不變。
func writeFileAtomic(target string, data []byte, perm fs.FileMode) (err error) {
	if perm == 0 {
		st, serr := os.Stat(target)
		if serr != nil {
			return serr
		}
		perm = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), tempPrefix+"*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(name, perm); err != nil {
		return err
	}
	if err = os.Rename(name, target); err != nil {
		return err
	}
	// 替換已完成；部分檔案系統（或 Windows）不支援目錄 fsync，失敗只代表斷電時可能回到舊檔，不算更新失敗
	_ = syncDir(filepath.Dir(target))
	return nil
}
