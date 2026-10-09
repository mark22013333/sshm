package tui

import "github.com/charmbracelet/lipgloss"

// 色盤名稱對應終端機 256 色。
var paletteANSI = map[string]lipgloss.Color{
	"red":    "196",
	"orange": "208",
	"yellow": "220",
	"green":  "40",
	"blue":   "33",
	"purple": "135",
	"white":  "255",
}

var (
	styleTabActive   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("39")).Padding(0, 1)
	styleTabInactive = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
	styleGroup       = lipgloss.NewStyle().Bold(true)
	styleSelected    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	styleDim         = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleHelp        = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleError       = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleInfo        = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	styleWarn        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	styleLabel       = lipgloss.NewStyle().Width(16)
	styleLabelFocus  = lipgloss.NewStyle().Width(16).Bold(true).Foreground(lipgloss.Color("39"))
)

// colorBar 回傳左側色條；沒有顏色時以空白佔位維持對齊。
func colorBar(color string) string {
	c, ok := paletteANSI[color]
	if !ok {
		return " "
	}
	return lipgloss.NewStyle().Foreground(c).Render("▌")
}
