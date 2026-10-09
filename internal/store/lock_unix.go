//go:build unix

package store

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// lockFile 以 flock 取得獨占鎖，最多等 lockTimeout，回傳解鎖函式。鎖檔不隨資料檔 rename 而換 inode。
func lockFile(lockPath string) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		return nil, fmt.Errorf("開啟鎖檔失敗：%w", err)
	}
	if err := f.Chmod(filePerm); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("設定鎖檔權限失敗：%w", err)
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EINTR {
			_ = f.Close()
			return nil, fmt.Errorf("取得檔案鎖失敗：%w", err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, ErrLockTimeout
		}
		time.Sleep(50 * time.Millisecond)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
