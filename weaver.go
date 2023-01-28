package weaver

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/antchfx/htmlquery"
)

var visited = map[string]bool{}

// var debug = io.Discard

var debug = os.Stderr

var base *url.URL

func Main() int {
	tmp, err := url.Parse(os.Args[1])
	base = tmp
	if err != nil {
		log.Fatal(err)
	}
	Crawl(os.Args[1])
	return 0
}

func Crawl(link string) {
	resp, err := http.Get(link)
	if err != nil {
		fmt.Println(link, err)
		return
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Println(link, resp.Status)
		return
	}
	fmt.Fprintln(debug, link, resp.Status)
	defer resp.Body.Close()
	if isOffsite(link, os.Args[1]) {
		fmt.Fprintln(debug, link, "offsite")
		return
	}
	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		fmt.Fprintln(debug, link, err)
	}
	list := htmlquery.Find(doc, "//a/@href")
	for _, n := range list {
		link := htmlquery.SelectAttr(n, "href")
		u, err := url.Parse(link)
		if err != nil {
			fmt.Fprintln(debug, link, err)
			return
		}
		link = base.ResolveReference(u).String()
		if !visited[link] {
			fmt.Fprintln(debug, link, "crawling...")
			time.Sleep(time.Second)
			visited[link] = true
			Crawl(link)
		} else {
			fmt.Fprintln(debug, link, "skipping")
		}
	}
}

func isOffsite(link, start string) bool {
	return !strings.HasPrefix(link, start)
}

func canonicalize(link string) string {
	if !strings.HasPrefix(link, "http") {
		return os.Args[1] + link
	}
	return link
}
