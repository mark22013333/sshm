package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// TunnelTitle 是 tunnel 專用分頁的標題。
func TunnelTitle(name string) string { return "⇄ " + name }

// TunnelCreateArgv 與 CreateArgv 相同但不搶焦點，讓使用者留在 sshm。
func (o OrcaEnv) TunnelCreateArgv(title, text string) []string {
	return []string{
		filepath.Join(o.BinDir, "orca"), "terminal", "create",
		"--worktree", "active",
		"--title=" + title,
		"--command=" + text,
		"--json",
	}
}

// OpenTunnelTab 開 tunnel 分頁並回傳 handle；拿不到 handle 就無法追蹤狀態，視為錯誤。
func (o OrcaEnv) OpenTunnelTab(run Runner, title, text string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	h, err := openWith(ctx, run, o.TunnelCreateArgv(title, text))
	if err != nil {
		return "", err
	}
	if h == "" {
		return "", errors.New("Orca 沒有回傳 terminal handle，分頁可能已開啟但無法追蹤狀態")
	}
	return h, nil
}

// ListArgv 組 `orca terminal list` 的 argv（拉高上限，避免結果被截斷）。
func (o OrcaEnv) ListArgv() []string {
	return []string{filepath.Join(o.BinDir, "orca"), "terminal", "list", "--limit=1000", "--json"}
}

// TerminalSet 是 terminal list 的結果。
type TerminalSet struct {
	// Handles：清單中的 handle；值為 false 表示該 PTY 正在重連（存在但狀態無法確認）。
	Handles map[string]bool
	// Complete 依 Orca 自己的規則判斷清單是否涵蓋所有主機；false 時「不在清單中」不代表已停止。
	Complete bool
}

type listHostScope struct {
	HostIDs        []string `json:"hostIds"`
	OmittedHostIDs []string `json:"omittedHostIds"`
}

// executionHostKind 對應 Orca 的 parseExecutionHostId：local、ssh:<id>、runtime:<id>；無法解析回空字串。
func executionHostKind(id string) string {
	id = strings.TrimSpace(id)
	if id == "local" {
		return "local"
	}
	for _, kind := range []string{"ssh", "runtime"} {
		enc, ok := strings.CutPrefix(id, kind+":")
		if !ok || enc == "" || strings.Contains(enc, "|") {
			continue
		}
		if dec, err := url.PathUnescape(enc); err == nil && dec != "" {
			return kind
		}
	}
	return ""
}

// hostScopeComplete 移植自 Orca 的 hostScopeCensusIsComplete（src/shared/runtime-listing-host-scope.ts）：
// 沒有 hostScope、沒有任何可解析的 hostIds、或 omittedHostIds 含非 runtime: 的主機，都不算完整。
func hostScopeComplete(scope *listHostScope) bool {
	if scope == nil {
		return false
	}
	legible := false
	for _, id := range scope.HostIDs {
		if executionHostKind(id) != "" {
			legible = true
			break
		}
	}
	if !legible {
		return false
	}
	for _, id := range scope.OmittedHostIDs {
		if executionHostKind(id) != "runtime" {
			return false
		}
	}
	return true
}

// ListTerminals 查詢目前存在的 terminal handle。
func (o OrcaEnv) ListTerminals(run Runner) (TerminalSet, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, stderr, err := run(ctx, o.ListArgv())
	var resp struct {
		OK     *bool `json:"ok"`
		Result struct {
			Terminals []struct {
				Handle    string `json:"handle"`
				Connected *bool  `json:"connected"`
			} `json:"terminals"`
			Truncated bool           `json:"truncated"`
			HostScope *listHostScope `json:"hostScope"`
		} `json:"result"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	perr := json.Unmarshal(bytes.TrimSpace(out), &resp)
	switch {
	case err != nil:
		msg := strings.TrimSpace(resp.Error.Message)
		if msg == "" {
			msg = strings.TrimSpace(string(stderr))
		}
		return TerminalSet{}, fmt.Errorf("orca terminal list 失敗：%v %s", err, msg)
	case perr != nil:
		return TerminalSet{}, fmt.Errorf("orca terminal list 輸出無法解析：%v", perr)
	case resp.OK != nil && !*resp.OK:
		return TerminalSet{}, errors.New("orca terminal list 失敗：" + resp.Error.Message)
	}
	set := TerminalSet{Handles: map[string]bool{}, Complete: !resp.Result.Truncated && hostScopeComplete(resp.Result.HostScope)}
	for _, t := range resp.Result.Terminals {
		connected := t.Connected == nil || *t.Connected
		if !connected {
			// Orca 不列出重連中的 PTY（runtime-terminal-list.ts），此時缺席不代表已停止
			set.Complete = false
		}
		if t.Handle != "" {
			set.Handles[t.Handle] = connected
		}
	}
	return set, nil
}

// CloseArgv 組 `orca terminal close --terminal <handle>` 的 argv。
func (o OrcaEnv) CloseArgv(handle string) []string {
	return []string{filepath.Join(o.BinDir, "orca"), "terminal", "close", "--terminal=" + handle, "--json"}
}

// CloseTerminal 關閉指定 terminal。
func (o OrcaEnv) CloseTerminal(run Runner, handle string) error {
	return runTerminalAction(run, o.CloseArgv(handle), "orca terminal close")
}

// SwitchArgv 組 `orca terminal switch --terminal <handle>` 的 argv。
func (o OrcaEnv) SwitchArgv(handle string) []string {
	return []string{filepath.Join(o.BinDir, "orca"), "terminal", "switch", "--terminal=" + handle, "--json"}
}

// SwitchTerminal 讓 Orca 切到指定 terminal 所在的分頁。
func (o OrcaEnv) SwitchTerminal(run Runner, handle string) error {
	return runTerminalAction(run, o.SwitchArgv(handle), "orca terminal switch")
}

// runTerminalAction 執行只需要成敗的 orca 指令；JSON 只從 stdout 解析，失敗訊息優先取 Orca 的 error.message。
func runTerminalAction(run Runner, argv []string, label string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, stderr, err := run(ctx, argv)
	var resp struct {
		OK    *bool `json:"ok"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(bytes.TrimSpace(out), &resp)
	if err != nil || (resp.OK != nil && !*resp.OK) {
		msg := strings.TrimSpace(resp.Error.Message)
		if msg == "" {
			msg = strings.TrimSpace(string(stderr))
		}
		if msg == "" && err != nil {
			msg = err.Error()
		}
		return errors.New(label + " 失敗：" + msg)
	}
	return nil
}
