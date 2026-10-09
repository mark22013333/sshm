// Package tunnelstate 記錄執行中 tunnel 的 Orca terminal handle（~/.config/sshm/state.json）。
package tunnelstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mark22013333/sshm/internal/store"
)

const fileName = "state.json"

// Entry 是一條執行中 tunnel 的紀錄。
type Entry struct {
	Handle    string `json:"handle"`
	StartedAt string `json:"startedAt,omitempty"`
}

// State 是 state.json 的內容；key 為 tunnel id。
type State struct {
	Version int              `json:"version"`
	Tunnels map[string]Entry `json:"tunnels"`
}

// Path 回傳資料目錄下 state.json 的路徑。
func Path() (string, error) {
	dir, err := store.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

func empty() *State { return &State{Version: 1, Tunnels: map[string]Entry{}} }

// Load 讀取狀態；檔案不存在時回傳空狀態。
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("讀取 %s 失敗：%w", path, err)
	}
	s := empty()
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("%s 格式錯誤：%w", path, err)
	}
	if s.Tunnels == nil {
		s.Tunnels = map[string]Entry{}
	}
	return s, nil
}

// Update 在鎖內重讀、套用 fn、原子寫回（0600）。
func Update(path string, fn func(s *State) error) (*State, error) {
	var out *State
	err := store.WithLock(path, func() error {
		s, err := Load(path)
		if err != nil {
			return err
		}
		if err := fn(s); err != nil {
			return err
		}
		data, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return err
		}
		out = s
		return store.WriteFileAtomic(path, append(data, '\n'), 0o600)
	})
	return out, err
}
