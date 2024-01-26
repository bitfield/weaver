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
	"strings"
	"sync"
	"time"

	"github.com/antchfx/htmlquery"
	"github.com/fatih/color"
	"golang.org/x/time/rate"
)

const maxRate rate.Limit = 5

var (
	start            = time.Now()
	warning          []Result
	success          int
	lastRateLimitSet time.Time
)

type Checker struct {
	Verbose    bool
	Output     io.Writer
	BaseURL    *url.URL
	HTTPClient *http.Client
	results    []Result
	limiter    *rate.Limiter

	mu      sync.Mutex
	visited map[string]bool
}

func NewChecker() *Checker {
	return &Checker{
		Verbose: false,
		Output:  os.Stdout,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		limiter: rate.NewLimiter(maxRate, 1),
		visited: make(map[string]bool),
	}
}

func (c *Checker) Check(ctx context.Context, page string) {
	base, err := url.Parse(page)
	if err != nil {
		c.Record(Result{
			Status:   StatusError,
			Message:  err.Error(),
			Link:     page,
			Referrer: "START",
		})
		return
	}
	c.BaseURL = base
	if !strings.HasSuffix(page, "/") {
		page += "/"
	}
	c.mu.Lock()
	c.visited[page] = true
	c.mu.Unlock()
	c.Crawl(ctx, page, "START")
}

func (c *Checker) Crawl(ctx context.Context, page, referrer string) {
	c.limiter.Wait(ctx)
	resp, err := c.HTTPClient.Get(page)
	if err != nil {
		c.Record(Result{
			Status:   StatusError,
			Message:  err.Error(),
			Link:     page,
			Referrer: referrer,
		})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		c.ReduceRateLimit()
		lastRateLimitSet = time.Now()
		c.Crawl(ctx, page, referrer)
		return
	}
	curLimit := c.limiter.Limit()
	if curLimit < maxRate && time.Since(lastRateLimitSet) > 10*time.Second {
		curLimit *= 1.5
		if curLimit > maxRate {
			curLimit = maxRate
		}
		c.limiter.SetLimit(curLimit)
		lastRateLimitSet = time.Now()
		if c.Verbose {
			fmt.Fprintf(c.Output, "[INFO] increasing rate limit to %.2fr/s\n", curLimit)
		}
	}
	result := Result{
		Message:  resp.Status,
		Link:     page,
		Referrer: referrer,
	}
	switch resp.StatusCode {
	case http.StatusOK:
		result.Status = StatusOK
	case http.StatusNotFound:
		result.Status = StatusError
	default:
		result.Status = StatusWarning
	}
	c.Record(result)
	success++
	if !strings.HasPrefix(page, c.BaseURL.String()) {
		return // skip parsing offsite pages
	}
	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		fmt.Fprintf(c.Output, "[%s] %s: %s (referrer: %s)\n", color.RedString("ERR"), page, err, referrer)
	}
	list := htmlquery.Find(doc, "//a/@href")
	for _, anchor := range list {
		link := htmlquery.SelectAttr(anchor, "href")
		u, err := url.Parse(link)
		if err != nil {
			c.Record(Result{
				Status:   StatusError,
				Message:  err.Error(),
				Link:     link,
				Referrer: page,
			})
			return
		}
		link = c.BaseURL.ResolveReference(u).String()

		if !c.visited[link] {
			c.visited[link] = true
			c.Crawl(ctx, link, page)
		}
	}
}

func (c *Checker) Record(r Result) {
	if r.Status != StatusOK || c.Verbose {
		fmt.Fprintln(c.Output, r)
	}
	c.results = append(c.results, r)
}

func (c *Checker) Results() []Result {
	return c.results
}

func (c *Checker) BrokenLinks() []Result {
	var broken []Result
	for _, r := range c.results {
		if r.Status != StatusOK {
			broken = append(broken, r)
		}
	}
	return broken
}

func (c *Checker) SetRateLimit(limit rate.Limit) {
	c.limiter.SetLimit(limit)
}

func (c *Checker) RateLimit() rate.Limit {
	return c.limiter.Limit()
}

func (c *Checker) ReduceRateLimit() {
	curLimit := c.RateLimit()
	c.SetRateLimit(curLimit / 2)
	if c.Verbose {
		fmt.Fprintf(c.Output, "[INFO] reducing rate limit to %.2fr/s\n", c.limiter.Limit())
	}
}

type Result struct {
	Link     string
	Status   Status
	Message  string
	Referrer string
}

func (r Result) String() string {
	return fmt.Sprintf("[%s] (%s) %s (referrer: %s)",
		r.Status,
		r.Message,
		r.Link,
		r.Referrer,
	)
}

type Status string

func (s Status) String() string {
	msg := string(s)
	switch s {
	case StatusOK:
		return color.GreenString(msg)
	case StatusWarning:
		return color.YellowString(msg)
	case StatusError:
		return color.RedString(msg)
	default:
		return msg
	}
}

const (
	StatusOK      Status = "OKAY"
	StatusWarning Status = "WARN"
	StatusError   Status = "DEAD"
)

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
	broken := c.BrokenLinks()
	if len(broken) > 0 {
		fmt.Println("\nBroken links:")
		for _, link := range broken {
			fmt.Println(link)
		}
	}
	fmt.Printf("\nLinks: %d (%d OK, %d broken, %d warnings) [%s]\n",
		len(c.visited)+1,
		success,
		len(broken),
		len(warning),
		time.Since(start).Round(100*time.Millisecond),
	)
	return 0
}
