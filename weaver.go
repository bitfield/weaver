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
	"strconv"
	"strings"
	"time"

	"github.com/antchfx/htmlquery"
	"github.com/fatih/color"
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
		msg := fmt.Sprintf("[%s] %s: %s (referrer %s)\n", color.RedString("ERR"), page, err, referrer)
		broken = append(broken, msg)
		return
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		limiter.SetLimit(limiter.Limit() / 2)
		if *verbose {
			fmt.Printf("[%s] %s, reducing rate limit to %.2fr/s\n", color.RedString("LMT"), page, limiter.Limit())
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
			fmt.Printf("[%s] increasing rate limit to %.2fr/s\n", color.GreenString("LMT"), curLimit)
		}
	}
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("[%s] %s (referrer: %s)\n",
			color.RedString(strconv.Itoa(resp.StatusCode)),
			page, referrer,
		)
		fmt.Print(msg)
		if resp.StatusCode == http.StatusNotFound {
			broken = append(broken, msg)
		} else {
			warning = append(warning, msg)
		}
		return
	}
	success++
	if *verbose {
		fmt.Printf("[%s] %s (referrer: %s)\n",
			color.GreenString(strconv.Itoa(resp.StatusCode)),
			page, referrer,
		)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(page, base.String()) {
		return // skip parsing offsite pages
	}
	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		fmt.Printf("[%s] %s: %s (referrer: %s)\n", color.RedString("ERR"), page, err, referrer)
	}
	list := htmlquery.Find(doc, "//a/@href")
	for _, n := range list {
		link := htmlquery.SelectAttr(n, "href")
		u, err := url.Parse(link)
		if err != nil {
			msg := fmt.Sprintf("[%s] %s (referrer: %s)\n", color.RedString("ERR"), page, referrer)
			fmt.Print(msg)
			broken = append(broken, msg)
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
	go func() {
		Crawl(ctx, base.String(), "CLI")
		cancel()
	}()
	<-ctx.Done()
	if len(broken) > 0 {
		fmt.Println("\nBroken links:")
		fmt.Print(strings.Join(broken, ""))
	}
	fmt.Printf("\nLinks: %d (%d OK, %d broken, %d warnings) [%s]\n",
		len(visited)+1,
		success,
		len(broken),
		len(warning),
		time.Since(start).Round(100*time.Millisecond),
	)
	return 0
}
