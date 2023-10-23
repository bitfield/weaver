package weaver

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	start         = time.Now()
	visited       = map[string]bool{}
	warning       []string
	success       int
	limiter       = rate.NewLimiter(maxRate, 1)
	lastRateLimit time.Time
)

type Checker struct {
	Verbose    bool
	Output     io.Writer
	Base       *url.URL
	HTTPClient *http.Client
	broken     []string
}

func NewChecker() *Checker {
	return &Checker{
		Verbose: false,
		Output:  os.Stdout,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *Checker) Check(ctx context.Context, page string) {
	base, err := url.Parse(page)
	if err != nil {
		msg := fmt.Sprintf("[%s] %s: %s (referrer %s)\n", color.RedString("ERR"), page, err, "START")
		c.broken = append(c.broken, msg)
		return
	}
	c.Base = base
	c.Crawl(ctx, page, "START")
}

func (c *Checker) Crawl(ctx context.Context, page, referrer string) {
	limiter.Wait(ctx)
	resp, err := c.HTTPClient.Get(page)
	if err != nil {
		msg := fmt.Sprintf("[%s] %s: %s (referrer %s)\n", color.RedString("ERR"), page, err, referrer)
		c.broken = append(c.broken, msg)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		limiter.SetLimit(limiter.Limit() / 2)
		if c.Verbose {
			fmt.Fprintf(c.Output, "[%s] %s, reducing rate limit to %.2fr/s\n", color.RedString("LMT"), page, limiter.Limit())
		}
		lastRateLimit = time.Now()
		c.Crawl(ctx, page, referrer)
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
		if c.Verbose {
			fmt.Fprintf(c.Output, "[%s] increasing rate limit to %.2fr/s\n", color.GreenString("LMT"), curLimit)
		}
	}
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("[%s] %s (referrer: %s)\n",
			color.RedString(strconv.Itoa(resp.StatusCode)),
			page, referrer,
		)
		fmt.Fprint(c.Output, msg)
		if resp.StatusCode == http.StatusNotFound {
			c.broken = append(c.broken, msg)
		} else {
			warning = append(warning, msg)
		}
		return
	}
	success++
	if c.Verbose {
		fmt.Fprintf(c.Output, "[%s] %s (referrer: %s)\n",
			color.GreenString(strconv.Itoa(resp.StatusCode)),
			page, referrer,
		)
	}
	if !strings.HasPrefix(page, c.Base.String()) {
		return // skip parsing offsite pages
	}
	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		fmt.Fprintf(c.Output, "[%s] %s: %s (referrer: %s)\n", color.RedString("ERR"), page, err, referrer)
	}
	list := htmlquery.Find(doc, "//a/@href")
	for _, n := range list {
		link := htmlquery.SelectAttr(n, "href")
		u, err := url.Parse(link)
		if err != nil {
			msg := fmt.Sprintf("[%s] %s (referrer: %s)\n", color.RedString("ERR"), page, referrer)
			fmt.Fprint(c.Output, msg)
			c.broken = append(c.broken, msg)
			return
		}
		link = c.Base.ResolveReference(u).String()
		if !visited[link] {
			visited[link] = true
			c.Crawl(ctx, link, page)
		}
	}
}

func (c *Checker) Broken() []string {
	return c.broken
}

type Result struct{}

func Main() int {
	verbose := flag.Bool("v", false, "verbose output")
	flag.Parse()
	site := flag.Args()[0]
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	c := NewChecker()
	c.Verbose = *verbose
	go func() {
		c.Check(ctx, site)
		cancel()
	}()
	<-ctx.Done()
	broken := c.Broken()
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
