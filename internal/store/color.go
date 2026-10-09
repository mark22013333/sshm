package store

import "strings"

// Colors 是固定七色色盤，順序即表單中的選擇順序。
var Colors = []string{"red", "orange", "yellow", "green", "blue", "purple", "white"}

var colorEmoji = map[string]string{
	"red":    "🔴",
	"orange": "🟠",
	"yellow": "🟡",
	"green":  "🟢",
	"blue":   "🔵",
	"purple": "🟣",
	"white":  "⚪",
}

// NormalizeColor 回傳色盤中的標準名稱；空字串或不認得的值回傳空字串。
func NormalizeColor(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	if _, ok := colorEmoji[c]; ok {
		return c
	}
	return ""
}

// ColorEmoji 回傳顏色對應的 emoji；無效顏色回傳空字串。
func ColorEmoji(c string) string { return colorEmoji[NormalizeColor(c)] }

// GroupColor 回傳群組的顏色（找不到群組時為空字串）。
func (f *File) GroupColor(name string) string {
	for _, g := range f.Groups {
		if g.Name == name {
			return NormalizeColor(g.Color)
		}
	}
	return ""
}

// EffectiveColor 機器自身顏色優先，否則用群組色，都沒有回傳空字串。
func (f *File) EffectiveColor(h Host) string {
	if c := NormalizeColor(h.Color); c != "" {
		return c
	}
	return f.GroupColor(h.Group)
}
