package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewID 產生帶前綴的隨機 id，例如 h_3f9a1c2b7d4e5f60。
func NewID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("無法取得亂數：%v", err))
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// HostIndex 回傳 id 對應的索引，找不到為 -1。
func (f *File) HostIndex(id string) int {
	for i, h := range f.Hosts {
		if h.ID == id {
			return i
		}
	}
	return -1
}

// HasGroup 判斷群組是否已存在。
func (f *File) HasGroup(name string) bool {
	for _, g := range f.Groups {
		if g.Name == name {
			return true
		}
	}
	return false
}

// EnsureGroup 群組不存在時新增（不帶顏色）；空名稱不處理。
func (f *File) EnsureGroup(name string) {
	if name == "" || f.HasGroup(name) {
		return
	}
	f.Groups = append(f.Groups, Group{Name: name})
}

// UpsertHost 驗證後新增或更新機器；新機器沒有 id 時自動產生。回傳最終 id。
func (f *File) UpsertHost(h Host) (string, error) {
	if err := ValidateHost(h); err != nil {
		return "", err
	}
	if h.ID == "" {
		h.ID = NewID("h")
	}
	f.EnsureGroup(h.Group)
	if i := f.HostIndex(h.ID); i >= 0 {
		f.Hosts[i] = h
	} else {
		f.Hosts = append(f.Hosts, h)
	}
	return h.ID, nil
}

// DuplicateHost 複製一台機器並插在原機器之後，回傳新 id。
func (f *File) DuplicateHost(id string) (string, error) {
	i := f.HostIndex(id)
	if i < 0 {
		return "", fmt.Errorf("找不到機器 %s", id)
	}
	c := f.Hosts[i]
	c.ID = NewID("h")
	c.Name += " (複本)"
	if c.Extra != nil {
		cp := make(Extra, len(c.Extra))
		for k, v := range c.Extra {
			cp[k] = v
		}
		c.Extra = cp
	}
	f.Hosts = append(f.Hosts[:i+1], append([]Host{c}, f.Hosts[i+1:]...)...)
	return c.ID, nil
}

// DeleteHost 刪除機器；tunnel 中引用它的 hostId 保留不動。
func (f *File) DeleteHost(id string) error {
	i := f.HostIndex(id)
	if i < 0 {
		return fmt.Errorf("找不到機器 %s", id)
	}
	f.Hosts = append(f.Hosts[:i], f.Hosts[i+1:]...)
	return nil
}
