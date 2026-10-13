// Package agentaccess 把 sshm 管理的機器開通給 AI agent：每台一把專用金鑰、一個 ssh 別名，
// agent 只用 `ssh <別名>` 連線，不會接觸到 sshm 存的密碼。
package agentaccess

import (
	"strconv"
	"strings"

	"github.com/mark22013333/sshm/internal/store"
)

// AliasPrefix 是預設別名的前綴。
const AliasPrefix = "sshm-"

// slug 取出英數（轉小寫），其餘字元轉成 -，合併連續的 -、去掉頭尾的 -。
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// fit 截斷到長度上限並去掉結尾的 -。
func fit(s string, limit int) string {
	if len(s) > limit {
		s = s[:limit]
	}
	return strings.TrimRight(s, "-")
}

// DefaultAlias 依機器名稱的英數部分產生別名；沒有英數時改用 host，與 taken 衝突時補 -2、-3…
func DefaultAlias(h store.Host, taken map[string]bool) string {
	base := slug(h.Name)
	if base == "" {
		base = slug(h.Host)
	}
	if base == "" {
		base = "host"
	}
	base = fit(AliasPrefix+base, store.AgentAliasMaxLen)
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		cand := fit(base, store.AgentAliasMaxLen-len(suffix)) + suffix
		if !taken[cand] {
			return cand
		}
	}
}

// TakenAliases 回傳 except 以外已開通機器的別名。
func TakenAliases(f *store.File, except string) map[string]bool {
	taken := map[string]bool{}
	for _, h := range f.Hosts {
		if h.Agent != nil && h.ID != except {
			taken[h.Agent.Alias] = true
		}
	}
	return taken
}
