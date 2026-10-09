// Package connect 負責把機器資料組成連線指令，以及決定在哪裡開啟。
package connect

import "strings"

// Quote 以 POSIX shell 規則引用單一參數；安全字元組成的字串原樣回傳。
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	// zsh 會把開頭的 = 展開成指令路徑（EQUALS 選項）
	safe := s[0] != '='
	for _, r := range s {
		if !isSafeRune(r) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isSafeRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("@%+=:,./-_", r)
}

// ShellText 把 argv 組成可打進 shell 的一行文字；開頭加空白讓 zsh HIST_IGNORE_SPACE 不記錄。
func ShellText(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = Quote(a)
	}
	return " " + strings.Join(parts, " ")
}
