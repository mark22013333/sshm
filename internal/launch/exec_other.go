//go:build !unix

package launch

import "errors"

// Exec 在非 unix 平台尚未支援。
func Exec(argv []string) error {
	return errors.New("此平台尚未支援原地連線")
}
