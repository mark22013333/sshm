package launch

import "strings"

// mergeEnv 回傳 base 加上 extra；extra 裡的 key 會取代 base 中同名的變數。
// 不去重的話，使用者環境裡殘留的同名變數排在前面，子行程會讀到舊值。
func mergeEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	override := make(map[string]bool, len(extra))
	for _, kv := range extra {
		override[envKey(kv)] = true
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		if !override[envKey(kv)] {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}

func envKey(kv string) string {
	if i := strings.IndexByte(kv, '='); i >= 0 {
		return kv[:i]
	}
	return kv
}
