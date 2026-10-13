//go:build !unix

package selfupdate

import "os"

// dirWritable 在非 unix 平台以建立暫存檔試寫。
func dirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".sshm-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}
