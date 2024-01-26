package weaver_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitfield/weaver"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/time/rate"
)

func TestCrawlDetectsInvalidLinks(t *testing.T) {
	t.Parallel()

	ts := httptest.NewTLSServer(
		http.FileServer(http.Dir("testdata/crawl")),
	)
	defer ts.Close()

	c := weaver.NewChecker()
	c.HTTPClient = ts.Client()
	c.Output = io.Discard
	c.Check(context.Background(), ts.URL+"/invalid_link.html")

	want := []weaver.Result{
		{
			Link:     ts.URL + "/invalid_link.html/",
			Status:   weaver.StatusOK,
			Message:  "200 OK",
			Referrer: "START",
		},
		{
			Link:     "httq://invalid_scheme.html",
			Status:   weaver.StatusError,
			Message:  `Get "httq://invalid_scheme.html": unsupported protocol scheme "httq"`,
			Referrer: ts.URL + "/invalid_link.html/",
		},
		{
			Link:     "http:// /",
			Status:   weaver.StatusError,
			Message:  `parse "http:// /": invalid character " " in host name`,
			Referrer: ts.URL + "/invalid_link.html/",
		},
	}

	got := c.Results()
	if !cmp.Equal(want, got) {
		t.Error(cmp.Diff(want, got))
	}
}

func TestCrawlReturnsExpectedResults(t *testing.T) {
	t.Parallel()
	ts := httptest.NewTLSServer(
		http.FileServer(http.Dir("testdata/crawl")),
	)
	defer ts.Close()

	c := weaver.NewChecker()
	c.HTTPClient = ts.Client()
	c.Output = io.Discard
	c.Check(context.Background(), ts.URL)
	want := []weaver.Result{
		{
			Link:     ts.URL + "/",
			Status:   weaver.StatusOK,
			Message:  "200 OK",
			Referrer: "START",
		},
		{
			Link:     ts.URL + "/go_sucks.html",
			Status:   weaver.StatusOK,
			Message:  "200 OK",
			Referrer: ts.URL + "/",
		},
		{
			Link:     ts.URL + "/bogus",
			Status:   weaver.StatusError,
			Message:  "404 Not Found",
			Referrer: ts.URL + "/go_sucks.html",
		},
		{
			Link:     ts.URL + "/rust_rules.html",
			Status:   weaver.StatusError,
			Message:  "404 Not Found",
			Referrer: ts.URL + "/",
		},
	}
	got := c.Results()
	if !cmp.Equal(want, got) {
		fmt.Println(got)
		t.Error(cmp.Diff(want, got))
	}
}

func TestReduceRateLimit_SetsCorrectLimit(t *testing.T) {
	t.Parallel()
	c := weaver.NewChecker()
	c.SetRateLimit(4)
	c.ReduceRateLimit()
	want := rate.Limit(2)
	got := c.RateLimit()
	if want != got {
		t.Errorf("want %.2f, got %.2f", want, got)
	}
}
