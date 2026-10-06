// SPDX-License-Identifier: AGPL-3.0-only

package linernotes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

// Defaults for Wikipedia and Wikidata.
const (
	DefaultWikipediaURL = "https://en.wikipedia.org"
	DefaultWikidataURL  = "https://www.wikidata.org"
)

const (
	wikiTimeout = 10 * time.Second
	maxWikiBody = 1 << 20
	// maxBio caps the bio, cut at a sentence; Wikipedia's summaries are
	// usually the article's first paragraph.
	maxBio = 900
)

// errNoArticle means there's no article to read.
var errNoArticle = errors.New("wikipedia: no article")

// wikipedia reads article summaries, finding articles through Wikidata.
type wikipedia struct {
	base      string // e.g. https://en.wikipedia.org
	wikidata  string
	site      string // the Wikidata sitelink key, e.g. "enwiki"
	userAgent string
	http      *http.Client
}

// siteKey is Wikidata's name for a Wikipedia: "en.wikipedia.org" is "enwiki".
func siteKey(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return "enwiki"
	}
	if lang, ok := strings.CutSuffix(u.Hostname(), ".wikipedia.org"); ok && lang != "" && !strings.Contains(lang, ".") {
		return strings.ReplaceAll(lang, "-", "_") + "wiki"
	}
	return "enwiki"
}

// summary is an artist's article summary.
type summary struct {
	Text string
	URL  string
}

// artistSummary finds the article for a Wikidata item, or else the one
// at a Wikipedia URL MusicBrainz linked, and reads its summary.
func (w *wikipedia) artistSummary(ctx context.Context, wikidataID, wikipediaURL string) (summary, error) {
	title := ""
	if wikidataID != "" {
		t, err := w.sitelink(ctx, wikidataID)
		if err != nil && !errors.Is(err, errNoArticle) {
			return summary{}, err
		}
		title = t
	}
	if title == "" && wikipediaURL != "" {
		title = w.titleFromURL(wikipediaURL)
	}
	if title == "" {
		return summary{}, errNoArticle
	}
	return w.summary(ctx, title)
}

// sitelink is the title of the item's article on our Wikipedia.
func (w *wikipedia) sitelink(ctx context.Context, id string) (string, error) {
	q := url.Values{"action": {"wbgetentities"}, "ids": {id}, "props": {"sitelinks"}, "sitefilter": {w.site}, "format": {"json"}}
	var r struct {
		Entities map[string]struct {
			Sitelinks map[string]struct {
				Title string `json:"title"`
			} `json:"sitelinks"`
		} `json:"entities"`
	}
	if err := w.get(ctx, w.wikidata+"/w/api.php?"+q.Encode(), &r); err != nil {
		return "", err
	}
	if t := r.Entities[id].Sitelinks[w.site].Title; t != "" {
		return t, nil
	}
	return "", errNoArticle
}

// titleFromURL is the title of an article on our Wikipedia, from its URL.
func (w *wikipedia) titleFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	base, err := url.Parse(w.base)
	if err != nil || !strings.EqualFold(u.Hostname(), base.Hostname()) {
		return ""
	}
	t, ok := strings.CutPrefix(u.Path, "/wiki/")
	if !ok {
		return ""
	}
	return t
}

func (w *wikipedia) summary(ctx context.Context, title string) (summary, error) {
	var r struct {
		Type        string `json:"type"`
		Extract     string `json:"extract"`
		ContentURLs struct {
			Desktop struct {
				Page string `json:"page"`
			} `json:"desktop"`
		} `json:"content_urls"`
	}
	path := url.PathEscape(strings.ReplaceAll(title, " ", "_"))
	if err := w.get(ctx, w.base+"/api/rest_v1/page/summary/"+path, &r); err != nil {
		return summary{}, err
	}
	// A disambiguation page isn't about the artist.
	if r.Type == "disambiguation" || strings.TrimSpace(r.Extract) == "" {
		return summary{}, errNoArticle
	}
	return summary{Text: clip(strings.TrimSpace(r.Extract), maxBio), URL: r.ContentURLs.Desktop.Page}, nil
}

func (w *wikipedia) get(ctx context.Context, u string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, wikiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", w.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := w.http.Do(req)
	if err != nil {
		return fmt.Errorf("wikipedia: %w: %w", provider.ErrUnavailable, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return errNoArticle
	default:
		return fmt.Errorf("wikipedia: HTTP %d: %w", resp.StatusCode, provider.ErrUnavailable)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxWikiBody)).Decode(v); err != nil {
		return fmt.Errorf("wikipedia: decoding: %w: %w", provider.ErrUnavailable, err)
	}
	return nil
}

// clip shortens s to at most n bytes, at the end of a sentence if it can,
// else at a word with an ellipsis.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndex(cut, ". "); i > n/3 {
		return cut[:i+1]
	}
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, ",;:") + "…"
}
