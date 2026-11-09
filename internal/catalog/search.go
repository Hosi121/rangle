package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

type searchEntry struct {
	text string
	tags map[string]bool
}

func newSearchEntry(view clipView) searchEntry {
	entry := searchEntry{
		text: strings.ToLower(strings.Join([]string{view.Title, view.Sport, view.Description, strings.Join(view.Tags, " ")}, " ")),
		tags: make(map[string]bool, len(view.Tags)),
	}
	for _, tag := range view.Tags {
		entry.tags[strings.ToLower(tag)] = true
	}
	return entry
}

// Search uses the whole startup catalog, before pagination. Its cursor is bound
// to the normalized filter so it cannot silently skip results of another search.
func (h *handler) selectClips(query url.Values) (clipSelection, string, error) {
	q, tag := query.Get("q"), query.Get("tag")
	if len(q) > 256 || !utf8.ValidString(q) || len(strings.Fields(q)) > 8 ||
		(tag != "" && !validTag(tag)) {
		return clipSelection{}, "", fmt.Errorf("invalid search")
	}
	terms := strings.Fields(strings.ToLower(q))
	tag = strings.ToLower(tag)
	if len(terms) == 0 && tag == "" {
		return clipSelection{first: h.extra, second: h.clips}, h.snapshot, nil
	}
	sum := sha256.Sum256([]byte(h.snapshot + "\n" + strings.Join(terms, " ") + "\n" + tag))
	selected := make([]jsonDocument, 0)
	collect := func(search []searchEntry, clips []jsonDocument) {
		for i, entry := range search {
			if tag != "" && !entry.tags[tag] {
				continue
			}
			matches := true
			for _, term := range terms {
				if !strings.Contains(entry.text, term) {
					matches = false
					break
				}
			}
			if matches {
				selected = append(selected, clips[i])
			}
		}
	}
	collect(h.extraSearch, h.extra)
	collect(h.search, h.clips)
	return clipSelection{first: selected}, hex.EncodeToString(sum[:]), nil
}
