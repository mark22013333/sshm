// Package assets 內嵌執行期需要的檔案。
package assets

import _ "embed"

// LoginScript 是 sshm-login.exp 的內容。
//
//go:embed sshm-login.exp
var LoginScript []byte
