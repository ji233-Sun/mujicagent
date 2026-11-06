package view

import (
	"embed"

	"github.com/egoist/mygo/ui"
)

// Original Lucide SVGs and their license are bundled with the application.
//
//go:embed assets/lucide
var lucideAssets embed.FS

func lucideIcon(name string) *ui.SVG {
	data, err := lucideAssets.ReadFile("assets/lucide/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(data)
}

var (
	plus            = lucideIcon("plus")
	chat            = lucideIcon("message-square")
	settle          = lucideIcon("archive-arrow-down")
	restore         = lucideIcon("archive-arrow-up")
	folder          = lucideIcon("folder")
	folderPlus      = lucideIcon("folder-plus")
	gear            = lucideIcon("settings")
	downloadIcon    = lucideIcon("download")
	spark           = lucideIcon("sparkles")
	chevron         = lucideIcon("chevron-right")
	arrow           = lucideIcon("arrow-up")
	stopIcon        = lucideIcon("square")
	closeIcon       = lucideIcon("x")
	checkIcon       = lucideIcon("check")
	agentIcon       = lucideIcon("bot")
	appearanceIcon  = lucideIcon("palette")
	terminalIcon    = lucideIcon("terminal")
	fileEditIcon    = lucideIcon("file-pen-line")
	thinkingIcon    = lucideIcon("brain")
	searchIcon      = lucideIcon("search")
	planIcon        = lucideIcon("list-checks")
	activityIcon    = lucideIcon("layers")
	shieldIcon      = lucideIcon("shield")
	shieldCheckIcon = lucideIcon("shield-check")
	ruleIcon        = lucideIcon("key-round")
	failureIcon     = lucideIcon("circle-x")
	fastIcon        = lucideIcon("zap")
	ultrafastIcon   = lucideIcon("gauge")
)
