package agentaccess

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mark22013333/sshm/internal/store"
)

// ConfName 是 sshm 自管的別名設定檔名（放在 ~/.ssh）。
const ConfName = "sshm_agents.conf"

// Paths 是開通功能會讀寫的本機路徑。
type Paths struct {
	SSHDir string // ~/.ssh
	Config string // ~/.ssh/config
	Conf   string // ~/.ssh/sshm_agents.conf
	KeyDir string // ~/.ssh/sshm
	// IncludeArg 是寫進 ~/.ssh/config 的 Include 參數。
	IncludeArg string
}

// PathsFor 以 home 組出路徑。ssh 的 ~ 取的是 passwd 家目錄而非 $HOME，
// 兩者不同時（例如測試用臨時 HOME）Include 改寫絕對路徑，才會真的指到這份 conf。
func PathsFor(home, passwdHome string) Paths {
	ssh := filepath.Join(home, ".ssh")
	p := Paths{
		SSHDir: ssh,
		Config: filepath.Join(ssh, "config"),
		Conf:   filepath.Join(ssh, ConfName),
		KeyDir: filepath.Join(ssh, "sshm"),
	}
	p.IncludeArg = "~/.ssh/" + ConfName
	if passwdHome == "" || filepath.Clean(passwdHome) != filepath.Clean(home) {
		p.IncludeArg = quoteConfValue(p.Conf)
	}
	return p
}

// DefaultPaths 依 $HOME 組出路徑。
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("找不到家目錄：%w", err)
	}
	passwdHome := ""
	if u, err := user.Current(); err == nil {
		passwdHome = u.HomeDir
	}
	return PathsFor(home, passwdHome), nil
}

// quoteConfValue 含空白的值在 ssh_config 中要加雙引號（值已驗證不含雙引號）。
func quoteConfValue(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

// confHost 判斷機器能否安全寫成別名區塊；% 會被 ssh 當成 token 展開，一律不收。
func confHost(h store.Host) error {
	if h.Agent == nil {
		return errors.New("未開通")
	}
	if err := store.ValidateHost(h); err != nil {
		return err
	}
	if strings.Contains(h.Host, "%") || strings.Contains(h.AgentUser(), "%") {
		return errors.New("host 或 user 不可包含 %")
	}
	return nil
}

func effectivePort(p int) int {
	if p <= 0 {
		return 22
	}
	return p
}

const confHeader = "# 由 sshm 自動產生，請勿手動編輯；請用 sshm agent enable／disable 管理。\n" +
	"# 已開通給 AI agent 的機器，連線方式：ssh <別名>\n"

// RenderConf 產生 sshm_agents.conf 的內容，依別名排序；資料不合法的機器略過並回報名稱。
func RenderConf(f *store.File) ([]byte, []string) {
	var hosts []store.Host
	var skipped []string
	for _, h := range f.Hosts {
		if h.Agent == nil {
			continue
		}
		if err := confHost(h); err != nil {
			skipped = append(skipped, fmt.Sprintf("%s（%v）", store.Printable(h.Name), err))
			continue
		}
		hosts = append(hosts, h)
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].Agent.Alias < hosts[j].Agent.Alias })
	var b strings.Builder
	b.WriteString(confHeader)
	for _, h := range hosts {
		b.WriteString("\nHost " + h.Agent.Alias + "\n")
		b.WriteString("  HostName " + h.Host + "\n")
		b.WriteString("  User " + h.AgentUser() + "\n")
		b.WriteString("  Port " + strconv.Itoa(effectivePort(h.Port)) + "\n")
		b.WriteString("  IdentityFile " + quoteConfValue(h.Agent.KeyFile) + "\n")
		b.WriteString("  IdentitiesOnly yes\n")
		b.WriteString("  IdentityAgent none\n")
	}
	return []byte(b.String()), skipped
}

// HasAgents 判斷是否有任何已開通的機器。
func HasAgents(f *store.File) bool {
	for _, h := range f.Hosts {
		if h.Agent != nil {
			return true
		}
	}
	return false
}

// ensureSSHDir 建立 ~/.ssh（0700）；已存在時不改權限。
func ensureSSHDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("建立 %s 失敗：%w", dir, err)
	}
	return os.Chmod(dir, 0o700)
}

// SyncConf 依 hosts.json 重寫 sshm_agents.conf（原子寫入、保留 symlink）；內容相同不寫。
// 沒有任何已開通機器且檔案不存在時不建立。回傳是否寫入與被略過的機器。
func (p Paths) SyncConf(f *store.File) (bool, []string, error) {
	data, skipped := RenderConf(f)
	cur, err := os.ReadFile(p.Conf)
	switch {
	case err == nil && bytes.Equal(cur, data):
		return false, skipped, nil
	case errors.Is(err, fs.ErrNotExist):
		if !HasAgents(f) {
			return false, skipped, nil
		}
	case err != nil:
		return false, skipped, fmt.Errorf("讀取 %s 失敗：%w", p.Conf, err)
	}
	if err := ensureSSHDir(p.SSHDir); err != nil {
		return false, skipped, err
	}
	if err := store.WriteFileAtomic(p.Conf, data, 0o600); err != nil {
		return false, skipped, err
	}
	return true, skipped, nil
}

// IncludeStatus 是檢查 ~/.ssh/config 的結果。
type IncludeStatus struct {
	Found     bool // 已有指向 sshm_agents.conf 的 Include
	Misplaced bool // Include 位於 Host／Match 區塊之後（只在該區塊符合時才生效）
	Line      int  // Include 所在行號（Found 時）
}

// configLines 讀 ~/.ssh/config；不存在時回傳 nil。
func configLines(path string) ([]string, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("讀取 %s 失敗：%w", path, err)
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, true, sc.Err()
}

// keyword 拆出 ssh_config 一行的關鍵字（小寫）與參數；支援 `Key value` 與 `Key=value`。
func keyword(line string) (string, []string) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", nil
	}
	end := strings.IndexAny(s, " \t=")
	if end < 0 {
		return strings.ToLower(s), nil
	}
	key := strings.ToLower(s[:end])
	rest := strings.TrimLeft(s[end:], " \t")
	rest = strings.TrimPrefix(rest, "=")
	var args []string
	for _, a := range strings.Fields(rest) {
		args = append(args, strings.Trim(a, `"`))
	}
	return key, args
}

func (p Paths) includeMatches(arg string) bool {
	switch arg {
	case strings.Trim(p.IncludeArg, `"`), "~/.ssh/" + ConfName, p.Conf, ConfName:
		return true
	}
	return false
}

// CheckInclude 檢查 ~/.ssh/config 是否已 Include sshm_agents.conf，以及位置是否正確。
func (p Paths) CheckInclude() (IncludeStatus, error) {
	lines, _, err := configLines(p.Config)
	if err != nil {
		return IncludeStatus{}, err
	}
	inBlock := false
	for i, line := range lines {
		key, args := keyword(line)
		switch key {
		case "host", "match":
			inBlock = true
		case "include":
			for _, a := range args {
				if p.includeMatches(a) {
					return IncludeStatus{Found: true, Misplaced: inBlock, Line: i + 1}, nil
				}
			}
		}
	}
	return IncludeStatus{}, nil
}

// IncludeWarning 是 Include 位置不對時給使用者的說明。
func (p Paths) IncludeWarning(st IncludeStatus) string {
	return fmt.Sprintf("警告：%s 第 %d 行的「Include %s」位於 Host／Match 區塊之後，只有在該區塊符合時才會生效，"+
		"AI agent 的別名可能無法使用。請手動把這一行移到檔案最上方（任何 Host／Match 之前）；sshm 不會自動搬動你的設定。",
		p.Config, st.Line, p.IncludeArg)
}

// EnsureInclude 在 ~/.ssh/config 最上方加入 Include（不存在才加；檔案不存在就建立 0600），
// 寫入為原子操作並保留 symlink。回傳是否有寫入與檢查結果（Misplaced 時呼叫端要警告）。
func (p Paths) EnsureInclude() (bool, IncludeStatus, error) {
	st, err := p.CheckInclude()
	if err != nil || st.Found {
		return false, st, err
	}
	data, err := os.ReadFile(p.Config)
	perm := fs.FileMode(0o600)
	switch {
	case err == nil:
		if info, err := os.Stat(p.Config); err == nil {
			perm = info.Mode().Perm()
		}
	case errors.Is(err, fs.ErrNotExist):
		if err := ensureSSHDir(p.SSHDir); err != nil {
			return false, st, err
		}
	default:
		return false, st, fmt.Errorf("讀取 %s 失敗：%w", p.Config, err)
	}
	out := append([]byte("Include "+p.IncludeArg+"\n"), data...)
	if err := store.WriteFileAtomic(p.Config, out, perm); err != nil {
		return false, st, err
	}
	return true, IncludeStatus{Found: true, Line: 1}, nil
}

// ConfigDefinesHost 判斷使用者自己的 ~/.ssh/config 是否已有同名的 Host（不含萬用字元比對）；
// 有的話 sshm 的區塊會因 Include 在最上方而蓋過它，應拒絕這個別名。
func (p Paths) ConfigDefinesHost(alias string) (bool, error) {
	lines, _, err := configLines(p.Config)
	if err != nil {
		return false, err
	}
	for _, line := range lines {
		key, args := keyword(line)
		if key != "host" {
			continue
		}
		for _, a := range args {
			if strings.EqualFold(a, alias) {
				return true, nil
			}
		}
	}
	return false, nil
}
