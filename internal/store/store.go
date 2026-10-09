package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ErrLockTimeout 表示等不到檔案鎖。
var ErrLockTimeout = errors.New("另一個 sshm 正在存檔，請稍後再試")

// lockTimeout 是等待檔案鎖的上限；測試可縮短。
var lockTimeout = 5 * time.Second

const (
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600
	fileName             = "hosts.json"
)

// Dir 回傳資料目錄：$XDG_CONFIG_HOME/sshm，未設定（或非絕對路徑）時為 ~/.config/sshm。
func Dir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "sshm"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("找不到家目錄：%w", err)
	}
	return filepath.Join(home, ".config", "sshm"), nil
}

// HostsPath 回傳 hosts.json 的完整路徑。
func HostsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// EnsureDir 建立資料目錄並確保權限為 0700。
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("建立資料目錄失敗：%w", err)
	}
	if err := os.Chmod(dir, dirPerm); err != nil {
		return fmt.Errorf("設定資料目錄權限失敗：%w", err)
	}
	return nil
}

// NewFile 回傳空的資料檔內容。
func NewFile() *File {
	return &File{Version: CurrentVersion, Groups: []Group{}, Hosts: []Host{}, Tunnels: []Tunnel{}}
}

// resolveTarget 回傳實際要讀寫的檔案：path 是 symlink 時為其目標；懸空 symlink 回傳清楚的錯誤。
func resolveTarget(path string) (string, error) {
	st, err := os.Lstat(path)
	if err != nil || st.Mode()&fs.ModeSymlink == 0 {
		return path, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		dest, _ := os.Readlink(path)
		return "", fmt.Errorf("%s 是 symlink，但指向的 %s 不存在；請建立該檔案或修正連結", path, dest)
	}
	return resolved, nil
}

// withLock 確保目錄後在鎖檔內執行 fn；鎖檔放在實際檔案（symlink 目標）旁，讓指向同一目標的連結互斥。
func withLock(path string, fn func() error) error {
	if err := EnsureDir(filepath.Dir(path)); err != nil {
		return err
	}
	target, err := resolveTarget(path)
	if err != nil {
		return err
	}
	unlock, err := lockFile(target + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// load 讀取並解析 path；不存在時回傳 (nil, nil)。既有檔權限比 0600 寬時立即收緊。
func load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("讀取 %s 失敗：%w", path, err)
	}
	if st, err := os.Stat(path); err == nil && st.Mode().Perm()&^filePerm != 0 {
		if err := os.Chmod(path, st.Mode().Perm()&filePerm); err != nil {
			return nil, fmt.Errorf("收緊 %s 權限失敗：%w", path, err)
		}
	}
	return Parse(data)
}

// Open 讀取 path；檔案不存在時建立空檔（目錄 0700、檔案 0600）。
func Open(path string) (*File, error) {
	var f *File
	err := withLock(path, func() error {
		var err error
		if f, err = load(path); err != nil || f != nil {
			return err
		}
		f = NewFile()
		return save(path, f)
	})
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Update 在檔案鎖內重讀最新內容、套用 mutate、寫回，避免多個 sshm 同時存檔互相覆蓋。
// mutate 回傳錯誤時不寫檔。回傳寫入後的完整內容。
func Update(path string, mutate func(f *File) error) (*File, error) {
	var f *File
	err := withLock(path, func() error {
		var err error
		if f, err = load(path); err != nil {
			return err
		}
		if f == nil {
			f = NewFile()
		}
		if err := mutate(f); err != nil {
			return err
		}
		return save(path, f)
	})
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Parse 解析 hosts.json 內容。
func Parse(data []byte) (*File, error) {
	f := NewFile()
	if len(bytes.TrimSpace(data)) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(data, f); err != nil {
		return nil, fmt.Errorf("hosts.json 格式錯誤：%w", err)
	}
	if f.Version == 0 {
		f.Version = CurrentVersion
	}
	return f, nil
}

// Encode 輸出縮排後的 JSON（結尾含換行）。
func Encode(f *File) ([]byte, error) {
	raw, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// Save 在檔案鎖內整份寫入 path（會覆蓋他人的變更；一般修改請用 Update）。
func Save(path string, f *File) error {
	return withLock(path, func() error { return save(path, f) })
}

func save(path string, f *File) error {
	data, err := Encode(f)
	if err != nil {
		return fmt.Errorf("序列化失敗：%w", err)
	}
	return WriteFileAtomic(path, data, filePerm)
}

// WriteFileAtomic 以同目錄暫存檔 → fsync → rename → fsync 目錄寫入；path 是 symlink 時寫到其目標，保留連結。
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) (err error) {
	target, err := resolveTarget(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("建立暫存檔失敗：%w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(perm); err != nil {
		return fmt.Errorf("設定暫存檔權限失敗：%w", err)
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("寫入暫存檔失敗：%w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("寫入暫存檔失敗：%w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("關閉暫存檔失敗：%w", err)
	}
	if err = os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("取代 %s 失敗：%w", target, err)
	}
	// rename 已完成，目錄 fsync 失敗只影響斷電耐久性，不回報為寫入失敗
	syncDir(dir)
	return nil
}

func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
