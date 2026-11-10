// Package lang holds the text predicates shared by the enrichment pipeline and
// the library audit, so both agree on what "still Japanese" means.
package lang

import "regexp"

// Kana matches hiragana/katakana letters. Japanese punctuation that also shows
// up in Chinese titles (・ ー ゛ ゜ 、。「」) is excluded, so a title like
// "续・出道" is not counted as Japanese.
var Kana = regexp.MustCompile(`[\x{3041}-\x{3096}\x{30A1}-\x{30FA}]`)

// Han matches CJK unified ideographs.
var Han = regexp.MustCompile(`[\x{4e00}-\x{9fff}]`)

// LooksJapanese reports whether s is Japanese text rather than a Chinese
// translation with a few leftover kana (typically an actress name). Text counts
// as Japanese when it has no Han at all, or when kana make up at least 20% of
// the characters.
func LooksJapanese(s string) bool {
	if !Kana.MatchString(s) {
		return false
	}
	if !Han.MatchString(s) {
		return true
	}
	runes := []rune(s)
	if len(runes) == 0 {
		return false
	}
	kana := len(Kana.FindAllString(s, -1))
	return float64(kana)/float64(len(runes)) >= 0.2
}
