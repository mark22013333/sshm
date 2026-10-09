package store

import (
	"errors"
	"fmt"
)

// ErrTunnelGone 表示要編輯的 tunnel 已被其他 sshm 刪除。
var ErrTunnelGone = errors.New("這條 tunnel 已被其他 sshm 刪除，無法儲存編輯")

// TunnelIndex 回傳 id 對應的索引，找不到為 -1。
func (f *File) TunnelIndex(id string) int {
	for i, t := range f.Tunnels {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// ValidateTunnelIn 除了規則本身，也確認 hostId 指向這份資料中存在的機器。
func (f *File) ValidateTunnelIn(t Tunnel) error {
	if err := ValidateTunnel(t); err != nil {
		return err
	}
	if f.HostIndex(t.HostID) < 0 {
		return fmt.Errorf("機器 %s 不存在", Printable(t.HostID))
	}
	return nil
}

// UpsertTunnel 驗證後新增（id 為空時產生）或依 id 更新；更新時 id 已不存在則回 ErrTunnelGone。
func (f *File) UpsertTunnel(t Tunnel, editing bool) (string, error) {
	if err := f.ValidateTunnelIn(t); err != nil {
		return "", err
	}
	if editing {
		i := f.TunnelIndex(t.ID)
		if i < 0 {
			return "", ErrTunnelGone
		}
		f.Tunnels[i] = t
		return t.ID, nil
	}
	if t.ID == "" {
		t.ID = NewID("t")
	}
	f.Tunnels = append(f.Tunnels, t)
	return t.ID, nil
}

// DeleteTunnel 刪除 tunnel。
func (f *File) DeleteTunnel(id string) error {
	i := f.TunnelIndex(id)
	if i < 0 {
		return fmt.Errorf("找不到 tunnel %s", Printable(id))
	}
	f.Tunnels = append(f.Tunnels[:i], f.Tunnels[i+1:]...)
	return nil
}

// TunnelsUsingHost 回傳引用該機器的 tunnel 名稱。
func (f *File) TunnelsUsingHost(hostID string) []string {
	var names []string
	for _, t := range f.Tunnels {
		if t.HostID == hostID {
			names = append(names, t.Name)
		}
	}
	return names
}
