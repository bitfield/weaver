package weaver

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/antchfx/htmlquery"
	"golang.org/x/time/rate"
)

const maxRate = 5

var (
	start           = time.Now()
	visited         = map[string]bool{}
	warning, broken []string
	success         int
	limiter         = rate.NewLimiter(maxRate, 1)
	lastRateLimit   time.Time
	base            *url.URL
	verbose         *bool
	httpClient      = &http.Client{
		Timeout: 5 * time.Second,
	}
)

func Crawl(ctx context.Context, page, referrer string) {
	limiter.Wait(ctx)
	resp, err := httpClient.Get(page)
	if err != nil {
		fmt.Printf("[%s] %s (referrer %s)\n", err, page, referrer)
		broken = append(broken, fmt.Sprintf("[%s] %s (referrer %s)\n", err, page, referrer))
		return
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		limiter.SetLimit(limiter.Limit() / 2)
		if *verbose {
			fmt.Printf("[%s] %s, reducing rate limit to %.2fr/s\n", resp.Status, page, limiter.Limit())
		}
		lastRateLimit = time.Now()
		Crawl(ctx, page, referrer)
		return
	}
	curLimit := limiter.Limit()
	if curLimit < maxRate && time.Since(lastRateLimit) > 10*time.Second {
		curLimit *= 1.5
		if curLimit > maxRate {
			curLimit = maxRate
		}
		limiter.SetLimit(curLimit)
		lastRateLimit = time.Now()
		if *verbose {
			fmt.Printf("increasing rate limit to %.2fr/s\n", curLimit)
		}
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("[%s] %s (referrer: %s)\n", resp.Status, page, referrer)
		if resp.StatusCode == http.StatusNotFound {
			broken = append(broken, fmt.Sprintf("[%s] %s (referrer: %s)\n", resp.Status, page, referrer))
		} else {
			warning = append(warning, fmt.Sprintf("[%s] %s (referrer: %s)\n", resp.Status, page, referrer))
		}
		return
	}
	success++
	if *verbose {
		fmt.Printf("[%s] %s (referrer: %s)\n", resp.Status, page, referrer)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(page, base.String()) {
		return // skip parsing offsite pages
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
			broken = append(broken, fmt.Sprintf("[%s] %s (referrer: %s)\n", err, link, referrer))
			return
		}
		link = base.ResolveReference(u).String()
		if !visited[link] {
			visited[link] = true
			Crawl(ctx, link, page)
		}
	}
}

func Main() int {
	verbose = flag.Bool("v", false, "verbose output")
	flag.Parse()
	tmp, err := url.Parse(flag.Args()[0])
	base = tmp
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	go Crawl(ctx, base.String(), "CLI")
	<-ctx.Done()
	if len(broken) > 0 {
		fmt.Println("\nBroken links:")
		fmt.Println(strings.Join(broken, ""))
	}
	fmt.Printf("\nTime: %s Visited: %d Links: %d OK: %d Broken: %d Warning: %d\n",
		time.Since(start).Round(time.Second),
		len(visited),
		success+len(broken)+len(warning),
		success,
		len(broken),
		len(warning),
	)
	return 0
}
