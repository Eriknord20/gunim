package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"  // pictures a page may show
	_ "image/jpeg" // pictures a page may show
	_ "image/png"  // pictures a page may show
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // pictures a page may show
	"golang.org/x/net/html"
)

// Limits on what a preview fetches: how long it takes, how much of the page and of its picture it reads, and how
// large a thumbnail it keeps.
const (
	previewWait  = 8 * time.Second
	pageLimit    = 1 << 20
	pictureLimit = 5 << 20
	thumbSide    = 160
)

// page is what a page says of itself: its title, description and site, and its picture, shrunk to a thumbnail.
type page struct {
	url, site, title, description string
	picture                       image.Image
}

// linkPattern finds web addresses in a message's text.
var linkPattern = regexp.MustCompile(`https?://[^\s<>()\[\]]+`)

// firstLink returns the first web address in text, without the punctuation that ends a sentence, or "".
func firstLink(text string) string {
	return strings.TrimRight(linkPattern.FindString(text), ".,;:!?'\"")
}

// fetchPage reads the page at link and returns what it says of itself: its Open Graph title, description, site name
// and picture, or else its title and description. It fails for a page that says nothing of itself.
func fetchPage(ctx context.Context, client *http.Client, link string) (page, error) {
	ctx, cancel := context.WithTimeout(ctx, previewWait)
	defer cancel()
	base, err := url.Parse(link)
	if err != nil {
		return page{}, err
	}
	body, typ, err := get(ctx, client, link, pageLimit)
	if err != nil {
		return page{}, err
	}
	if mt, _, _ := mime.ParseMediaType(typ); mt != "text/html" {
		return page{}, fmt.Errorf("%s is %s, not a page", link, typ)
	}
	p, pictureURL := readHead(string(body))
	p.url = link
	if p.site == "" {
		p.site = strings.TrimPrefix(base.Hostname(), "www.")
	}
	if p.title == "" {
		return page{}, fmt.Errorf("%s has no title", link)
	}
	if pictureURL != "" {
		if ref, err := base.Parse(pictureURL); err == nil {
			// A page without its picture still makes a card.
			p.picture, _ = fetchPicture(ctx, client, ref.String())
		}
	}
	return p, nil
}

// get reads at most limit bytes from link, and returns them with their content type.
func get(ctx context.Context, client *http.Client, link string, limit int64) (body []byte, kind string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, http.NoBody)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "gunim chat example link preview")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%s answered %s", link, resp.Status)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// fetchPicture reads the picture at link and shrinks it to a thumbnail.
func fetchPicture(ctx context.Context, client *http.Client, link string) (image.Image, error) {
	body, _, err := get(ctx, client, link, pictureLimit)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	scale := min(1, float64(thumbSide)/float64(max(b.Dx(), b.Dy(), 1)))
	w, h := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	thumb := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.ApproxBiLinear.Scale(thumb, thumb.Bounds(), img, b, xdraw.Src, nil)
	return thumb, nil
}

// readHead reads what a page's head says of the page, and where its picture is.
func readHead(doc string) (p page, pictureURL string) {
	z := html.NewTokenizer(strings.NewReader(doc))
	var title strings.Builder
	inTitle := false
	var desc string
	for {
		switch z.Next() {
		case html.ErrorToken:
			return finish(p, title.String(), desc), pictureURL
		case html.TextToken:
			if inTitle {
				title.Write(z.Text())
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "title":
				inTitle = false
			case "head":
				return finish(p, title.String(), desc), pictureURL
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, more := z.TagName()
			switch string(name) {
			case "title":
				inTitle = true
			case "body":
				return finish(p, title.String(), desc), pictureURL
			case "meta":
				var key, content string
				for more {
					var k, v []byte
					k, v, more = z.TagAttr()
					switch string(k) {
					case "property", "name":
						key = strings.ToLower(string(v))
					case "content":
						content = strings.TrimSpace(string(v))
					}
				}
				switch key {
				case "og:title":
					p.title = content
				case "og:description":
					p.description = content
				case "og:site_name":
					p.site = content
				case "og:image":
					pictureURL = content
				case "twitter:image":
					if pictureURL == "" {
						pictureURL = content
					}
				case "description":
					desc = content
				}
			}
		default:
			// A comment or a doctype says nothing about the page.
		}
	}
}

// finish fills what the Open Graph tags left out from the page's title and description.
func finish(p page, title, desc string) page {
	if p.title == "" {
		p.title = strings.Join(strings.Fields(title), " ")
	}
	if p.description == "" {
		p.description = desc
	}
	return p
}
