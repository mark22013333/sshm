package store

import (
	"errors"
	"fmt"
	"strings"
)

// ValidateHost 檢查機器資料；user、host 以 - 開頭會被 ssh 當成選項，一律拒絕。
func ValidateHost(h Host) error {
	var missing []string
	if strings.TrimSpace(h.Name) == "" {
		missing = append(missing, "名稱")
	}
	if strings.TrimSpace(h.Host) == "" {
		missing = append(missing, "host")
	}
	if strings.TrimSpace(h.User) == "" {
		missing = append(missing, "user")
	}
	if len(missing) > 0 {
		return fmt.Errorf("必填欄位未填：%s", strings.Join(missing, "、"))
	}
	// 連線指令會打進 shell，換行、Tab 等控制字元會被行編輯器解讀
	fields := []struct{ label, value string }{
		{"名稱", h.Name}, {"群組", h.Group}, {"host", h.Host}, {"user", h.User},
		{"密碼", h.Password}, {"金鑰檔", h.IdentityFile}, {"額外 ssh 參數", h.ExtraArgs}, {"自訂指令", h.CustomCommand},
	}
	for _, fd := range fields {
		if hasControl(fd.value) {
			return fmt.Errorf("%s 不可包含換行或其他控制字元", fd.label)
		}
	}
	if strings.HasPrefix(h.User, "-") {
		return errors.New("user 不可以 - 開頭")
	}
	if strings.HasPrefix(h.Host, "-") {
		return errors.New("host 不可以 - 開頭")
	}
	// 0 代表未設定，連線時用 22
	if h.Port < 0 || h.Port > 65535 {
		return errors.New("port 必須是 1–65535 的數字")
	}
	switch h.Auth {
	case AuthPassword, AuthNone, "":
	case AuthKey:
		if strings.TrimSpace(h.IdentityFile) == "" {
			return errors.New("認證方式為 key 時必須填金鑰檔")
		}
	default:
		return fmt.Errorf("不支援的認證方式 %q", h.Auth)
	}
	return nil
}

// hasControl 判斷字串是否含 C0 控制字元（含換行、Tab）或 DEL。
func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
