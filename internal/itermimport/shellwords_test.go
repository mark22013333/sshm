package itermimport

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type splitCase struct {
	text    string
	want    []string // login.exp 之後的 4 個參數；nil 表示應無法匯入
	wantErr string
	oracle  bool // 是否拿 /bin/sh 的實際結果比對
}

var splitCases = []splitCase{
	{`~/bin/login.exp 22 u h "pa\$ss"`, []string{"22", "u", "h", "pa$ss"}, "", true},
	{`~/bin/login.exp 22 u h "pa\xss"`, []string{"22", "u", "h", `pa\xss`}, "", true},
	{`~/bin/login.exp 22 u h 'pa\xss'`, []string{"22", "u", "h", `pa\xss`}, "", true},
	{`/usr/local/bin/login.exp 22 u h pa\ ss`, []string{"22", "u", "h", "pa ss"}, "", true},
	{"./login.exp  22   u  h  p#q\n", []string{"22", "u", "h", "p#q"}, "", true},
	{`./login.exp 22 u h #pw`, nil, "實際 3 個", true},
	{`./login.exp 22 u h $'a\tb'`, nil, "$", false},
	{`expect ./login.exp 22 u h pw`, []string{"22", "u", "h", "pw"}, "", true},
	{`./login.exp 22 u h pw; exit`, []string{"22", "u", "h", "pw"}, "", false},
	{"./login.exp 22 u h pw\r\n", []string{"22", "u", "h", "pw"}, "", true},
	{`./login.exp 22 u h "pw\"x"`, []string{"22", "u", "h", `pw"x`}, "", true},
	{`./login.exp 22 u h "pw\\x"`, []string{"22", "u", "h", `pw\x`}, "", true},
	{`./login.exp 22 u h pw\x`, []string{"22", "u", "h", "pwx"}, "", true},
	{`./login.exp 22 u h a"b"c`, []string{"22", "u", "h", "abc"}, "", true},
	{`./login.exp 22 u h "a\nb"`, []string{"22", "u", "h", `a\nb`}, "", true},
	{`./login.exp 22 u h 'a\b'`, []string{"22", "u", "h", `a\b`}, "", true},
	{`./login.exp 22 u h a\ b`, []string{"22", "u", "h", "a b"}, "", true},
	{`./login.exp 22 u h 'it'\''s'`, []string{"22", "u", "h", "it's"}, "", true},
	{`./login.exp 22 u h "unclosed`, nil, "引號未閉合", false},
	{`./login.exp 22 u h 'unclosed`, nil, "引號未閉合", false},
	{`./login.exp 22 u h $HOME`, nil, "$", false},
	{`./login.exp 22 u h "x$HOME"`, nil, "$", false},
	{"./login.exp 22 u h `id`", nil, "反引號", false},
	{`./login.exp 22 u h pw*`, nil, "萬用字元", false},
	{`./login.exp 22 u h ~pw`, nil, "~", false},
	{`$HOME/login.exp 22 u h 'ok'`, []string{"22", "u", "h", "ok"}, "", false},
}

func TestLoginArgsPOSIX(t *testing.T) {
	for _, c := range splitCases {
		got, isLogin, err := loginArgs(c.text)
		if !isLogin {
			t.Errorf("%q: 應辨識為 login.exp", c.text)
			continue
		}
		if c.want == nil {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%q: err = %v, want 含 %q", c.text, err, c.wantErr)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %q err=%v, want %q", c.text, got, err, c.want)
		}
	}
}

// 以 /bin/sh 的實際拆分結果當 oracle（只用合成字串；在空目錄、假 HOME 下執行，避免展開到真實檔案）。
func TestLoginArgsMatchesSh(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("沒有 /bin/sh")
	}
	dir := t.TempDir()
	for _, c := range splitCases {
		if !c.oracle {
			continue
		}
		text := strings.TrimRight(c.text, " \t\r\n")
		cmd := exec.Command("/bin/sh", "-c", `printf '%s\000' `+text)
		cmd.Dir = dir
		cmd.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin"}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%q: sh 失敗：%v", c.text, err)
		}
		words := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		var shArgs []string
		for i, w := range words {
			if filepath.Base(w) == "login.exp" {
				shArgs = words[i+1:]
				break
			}
		}
		got, _, err := loginArgs(c.text)
		if len(shArgs) != 4 {
			if err == nil {
				t.Errorf("%q: sh 得到 %d 個參數 %q，但我們接受了 %q", c.text, len(shArgs), shArgs, got)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, shArgs) {
			t.Errorf("%q: 我們 %q（err=%v）≠ sh %q", c.text, got, err, shArgs)
		}
	}
}
