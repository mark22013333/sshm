//go:build unix

// Package launch 集中平台相關的「原地執行」實作。
package launch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// Exec 以 syscall.Exec 取代目前行程執行 argv；成功時不會回傳。
func Exec(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("沒有要執行的指令")
	}
	bin := argv[0]
	if !filepath.IsAbs(bin) {
		p, err := exec.LookPath(bin)
		if err != nil {
			return fmt.Errorf("找不到 %s：%w", bin, err)
		}
		bin = p
	}
	if err := syscall.Exec(bin, argv, os.Environ()); err != nil {
		return fmt.Errorf("執行 %s 失敗：%w", bin, err)
	}
	return nil
}
