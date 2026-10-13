package agentaccess

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark22013333/sshm/internal/connect"
)

// Key 是一台機器的專用金鑰。
type Key struct {
	Private     string // 私鑰路徑
	Public      string // 公鑰路徑
	TypeBlob    string // 「ssh-ed25519 AAAA…」（不含註解）
	Fingerprint string // SHA256:…
	Created     bool   // 本次新產生（取消或失敗時要清掉）
}

// KeyComment 是公鑰的註解，也是遠端 authorized_keys 行尾的標記。
func KeyComment(alias string) string { return "sshm-agent:" + alias }

// ensureKeyDir 建立 ~/.ssh/sshm 並確保 0700。
func ensureKeyDir(p Paths) error {
	if err := ensureSSHDir(p.SSHDir); err != nil {
		return err
	}
	if err := os.MkdirAll(p.KeyDir, 0o700); err != nil {
		return fmt.Errorf("建立 %s 失敗：%w", p.KeyDir, err)
	}
	return os.Chmod(p.KeyDir, 0o700)
}

// KeyPath 回傳別名對應的私鑰路徑。
func (p Paths) KeyPath(alias string) string { return filepath.Join(p.KeyDir, alias) }

// EnsureKey 取得別名的 ed25519 金鑰：已存在就沿用，否則以 ssh-keygen 產生（無 passphrase、私鑰 0600）。
func EnsureKey(ctx context.Context, run connect.Runner, p Paths, alias string) (Key, error) {
	if err := ensureKeyDir(p); err != nil {
		return Key{}, err
	}
	k := Key{Private: p.KeyPath(alias), Public: p.KeyPath(alias) + ".pub"}
	_, privErr := os.Stat(k.Private)
	_, pubErr := os.Stat(k.Public)
	switch {
	case privErr == nil && pubErr == nil:
	case errors.Is(privErr, fs.ErrNotExist) && errors.Is(pubErr, fs.ErrNotExist):
		argv := []string{"ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", KeyComment(alias), "-f", k.Private}
		if _, stderr, err := run(ctx, argv); err != nil {
			return Key{}, fmt.Errorf("產生金鑰失敗：%v %s", err, strings.TrimSpace(string(stderr)))
		}
		k.Created = true
	default:
		return Key{}, fmt.Errorf("%s 只有私鑰或公鑰其中之一，請檢查後再試", k.Private)
	}
	if err := os.Chmod(k.Private, 0o600); err != nil {
		return Key{}, fmt.Errorf("設定私鑰權限失敗：%w", err)
	}
	pub, err := os.ReadFile(k.Public)
	if err != nil {
		return Key{}, fmt.Errorf("讀取公鑰失敗：%w", err)
	}
	k.TypeBlob, k.Fingerprint, err = parsePublicKey(string(pub))
	if err != nil {
		return Key{}, err
	}
	return k, nil
}

// parsePublicKey 取出「類型 base64」並計算 OpenSSH 格式的 SHA256 指紋。
func parsePublicKey(line string) (string, string, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "ssh-ed25519" {
		return "", "", errors.New("公鑰格式不是 ssh-ed25519")
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", "", fmt.Errorf("公鑰內容無法解析：%w", err)
	}
	sum := sha256.Sum256(blob)
	return fields[0] + " " + fields[1], "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]), nil
}

// RemoveKey 刪除別名的金鑰檔；只刪 ~/.ssh/sshm 底下的檔案，避免手改 hosts.json 指到別處時誤刪。
func RemoveKey(p Paths, private string) ([]string, error) {
	if filepath.Dir(filepath.Clean(private)) != filepath.Clean(p.KeyDir) {
		return nil, fmt.Errorf("金鑰 %s 不在 %s 底下，未刪除，請自行確認", private, p.KeyDir)
	}
	var removed []string
	for _, path := range []string{private, private + ".pub"} {
		err := os.Remove(path)
		switch {
		case err == nil:
			removed = append(removed, path)
		case errors.Is(err, fs.ErrNotExist):
		default:
			return removed, fmt.Errorf("刪除 %s 失敗：%w", path, err)
		}
	}
	return removed, nil
}
