//go:build !unix

package store

// lockFile 在非 unix 平台尚未實作，只回傳空的解鎖函式。
func lockFile(string) (func(), error) { return func() {}, nil }
