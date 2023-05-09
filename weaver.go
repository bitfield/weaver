package weaver

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/antchfx/htmlquery"
	"golang.org/x/time/rate"
)

var visited = map[string]bool{}

var limiter = rate.NewLimiter(5, 1)

var (
	base    *url.URL
	verbose *bool
)

func Main() int {
	verbose = flag.Bool("v", false, "verbose output")
	flag.Parse()
	tmp, err := url.Parse(flag.Args()[0])
	base = tmp
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	Crawl(ctx, base.String(), "none")
	return 0
}

func Crawl(ctx context.Context, page, referrer string) {
	limiter.Wait(ctx)
	resp, err := http.Get(page)
	if err != nil {
		fmt.Printf("[%s] %s (referrer %s)\n", err, page, referrer)
		return
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("[%s] %s (referrer: %s)\n", resp.Status, page, referrer)
		return
	}
	if *verbose {
		fmt.Printf("[%s] %s (referrer: %s)\n", resp.Status, page, referrer)
	}
	defer resp.Body.Close()
	if isOffsite(page) {
		return
	}
	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		fmt.Printf("[%s] %s (referrer: %s)\n", err, page, referrer)
	}
	list := htmlquery.Find(doc, "//a/@href")
	for _, n := range list {
		link := htmlquery.SelectAttr(n, "href")
		u, err := url.Parse(link)
		if err != nil {
			fmt.Printf("[%s] %s (referrer: %s)\n", err, link, referrer)
			return
		}
		link = base.ResolveReference(u).String()
		if !visited[link] {
			visited[link] = true
			Crawl(ctx, link, page)
		} else if *verbose {
			fmt.Printf("[skip] %s (referrer: %s)\n", link, referrer)
		}
	}
}

func isOffsite(link string) bool {
	return !strings.HasPrefix(link, base.String())
}

func canonicalize(link string) string {
	if !strings.HasPrefix(link, "http") {
		return base.String() + "/" + link
	}
	return link
}
