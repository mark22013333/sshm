package connect

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mark22013333/sshm/assets"
	"github.com/mark22013333/sshm/internal/store"
)

// LoginScriptName 是寫到資料目錄的 expect 腳本檔名。
const LoginScriptName = "sshm-login.exp"

// EnsureLoginScript 確保 dir 中的 expect 腳本與內嵌版本一致、權限 0700，回傳其路徑。
func EnsureLoginScript(dir string) (string, error) {
	if err := store.EnsureDir(dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, LoginScriptName)
	cur, err := os.ReadFile(path)
	switch {
	case err == nil && bytes.Equal(cur, assets.LoginScript):
		return path, os.Chmod(path, 0o700)
	case err == nil, errors.Is(err, fs.ErrNotExist):
		return path, store.WriteFileAtomic(path, assets.LoginScript, 0o700)
	default:
		return "", err
	}
}
