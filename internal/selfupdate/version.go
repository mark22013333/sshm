// Package selfupdate 檢查 GitHub 上的最新釋出版，並把目前的 sshm 執行檔原子替換成新版。
package selfupdate

import (
	"strings"

	"golang.org/x/mod/semver"
)

// Normalize 把 GoReleaser 寫入的 0.3.0 補成 v0.3.0；其他值只去頭尾空白。
func Normalize(v string) string {
	v = strings.TrimSpace(v)
	if v != "" && v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

// IsRelease 回報 v 是否為正式釋出版：完整的 vX.Y.Z，沒有預發版或 build 標記。
// (devel)、pseudo-version、goreleaser snapshot、+dirty 都不算。
func IsRelease(v string) bool {
	v = Normalize(v)
	return semver.IsValid(v) && semver.Canonical(v) == v && semver.Prerelease(v) == ""
}

// IsNewer 回報 latest 是否為比 current 新的正式版；任一方不是正式版時一律 false。
func IsNewer(latest, current string) bool {
	if !IsRelease(latest) || !IsRelease(current) {
		return false
	}
	return semver.Compare(Normalize(latest), Normalize(current)) > 0
}
