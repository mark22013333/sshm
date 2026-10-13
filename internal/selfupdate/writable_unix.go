//go:build unix

package selfupdate

import "golang.org/x/sys/unix"

// dirWritable 以 access(2) 判斷目前使用者能否在 dir 建立與 rename 檔案，不實際寫入。
func dirWritable(dir string) error {
	return unix.Access(dir, unix.W_OK|unix.X_OK)
}
