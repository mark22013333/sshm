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
	// Argv 供 syscall.Exec 原地執行。
	Argv []string
	// Text 是打進 shell 的一行文字（開頭有空白），供 Orca --command 使用。
	Text string
}

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

// build 依認證方式組 argv；extra 會接在 ssh 參數中（password 時交給 expect 腳本轉傳）。
func build(h store.Host, scriptPath string, extra []string) ([]string, error) {
	if err := validate(h); err != nil {
		return nil, err
	}
	if h.Auth == store.AuthPassword {
		if scriptPath == "" {
			return nil, errors.New("找不到 expect 登入腳本")
		}
		argv := []string{scriptPath, strconv.Itoa(port(h)), h.User, h.Host, h.Password}
		return append(argv, extra...), nil
	}
	return sshArgs(h, extra)
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
	argv, err := build(h, scriptPath, extra)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Argv: argv, Text: ShellText(argv)}, nil
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
	args := []string{"-N", "-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3", "-o", "ExitOnForwardFailure=yes"}
	for i, r := range rules {
		spec, err := ruleSpec(r)
		if err != nil {
			return nil, fmt.Errorf("第 %d 條規則：%w", i+1, err)
		}
		args = append(args, "-"+strings.ToUpper(r.Type), spec)
	}
	return args, nil
}

func ruleSpec(r store.Rule) (string, error) {
	if r.BindPort <= 0 {
		return "", errors.New("缺少 bindPort")
	}
	prefix := ""
	if r.BindAddress != "" {
		prefix = r.BindAddress + ":"
	}
	switch strings.ToUpper(r.Type) {
	case "D":
		return prefix + strconv.Itoa(r.BindPort), nil
	case "L", "R":
		if r.TargetHost == "" || r.TargetPort <= 0 {
			return "", errors.New("L/R 規則需要 targetHost 與 targetPort")
		}
		return fmt.Sprintf("%s%d:%s:%d", prefix, r.BindPort, r.TargetHost, r.TargetPort), nil
	default:
		return "", fmt.Errorf("不支援的規則類型 %q", r.Type)
	}
}

// TunnelPlan 組出 tunnel 連線指令；tunnel 一律用組出的指令，不套用 customCommand。
func TunnelPlan(h store.Host, t store.Tunnel, scriptPath string) (Plan, error) {
	extra, err := SplitArgs(h.ExtraArgs)
	if err != nil {
		return Plan{}, err
	}
	targs, err := TunnelArgs(t.Rules)
	if err != nil {
		return Plan{}, err
	}
	argv, err := build(h, scriptPath, append(targs, extra...))
	if err != nil {
		return Plan{}, err
	}
	return Plan{Argv: argv, Text: ShellText(argv)}, nil
}
