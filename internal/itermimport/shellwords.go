package itermimport

import (
	"errors"
	"strings"
)

// word 是依 POSIX sh 規則拆出的一個字；op 為 ; | & < > ( ) 或換行等指令分隔符號。
type word struct {
	text   string
	op     bool
	unsafe string // 非空表示這個字會被 shell 展開（$、反引號、萬用字元、開頭的 ~），無法確定實際值
}

var errUnclosedQuote = errors.New("引號未閉合")

const operatorChars = ";|&<>()"

// splitShellWords 依 POSIX sh 規則拆分一行指令：單引號內全為字面值；雙引號內只有 \ 接 $ ` " \ 換行才是跳脫；
// 引號外 \ 跳脫下一字元；未加引號、位於字首的 # 起始註解。展開類語法不執行，只在該字標記 unsafe。
func splitShellWords(s string) ([]word, error) {
	var (
		words   []word
		cur     strings.Builder
		inWord  bool
		unsafe  string
		r       = []rune(s)
		markBad = func(reason string) {
			if unsafe == "" {
				unsafe = reason
			}
		}
	)
	flush := func() {
		if inWord {
			words = append(words, word{text: cur.String(), unsafe: unsafe})
		}
		cur.Reset()
		inWord, unsafe = false, ""
	}
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == ' ' || c == '\t':
			flush()
		case c == '\n' || strings.ContainsRune(operatorChars, c):
			flush()
			words = append(words, word{text: string(c), op: true})
		case c == '#' && !inWord:
			// 註解：略過到行尾
			for i+1 < len(r) && r[i+1] != '\n' {
				i++
			}
		case c == '\\':
			if i+1 >= len(r) {
				// 行尾單獨的反斜線：sh 視為字面值
				cur.WriteRune(c)
				inWord = true
				break
			}
			i++
			if r[i] == '\n' {
				break // 續行
			}
			cur.WriteRune(r[i])
			inWord = true
		case c == '\'':
			inWord = true
			j := i + 1
			for j < len(r) && r[j] != '\'' {
				j++
			}
			if j >= len(r) {
				return nil, errUnclosedQuote
			}
			cur.WriteString(string(r[i+1 : j]))
			i = j
		case c == '"':
			inWord = true
			j := i + 1
			closed := false
			for ; j < len(r); j++ {
				d := r[j]
				if d == '"' {
					closed = true
					break
				}
				if d == '\\' && j+1 < len(r) && strings.ContainsRune("$`\"\\\n", r[j+1]) {
					j++
					if r[j] != '\n' {
						cur.WriteRune(r[j])
					}
					continue
				}
				if d == '$' || d == '`' {
					markBad("雙引號內含 $ 或反引號（會被 shell 展開）")
				}
				cur.WriteRune(d)
			}
			if !closed {
				return nil, errUnclosedQuote
			}
			i = j
		case c == '$' || c == '`':
			markBad("含未加引號的 $ 或反引號（會被 shell 展開）")
			cur.WriteRune(c)
			inWord = true
		case c == '*' || c == '?' || c == '[':
			markBad("含未加引號的萬用字元（* ? [）")
			cur.WriteRune(c)
			inWord = true
		case c == '~' && !inWord:
			markBad("字首有未加引號的 ~（會被 shell 展開）")
			cur.WriteRune(c)
			inWord = true
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	flush()
	return words, nil
}
