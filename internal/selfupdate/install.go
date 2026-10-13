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

// writeFileAtomic 寫到同目錄的暫存檔、fsync、設權限後 rename 取代 target；
// perm 為 0 時沿用 target 現有的權限。失敗時移除暫存檔，target 不變。
func writeFileAtomic(target string, data []byte, perm fs.FileMode) (err error) {
	if perm == 0 {
		st, serr := os.Stat(target)
		if serr != nil {
			return serr
		}
		perm = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".sshm-update-*")
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
	return os.Rename(name, target)
}
