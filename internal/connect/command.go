package connect

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/shlex"
	"github.com/mark22013333/sshm/internal/store"
)

// Plan 是一次連線要執行的東西。
type Plan struct {
	// Argv 供 syscall.Exec 原地執行；不含密碼。
	Argv []string
	// Env 是執行 Argv 時要額外加入的環境變數（密碼經此交給 expect，不放 argv）。
	Env []string
	// Text 是打進 shell 的一行文字（開頭有空白），供 Orca --command 使用；密碼以前置變數指定，不成為參數。
	Text string
}

// PasswordEnv 是把密碼交給 expect 登入腳本的環境變數名；腳本讀到後立刻 unset，不傳給 ssh。
const PasswordEnv = "SSHM_PASSWORD"

const defaultPort = 22

// SplitArgs 以 shell 規則拆分 extraArgs。
func SplitArgs(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	args, err := shlex.Split(s)
	if err != nil {
		return nil, fmt.Errorf("額外 ssh 參數無法解析：%w", err)
	}
	return args, nil
}

func port(h store.Host) int {
	if h.Port <= 0 {
		return defaultPort
	}
	return h.Port
}

// sshArgs 組出 key/none 認證的 ssh 參數（不含目的地以外的 tunnel 參數）。
func sshArgs(h store.Host, extra []string) ([]string, error) {
	argv := []string{"ssh", "-o", "StrictHostKeyChecking=accept-new", "-p", strconv.Itoa(port(h))}
	switch h.Auth {
	case store.AuthKey:
		if h.IdentityFile == "" {
			return nil, errors.New("認證方式為 key，但未設定金鑰檔")
		}
		argv = append(argv, "-i", h.IdentityFile)
	case store.AuthNone, "":
	default:
		return nil, fmt.Errorf("不支援的認證方式 %q", h.Auth)
	}
	argv = append(argv, extra...)
	// -- 讓目的地不會被當成選項
	return append(argv, "--", h.User+"@"+h.Host), nil
}

// validate 連線前重新驗證（hosts.json 可能被手改，例如帶進控制字元）。
func validate(h store.Host) error {
	if err := store.ValidateHost(h); err != nil {
		return fmt.Errorf("機器資料不合法：%w", err)
	}
	return nil
}

// build 依認證方式組 argv 與額外環境變數；extra 會接在 ssh 參數中（password 時交給 expect 腳本轉傳）。
// 密碼只放環境變數：argv 對同機其他使用者可見（ps -ww），環境變數只有同一使用者與 root 讀得到。
func build(h store.Host, scriptPath string, extra []string) ([]string, []string, error) {
	if err := validate(h); err != nil {
		return nil, nil, err
	}
	if h.Auth == store.AuthPassword {
		if scriptPath == "" {
			return nil, nil, errors.New("找不到 expect 登入腳本")
		}
		argv := []string{scriptPath, strconv.Itoa(port(h)), h.User, h.Host}
		return append(argv, extra...), []string{PasswordEnv + "=" + h.Password}, nil
	}
	argv, err := sshArgs(h, extra)
	return argv, nil, err
}

// planFor 組出 Plan；Text 以「VAR=值 指令」前置環境變數，shell 不會把它變成參數。
func planFor(argv, env []string) Plan {
	text := ShellText(argv)
	if len(env) > 0 {
		prefix := make([]string, len(env))
		for i, e := range env {
			k, v, _ := strings.Cut(e, "=")
			prefix[i] = k + "=" + Quote(v)
		}
		text = " " + strings.Join(prefix, " ") + text
	}
	return Plan{Argv: argv, Env: env, Text: text}
}

// HostPlan 組出連線到機器的指令；customCommand 非空時整個取代。
func HostPlan(h store.Host, scriptPath string) (Plan, error) {
	if err := validate(h); err != nil {
		return Plan{}, err
	}
	if c := strings.TrimSpace(h.CustomCommand); c != "" {
		return customPlan(c), nil
	}
	extra, err := SplitArgs(h.ExtraArgs)
	if err != nil {
		return Plan{}, err
	}
	argv, env, err := build(h, scriptPath, extra)
	if err != nil {
		return Plan{}, err
	}
	return planFor(argv, env), nil
}

// customPlan 自訂指令是 shell 文字：Orca 直接打進 shell，原地執行則交給使用者的 shell -c。
func customPlan(cmd string) Plan {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	return Plan{Argv: []string{sh, "-c", cmd}, Text: " " + cmd}
}

// TunnelArgs 組出 tunnel 專用參數與每條轉送規則。
func TunnelArgs(rules []store.Rule) ([]string, error) {
	if len(rules) == 0 {
		return nil, errors.New("tunnel 沒有任何規則")
	}
	// 不共用既有的 ControlMaster 連線，tunnel 的生死只看這條 ssh
	args := []string{"-N", "-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3", "-o", "ExitOnForwardFailure=yes",
		"-o", "ControlPath=none", "-o", "ControlMaster=no"}
	for i, r := range rules {
		spec, err := ruleSpec(r)
		if err != nil {
			return nil, fmt.Errorf("第 %d 條規則：%w", i+1, err)
		}
		args = append(args, "-"+strings.ToUpper(r.Type), spec)
	}
	return args, nil
}

// bracketIPv6 含 : 的位址（IPv6）要加中括號，否則 ssh 會把 : 當成欄位分隔。
func bracketIPv6(addr string) string {
	if strings.Contains(addr, ":") && !strings.HasPrefix(addr, "[") {
		return "[" + addr + "]"
	}
	return addr
}

func ruleSpec(r store.Rule) (string, error) {
	if r.BindPort <= 0 {
		return "", errors.New("缺少 bindPort")
	}
	prefix := ""
	if r.BindAddress != "" {
		prefix = bracketIPv6(r.BindAddress) + ":"
	}
	switch strings.ToUpper(r.Type) {
	case "D":
		return prefix + strconv.Itoa(r.BindPort), nil
	case "L", "R":
		if r.TargetHost == "" || r.TargetPort <= 0 {
			return "", errors.New("L/R 規則需要 targetHost 與 targetPort")
		}
		return fmt.Sprintf("%s%d:%s:%d", prefix, r.BindPort, bracketIPv6(r.TargetHost), r.TargetPort), nil
	default:
		return "", fmt.Errorf("不支援的規則類型 %q", r.Type)
	}
}

// TunnelPlan 組出 tunnel 連線指令；tunnel 一律用組出的指令，不套用 customCommand。
func TunnelPlan(h store.Host, t store.Tunnel, scriptPath string) (Plan, error) {
	// 與匯入、表單存檔相同的驗證：type、port、target、hostId
	if err := store.ValidateTunnel(t); err != nil {
		return Plan{}, fmt.Errorf("tunnel 設定不合法：%w", err)
	}
	if t.HostID != h.ID {
		return Plan{}, errors.New("tunnel 的 hostId 與機器不符")
	}
	if err := validate(h); err != nil {
		return Plan{}, err
	}
	extra, err := SplitArgs(h.ExtraArgs)
	if err != nil {
		return Plan{}, err
	}
	targs, err := TunnelArgs(t.Rules)
	if err != nil {
		return Plan{}, err
	}
	argv, env, err := build(h, scriptPath, append(targs, extra...))
	if err != nil {
		return Plan{}, err
	}
	p := planFor(argv, env)
	p.Text += tunnelEpilogue()
	return p, nil
}

// TunnelCloseDelay 是 tunnel 結束後保留分頁的秒數；測試可縮短。
var TunnelCloseDelay = 3

// tunnelEpilogue 接在 Orca 分頁的指令後：ssh 結束時印出結束碼、等幾秒讓使用者看錯誤，再關閉分頁，
// 分頁消失後 sshm 的燈號就會回到「已停止」。
func tunnelEpilogue() string {
	n := strconv.Itoa(TunnelCloseDelay)
	return `; sshm_rc=$?; printf '\n%s\n' "Tunnel 已結束（代碼 ${sshm_rc}），` + n + ` 秒後關閉此分頁"; sleep ` + n + `; exit "$sshm_rc"`
}

// PasswordLoginArgv 組出以 expect 腳本自動輸入密碼的 argv 與要加入的環境變數（含密碼），
// extra 接在 ssh 參數中（供 sshm agent 安裝公鑰）。
func PasswordLoginArgv(h store.Host, scriptPath string, extra []string) ([]string, []string, error) {
	if h.Auth != store.AuthPassword {
		return nil, nil, errors.New("只支援密碼認證的機器")
	}
	return build(h, scriptPath, extra)
}
