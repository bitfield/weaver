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
	Verbose    bool
	Output     io.Writer
	BaseURL    *url.URL
	HTTPClient *http.Client
	Limiter    *AdaptiveRateLimiter
	results    []Result
	visited    map[string]bool
}

func NewChecker() *Checker {
	return &Checker{
		Verbose: false,
		Output:  os.Stdout,
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		Limiter: NewAdaptiveRateLimiter(),
		visited: map[string]bool{},
	}
}

func (c *Checker) Check(ctx context.Context, site string) {
	base, err := url.Parse(site)
	if err != nil {
		c.RecordResult(site, "START", err, nil)
		return
	}
	c.BaseURL = base
	if !strings.HasSuffix(site, "/") {
		site += "/"
	}
	c.visited[site] = true
	c.Crawl(ctx, base, "START")
}

func (c *Checker) Crawl(ctx context.Context, page *url.URL, referrer string) {
	c.Limiter.Wait(ctx)
	req, err := http.NewRequest("GET", page.String(), nil)
	if err != nil {
		c.RecordResult(page.String(), referrer, err, nil)
		return
	}
	req.Header.Set("User-Agent", fakeUserAgent)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		c.RecordResult(page.String(), referrer, err, resp)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		c.Limiter.ReduceLimit()
		if c.Verbose {
			fmt.Fprintf(c.Output, "[INFO] reducing rate limit to %.2fr/s\n", c.Limiter.Limit())
		}
		c.Crawl(ctx, page, referrer)
		return
	}
	if c.Limiter.GraduallyIncreaseRateLimit() && c.Verbose {
		fmt.Fprintf(c.Output, "[INFO] increasing rate limit to %.2fr/s\n", c.Limiter.Limit())
	}
	c.RecordResult(page.String(), referrer, err, resp)
	if page.Host != c.BaseURL.Host {
		return // skip parsing offsite pages
	}
	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		return // skip invalid HTML
	}
	list := htmlquery.Find(doc, "//a/@href")
	for _, anchor := range list {
		link := htmlquery.SelectAttr(anchor, "href")
		u, err := url.Parse(link)
		if err != nil {
			c.RecordResult(link, page.String(), err, nil)
			return
		}
		if u.Scheme == "mailto" {
			continue
		}
		target := page.ResolveReference(u)
		if !c.visited[target.String()] {
			c.visited[target.String()] = true
			c.Crawl(ctx, target, page.String())
		}
	}
}

func (c *Checker) RecordResult(link, referrer string, err error, resp *http.Response) {
	res := Result{
		Status:   StatusError,
		Link:     link,
		Referrer: referrer,
	}
	if err != nil {
		res.Message = err.Error()
		var e *tls.CertificateVerificationError
		if errors.As(err, &e) {
			res.Status = StatusWarning
		}
		fmt.Fprintln(c.Output, res)
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

type Result struct {
	Link     string
	Status   Status
	Message  string
	Referrer string
}

func (r Result) String() string {
	return fmt.Sprintf("[%s] %s (%s) (referrer: %s)",
		r.Status,
		r.Link,
		r.Message,
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

var usage = `Usage: weaver [-v] URL

Checks the website at URL, following all links and reporting any broken links or errors.

In verbose mode (-v), reports all links found.`

func Main() int {
	verbose := flag.Bool("v", false, "verbose output")
	flag.Parse()
	if len(flag.Args()) == 0 {
		fmt.Println(usage)
		return 0
	}
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
				errors++
			case StatusWarning:
				warnings++
			}
		}
	}
	fmt.Printf("\nLinks: %d (%d OK, %d errors, %d warnings) [%s]\n",
		len(results), ok, errors, warnings,
		time.Since(start).Round(100*time.Millisecond),
	)
	return 0
}

type AdaptiveRateLimiter struct {
	limiter          *rate.Limiter
	limitLastUpdated time.Time
}

func NewAdaptiveRateLimiter() *AdaptiveRateLimiter {
	return &AdaptiveRateLimiter{
		limiter:          rate.NewLimiter(maxRate, 1),
		limitLastUpdated: time.Now(),
	}
}

func (a *AdaptiveRateLimiter) Wait(ctx context.Context) {
	a.limiter.Wait(ctx)
}

func (a *AdaptiveRateLimiter) GraduallyIncreaseRateLimit() (increased bool) {
	curLimit := a.limiter.Limit()
	if curLimit >= maxRate {
		return false
	}
	if time.Since(a.limitLastUpdated) <= 10*time.Second {
		return false
	}
	curLimit *= 1.5
	if curLimit > maxRate {
		curLimit = maxRate
	}
	a.limiter.SetLimit(curLimit)
	a.limitLastUpdated = time.Now()
	return true
}

func (a *AdaptiveRateLimiter) ReduceLimit() {
	curLimit := a.limiter.Limit()
	a.limiter.SetLimit(curLimit / 2)
	a.limitLastUpdated = time.Now()
}

func (a AdaptiveRateLimiter) Limit() rate.Limit {
	return a.limiter.Limit()
}

func (a AdaptiveRateLimiter) SetLimit(r rate.Limit) {
	a.limiter.SetLimit(r)
}
