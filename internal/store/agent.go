package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// AgentAccess 記錄一台機器開通給 AI agent 的狀態：ssh 別名、登入帳號與專用金鑰。
type AgentAccess struct {
	Alias string `json:"alias"`
	// User 空字串代表沿用機器的 user（機器改 user 時別名跟著改）。
	User    string `json:"user,omitempty"`
	KeyFile string `json:"keyFile"`
	// Installed 是公鑰實際裝過的遠端位置；機器改了 host／port／user 後，停用時要連舊位置一起清除。
	Installed []AgentInstall `json:"installed,omitempty"`
	Extra     Extra          `json:"-"`
}

// AgentInstall 是一次開通把公鑰裝到的遠端位置。
type AgentInstall struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Fingerprint string `json:"fingerprint"`
}

func (a *AgentAccess) UnmarshalJSON(b []byte) error {
	var v agentAlias
	extra, err := decodeWithExtra(b, &v)
	if err != nil {
		return err
	}
	*a = AgentAccess(v)
	a.Extra = extra
	return nil
}

func (a AgentAccess) MarshalJSON() ([]byte, error) { return encodeWithExtra(agentAlias(a), a.Extra) }

// AgentAliasMaxLen 是別名長度上限。
const AgentAliasMaxLen = 64

// ValidAgentAlias 檢查別名是否能安全寫成 ssh 的 Host 名稱：小寫英數開頭，只含小寫英數與 . _ -。
// ssh 會把命令列上的主機名稱轉小寫再比對 Host，因此不收大寫。
func ValidAgentAlias(s string) bool {
	if s == "" || len(s) > AgentAliasMaxLen {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// ValidateAgent 檢查開通資料；內容會寫進 ~/.ssh 的設定檔，必須擋掉換行、引號等能改變設定語意的字元。
func ValidateAgent(a AgentAccess) error {
	if hasControl(a.Alias) || hasControl(a.User) || hasControl(a.KeyFile) {
		return errors.New("AI agent 開通資料不可包含換行或其他控制字元")
	}
	if !ValidAgentAlias(a.Alias) {
		return fmt.Errorf("AI agent 別名 %q 不合法：只能用小寫英數與 . _ -，且以英數開頭、最長 %d 字", a.Alias, AgentAliasMaxLen)
	}
	if a.KeyFile == "" || !filepath.IsAbs(a.KeyFile) || strings.ContainsAny(a.KeyFile, `"\`) {
		return errors.New("AI agent 金鑰檔必須是絕對路徑，且不可包含雙引號或反斜線")
	}
	if a.User != "" && (strings.HasPrefix(a.User, "-") || strings.ContainsAny(a.User, "@: \t\"")) {
		return errors.New("AI agent 登入帳號不可以 - 開頭，也不可包含 @、:、引號或空白")
	}
	for _, in := range a.Installed {
		if hasControl(in.Host) || hasControl(in.User) || hasControl(in.Fingerprint) ||
			strings.HasPrefix(in.Host, "-") || strings.HasPrefix(in.User, "-") || in.Port < 0 || in.Port > 65535 {
			return errors.New("AI agent 的公鑰安裝位置紀錄不合法")
		}
	}
	return nil
}

// AgentUser 回傳 AI agent 實際登入的帳號；未開通時為空字串。
func (h Host) AgentUser() string {
	if h.Agent == nil {
		return ""
	}
	if h.Agent.User != "" {
		return h.Agent.User
	}
	return h.User
}

// SetAgent 只改一台機器的開通狀態（nil 為停用），在 Update 鎖內使用以免蓋掉其他欄位。
func (f *File) SetAgent(id string, a *AgentAccess) error {
	i := f.HostIndex(id)
	if i < 0 {
		return ErrHostGone
	}
	if a != nil {
		if err := ValidateAgent(*a); err != nil {
			return err
		}
		cp := *a
		a = &cp
	}
	f.Hosts[i].Agent = a
	return nil
}
