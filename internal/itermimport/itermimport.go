// Package itermimport 從 iTerm2 Profiles 讀出以 login.exp 登入的機器（資料只留在記憶體）。
package itermimport

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mark22013333/sshm/internal/store"
)

// DefaultPlistPath 回傳 iTerm2 設定檔的預設位置（相對於家目錄）。
const DefaultPlistPath = "Library/Preferences/com.googlecode.iterm2.plist"

// Runner 執行外部指令並分別回傳 stdout、stderr。
type Runner func(ctx context.Context, argv []string) (stdout, stderr []byte, err error)

// Bookmark 是 iTerm2 的一個 profile（只取需要的欄位）。
type Bookmark struct {
	Name        string
	Tags        []string
	InitialText string
	TagsNote    string // Tags 格式不符或含控制字元而被忽略時的說明
}

// ReadBookmarks 以 plutil 只取出 "New Bookmarks" 段落；輸出直接讀 stdout，不落地。
func ReadBookmarks(run Runner, plistPath string) ([]Bookmark, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, stderr, err := run(ctx, []string{"plutil", "-extract", "New Bookmarks", "json", "-o", "-", plistPath})
	if err != nil {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("讀取 iTerm2 設定失敗（%s）：%s", plistPath, msg)
	}
	return ParseBookmarks(out)
}

// ParseBookmarks 解析 plutil 輸出的 JSON 陣列；單一 profile 欄位型別不符時只略過該欄位。
func ParseBookmarks(data []byte) ([]Bookmark, error) {
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("iTerm2 的 New Bookmarks 不是預期的格式：%w", err)
	}
	out := make([]Bookmark, 0, len(raw))
	for _, m := range raw {
		var b Bookmark
		_ = json.Unmarshal(m["Name"], &b.Name)
		_ = json.Unmarshal(m["Initial Text"], &b.InitialText)
		if raw, ok := m["Tags"]; ok && string(raw) != "null" {
			var items []json.RawMessage
			if json.Unmarshal(raw, &items) != nil {
				b.TagsNote = "Tags 不是清單，已忽略"
			}
			for _, it := range items {
				var t string
				if json.Unmarshal(it, &t) != nil {
					b.TagsNote = "部分 Tags 不是文字，已忽略"
					continue
				}
				t = strings.TrimSpace(t)
				if t != store.Printable(t) {
					b.TagsNote = "部分 Tags 含控制字元，已忽略"
					continue
				}
				if t != "" {
					b.Tags = append(b.Tags, t)
				}
			}
		}
		out = append(out, b)
	}
	return out, nil
}

// Status 是候選的狀態。
type Status int

const (
	StatusOK        Status = iota
	StatusDuplicate        // 已有同 host+port+user
	StatusInvalid          // 無法解析或資料不合法
)

// Candidate 是預覽畫面的一列。
type Candidate struct {
	Name     string
	Tags     []string
	TagIndex int // 多 tag 時選用的群組
	Host     store.Host
	Status   Status
	Reason   string // 無法匯入或重複的原因；不含密碼
	Note     string // 其他提示（例如 Tags 格式不符）
	Selected bool
}

// Group 回傳目前選用的群組（沒有 tag 時為空字串）。
func (c *Candidate) Group() string {
	if len(c.Tags) == 0 {
		return ""
	}
	return c.Tags[c.TagIndex]
}

// CycleGroup 在多個 tag 間切換群組。
func (c *Candidate) CycleGroup(delta int) {
	if n := len(c.Tags); n > 1 {
		c.TagIndex = ((c.TagIndex+delta)%n + n) % n
	}
}

// Plan 是解析結果。
type Plan struct {
	Candidates []Candidate
	Skipped    int // 非 login.exp 的 profile 數
}

func dupKey(host string, port int, user string) string {
	if port <= 0 {
		port = 22
	}
	return strings.ToLower(host) + "\x00" + strconv.Itoa(port) + "\x00" + user
}

// Build 把 profiles 轉成候選；existing 用來判斷重複。
func Build(bookmarks []Bookmark, existing *store.File) Plan {
	existingKeys := map[string]bool{}
	for _, h := range existing.Hosts {
		existingKeys[dupKey(h.Host, h.Port, h.User)] = true
	}
	batchKeys := map[string]bool{}
	var p Plan
	for _, b := range bookmarks {
		args, isLogin, err := loginArgs(b.InitialText)
		if !isLogin {
			p.Skipped++
			continue
		}
		c := Candidate{Name: b.Name, Tags: b.Tags, Note: b.TagsNote}
		if err != nil {
			c.Status, c.Reason = StatusInvalid, err.Error()
			p.Candidates = append(p.Candidates, c)
			continue
		}
		port, perr := strconv.Atoi(args[0])
		if perr != nil || port < 1 || port > 65535 {
			c.Status, c.Reason = StatusInvalid, "第 1 個參數（port）不是 1–65535 的數字"
			p.Candidates = append(p.Candidates, c)
			continue
		}
		c.Host = store.Host{
			Name: strings.TrimSpace(b.Name), Group: c.Group(),
			Host: args[2], Port: port, User: args[1],
			Auth: store.AuthPassword, Password: args[3],
		}
		if err := store.ValidateHost(c.Host); err != nil {
			c.Status, c.Reason = StatusInvalid, err.Error()
			p.Candidates = append(p.Candidates, c)
			continue
		}
		key := dupKey(c.Host.Host, c.Host.Port, c.Host.User)
		switch {
		case existingKeys[key]:
			c.Status, c.Reason = StatusDuplicate, "已有相同 host、port、user 的機器"
		case batchKeys[key]:
			c.Status, c.Reason = StatusDuplicate, "與本批另一個 profile 重複（相同 host、port、user）"
		default:
			c.Selected = true
		}
		batchKeys[key] = true
		p.Candidates = append(p.Candidates, c)
	}
	return p
}

// loginArgs 依 POSIX sh 規則拆分 Initial Text，回傳 login.exp 之後、下一個指令分隔符號之前的 4 個參數。
func loginArgs(text string) ([]string, bool, error) {
	if !strings.Contains(text, "login.exp") {
		return nil, false, nil
	}
	// iTerm2 送出 Initial Text 時會自己補換行，結尾的空白與換行不算內容
	words, err := splitShellWords(strings.TrimRight(text, " \t\r\n"))
	if err != nil {
		return nil, true, fmt.Errorf("Initial Text 無法依 shell 規則拆分：%v", err)
	}
	for i, w := range words {
		if w.op || filepath.Base(w.text) != "login.exp" {
			continue
		}
		var args []string
		for _, a := range words[i+1:] {
			if a.op {
				break
			}
			if a.unsafe != "" {
				return nil, true, fmt.Errorf("第 %d 個參數%s，無法確定實際值", len(args)+1, a.unsafe)
			}
			args = append(args, a.text)
		}
		if len(args) != 4 {
			return nil, true, fmt.Errorf("login.exp 之後應有 4 個參數（port user host password），實際 %d 個", len(args))
		}
		return args, true, nil
	}
	return nil, true, fmt.Errorf("Initial Text 含 login.exp 字樣，但找不到 login.exp 這個參數")
}

// Result 是寫入結果。
type Result struct {
	Added   int
	Skipped int // 未勾選、無法匯入、或寫入時才發現重複
}

// Apply 在 store 的鎖內更新中寫入勾選的候選；寫入時再以最新資料判斷一次重複。
func Apply(f *store.File, cands []Candidate) (Result, error) {
	var r Result
	seen := map[string]bool{}
	for _, h := range f.Hosts {
		seen[dupKey(h.Host, h.Port, h.User)] = true
	}
	for i := range cands {
		c := &cands[i]
		if !c.Selected || c.Status == StatusInvalid {
			r.Skipped++
			continue
		}
		h := c.Host
		h.Group = c.Group()
		key := dupKey(h.Host, h.Port, h.User)
		if seen[key] && c.Status != StatusDuplicate {
			r.Skipped++
			continue
		}
		if _, err := f.UpsertHost(h); err != nil {
			return r, fmt.Errorf("匯入「%s」失敗：%w", h.Name, err)
		}
		seen[key] = true
		r.Added++
	}
	return r, nil
}

// CountSelected 回傳實際會嘗試寫入的候選數（已勾選且可匯入）。
func CountSelected(cands []Candidate) int {
	n := 0
	for _, c := range cands {
		if c.Selected && c.Status != StatusInvalid {
			n++
		}
	}
	return n
}
