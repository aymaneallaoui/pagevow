package verify

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

const (
	guardHrefIndex  = 12
	guardScopeIndex = 13
)

// HNStory checks that the final page is the story at the given rank on the initial front page: its comment thread or its own link.
func HNStory(final page.State, initial *page.State, args HNStoryArgs) (Result, error) {
	if initial == nil {
		return Result{}, errors.New("hn_story needs the initial page")
	}
	prefix := strconv.Itoa(args.Rank) + ".\t"
	var hrefs []string
	for _, action := range initial.Actions {
		if action.Kind != page.KindClick {
			continue
		}
		href, scope, ok, err := guardTexts(initial.Guards[strconv.Itoa(action.Node)], action.Node)
		if err != nil {
			return Result{}, err
		}
		if ok && strings.HasPrefix(scope, prefix) {
			hrefs = append(hrefs, href)
		}
	}
	story, title := "", ""
	foundStory, foundTitle := false, false
	for _, href := range hrefs {
		if !foundStory && strings.HasPrefix(href, "vote?id=") {
			afterID := strings.Split(href, "id=")[1]
			story, _, _ = strings.Cut(afterID, "&")
			foundStory = true
		}
		if !foundTitle && !hasAnyPrefix(href, "vote?", "from?", "hide?") {
			title, foundTitle = href, true
		}
	}
	var expected *string
	switch {
	case args.Comments && story != "":
		url := "https://news.ycombinator.com/item?id=" + story
		expected = &url
	case !args.Comments && title != "":
		url := joinURL(initial.URL, title)
		expected = &url
	}
	res := newResult()
	res.set("story_found", expected != nil)
	res.set("url", expected != nil && sameURL(final.URL, *expected))
	res.finish()
	res.Expected = expected
	return res, nil
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// guardTexts returns the href and scope text of a snapshot guard; ok is false when the element has no guard.
func guardTexts(raw json.RawMessage, node int) (href, scope string, ok bool, err error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", false, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", "", false, fmt.Errorf("guard of node %d is not a list: %w", node, err)
	}
	if len(entries) == 0 {
		return "", "", false, nil
	}
	if len(entries) <= guardScopeIndex {
		return "", "", false, fmt.Errorf("guard of node %d has %d entries, want at least %d", node, len(entries), guardScopeIndex+1)
	}
	for i, target := range map[int]*string{guardHrefIndex: &href, guardScopeIndex: &scope} {
		var text *string
		if err := json.Unmarshal(entries[i], &text); err != nil {
			return "", "", false, fmt.Errorf("guard entry %d of node %d is not text: %w", i, node, err)
		}
		if text != nil {
			*target = *text
		}
	}
	return href, scope, true, nil
}
