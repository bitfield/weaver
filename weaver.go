package weaver

import (
	"context"
	"crypto/tls"
	"errors"
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

const (
	maxRate       rate.Limit = 5
	fakeUserAgent            = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"
)

type Checker struct {
	Verbose          bool
	Output           io.Writer
	BaseURL          *url.URL
	HTTPClient       *http.Client
	results          []Result
	limiter          *rate.Limiter
	lastRateLimitSet time.Time
	visited          map[string]bool
}

func NewChecker() *Checker {
	return &Checker{
		Verbose: false,
		Output:  os.Stdout,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		limiter:          rate.NewLimiter(maxRate, 1),
		lastRateLimitSet: time.Now(),
		visited:          map[string]bool{},
	}
}

func (c *Checker) Check(ctx context.Context, page string) {
	base, err := url.Parse(page)
	if err != nil {
		c.RecordResult(page, "START", err, nil)
		return
	}
	c.BaseURL = base
	if !strings.HasSuffix(page, "/") {
		page += "/"
	}
	c.visited[page] = true
	c.Crawl(ctx, page, "START")
}

func (c *Checker) Crawl(ctx context.Context, page, referrer string) {
	c.limiter.Wait(ctx)
	req, err := http.NewRequest("GET", page, nil)
	if err != nil {
		c.RecordResult(page, referrer, err, nil)
		return
	}
	req.Header.Set("User-Agent", fakeUserAgent)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		c.RecordResult(page, referrer, err, resp)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		c.ReduceRateLimit()
		c.lastRateLimitSet = time.Now()
		c.Crawl(ctx, page, referrer)
		return
	}
	curLimit := c.limiter.Limit()
	if curLimit < maxRate && time.Since(c.lastRateLimitSet) > 10*time.Second {
		curLimit *= 1.5
		if curLimit > maxRate {
			curLimit = maxRate
		}
		c.limiter.SetLimit(curLimit)
		c.lastRateLimitSet = time.Now()
		if c.Verbose {
			fmt.Fprintf(c.Output, "[INFO] increasing rate limit to %.2fr/s\n", curLimit)
		}
	}
	c.RecordResult(page, referrer, err, resp)
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
			c.RecordResult(link, page, err, nil)
			return
		}
		if u.Scheme == "mailto" {
			continue
		}
		link = c.BaseURL.ResolveReference(u).String()
		if !c.visited[link] {
			c.visited[link] = true
			c.Crawl(ctx, link, page)
		}
	}
}

func (c *Checker) RecordResult(page, referrer string, err error, resp *http.Response) {
	res := Result{
		Status:   StatusError,
		Link:     page,
		Referrer: referrer,
	}
	if err != nil {
		res.Message = err.Error()
		var e *tls.CertificateVerificationError
		if errors.As(err, &e) {
			res.Status = StatusWarning
		}
		c.results = append(c.results, res)
		return
	}
	res.Message = resp.Status
	switch resp.StatusCode {
	case http.StatusOK:
		res.Status = StatusOK
	case http.StatusNotFound,
		http.StatusNotAcceptable,
		http.StatusGone,
		http.StatusUnauthorized,
		http.StatusBadRequest,
		http.StatusForbidden:
		res.Status = StatusError
	default:
		res.Status = StatusWarning
	}
	if res.Status == StatusError || res.Status == StatusWarning || c.Verbose {
		fmt.Fprintln(c.Output, res)
	}
	c.results = append(c.results, res)
}

func (c *Checker) Results() []Result {
	return c.results
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
	case StatusOK, StatusSkipped:
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
	StatusSkipped Status = "SKIP"
)

func Main() int {
	verbose := flag.Bool("v", false, "verbose output")
	flag.Parse()
	site := flag.Args()[0]
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	c := NewChecker()
	c.Verbose = *verbose
	start := time.Now()
	go func() {
		c.Check(ctx, site)
		cancel()
	}()
	<-ctx.Done()
	results := c.Results()
	ok, errors, warnings := 0, 0, 0
	if len(results) > 0 {
		for _, link := range results {
			switch link.Status {
			case StatusOK, StatusSkipped:
				ok++
			case StatusError:
				fmt.Println(link)
				errors++
			case StatusWarning:
				fmt.Println(link)
				warnings++
			}
		}
	}
	fmt.Printf("\nLinks: %d (%d OK, %d errors, %d warnings) [%s]\n",
		ok,
		ok-errors-warnings,
		errors, warnings,
		time.Since(start).Round(100*time.Millisecond),
	)
	return 0
}
