// Package catalog 負責機器清單的搜尋過濾與依群組分組。
package catalog

import (
	"strings"

	"github.com/mark22013333/sshm/internal/store"
)

// Section 是畫面上的一個群組區塊。
type Section struct {
	Name  string // 空字串代表未分組
	Color string
	Hosts []store.Host
}

// Match 判斷機器是否符合搜尋字（名稱、host、user、群組；不分大小寫、子字串）。
func Match(h store.Host, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	for _, field := range []string{h.Name, h.Host, h.User, h.Group} {
		if strings.Contains(strings.ToLower(field), q) {
			return true
		}
	}
	return false
}

// Build 依群組分組並過濾：先依 groups 定義順序，再是只出現在機器上的群組，未分組放最後；空群組不列出。
func Build(f *store.File, query string) []Section {
	index := map[string]int{}
	var sections []Section
	add := func(name string) {
		if _, ok := index[name]; ok {
			return
		}
		index[name] = len(sections)
		sections = append(sections, Section{Name: name, Color: f.GroupColor(name)})
	}
	for _, g := range f.Groups {
		if g.Name != "" {
			add(g.Name)
		}
	}
	for _, h := range f.Hosts {
		if h.Group != "" {
			add(h.Group)
		}
	}
	add("")
	for _, h := range f.Hosts {
		if Match(h, query) {
			s := &sections[index[h.Group]]
			s.Hosts = append(s.Hosts, h)
		}
	}
	out := sections[:0]
	for _, s := range sections {
		if len(s.Hosts) > 0 {
			out = append(out, s)
		}
	}
	return out
}
