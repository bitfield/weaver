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
)

func TestCrawlReturnsExpectedResults(t *testing.T) {
	t.Parallel()
	ts := httptest.NewTLSServer(
		http.FileServer(http.Dir("testdata/crawl")),
	)
	c := weaver.NewChecker()
	c.HTTPClient = ts.Client()
	c.Output = io.Discard
	c.Check(context.Background(), ts.URL)
	want := []string{
		fmt.Sprintf("[\x1b[31m404\x1b[0m] %s/bogus (referrer: %[1]s/go_sucks.html)\n", ts.URL),
		fmt.Sprintf("[\x1b[31m404\x1b[0m] %s/rust_rules.html (referrer: %[1]s/)\n", ts.URL),
	}
	got := c.Broken()
	if !cmp.Equal(want, got) {
		t.Error(cmp.Diff(want, got))
	}
}
