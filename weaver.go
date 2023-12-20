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
	"time"

	"github.com/antchfx/htmlquery"
	"github.com/fatih/color"
	"golang.org/x/time/rate"
)

const maxRate = 5

var (
	start         = time.Now()
	visited       = map[string]bool{}
	warning       []Result
	success       int
	limiter       = rate.NewLimiter(maxRate, 1)
	lastRateLimit time.Time
)

type Checker struct {
	Verbose    bool
	Output     io.Writer
	BaseURL    *url.URL
	HTTPClient *http.Client
	results    []Result
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
	visited[page] = true
	c.Crawl(ctx, page, "START")
}

func (c *Checker) Crawl(ctx context.Context, page, referrer string) {
	limiter.Wait(ctx)
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
		limiter.SetLimit(limiter.Limit() / 2)
		if c.Verbose {
			fmt.Fprintf(c.Output, "[INFO] reducing rate limit to %.2fr/s\n", limiter.Limit())
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
	for _, n := range list {
		link := htmlquery.SelectAttr(n, "href")
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
		if !visited[link] {
			visited[link] = true
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
		len(visited)+1,
		success,
		len(broken),
		len(warning),
		time.Since(start).Round(100*time.Millisecond),
	)
	return 0
}
