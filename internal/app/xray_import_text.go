package app

import (
	"fmt"
	"strings"
)

// xrayText is one message of the conversion log, kept as an English format and
// its arguments until it is written, so the log comes out in the panel language.
// Arguments may be other messages (a skip reason inside "skipped — %s"), lists
// of messages, or server errors, which are translated the same way.
type xrayText struct {
	format string
	args   []any
}

func xt(format string, args ...any) xrayText { return xrayText{format: format, args: args} }

// set reports whether the message is not empty (an empty skip reason means "not skipped").
func (t xrayText) set() bool { return t.format != "" }

// xrayList is written as its messages separated by commas.
type xrayList []xrayText

// xrayTextLanguages is the order of the translations in xrayTextCatalog.
var xrayTextLanguages = map[string]int{"zh-CN": 0, "zh-TW": 1, "ja": 2, "ru": 3, "fa": 4, "vi": 5, "es": 6, "tr": 7, "uk": 8, "pt-BR": 9}

func (t xrayText) render(language string) string {
	if !t.set() {
		return ""
	}
	format := t.format
	if index, ok := xrayTextLanguages[language]; ok {
		if translations, ok := xrayTextCatalog[t.format]; ok && translations[index] != "" {
			format = translations[index]
		}
	}
	args := make([]any, len(t.args))
	for i, arg := range t.args {
		switch typed := arg.(type) {
		case xrayText:
			args[i] = typed.render(language)
		case xrayList:
			parts := make([]string, len(typed))
			for j, item := range typed {
				parts[j] = item.render(language)
			}
			separator := ", "
			if language == "zh-CN" || language == "zh-TW" || language == "ja" {
				separator = "、"
			}
			args[i] = strings.Join(parts, separator)
		case error:
			// Server messages are written in Chinese; English is the log's source language.
			args[i] = translateMessage(typed.Error(), valueOr(language, "en"))
		default:
			args[i] = arg
		}
	}
	return fmt.Sprintf(format, args...)
}
